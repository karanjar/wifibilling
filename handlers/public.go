package handlers

import (
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wifibilling/config"
	"wifibilling/models"
	"wifibilling/services"
)

type Handler struct {
	DB     *gorm.DB
	Cfg    *config.Config
	M_pesa *services.Mpesa
}

// GET /api/v1/plans
func (h *Handler) ListPlans(c *gin.Context) {
	var plans []models.Plan
	if err := h.DB.Where("active = ?", true).Order("price_kes ASC").Find(&plans).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	c.JSON(http.StatusOK, plans)
}

type payReq struct {
	PlanID uint   `json:"plan_id" binding:"required"`
	Phone  string `json:"phone" binding:"required"`
	MAC    string `json:"mac" binding:"required"`
}

// POST /api/v1/pay/mpesa  -> triggers STK push on the customer's phone
func (h *Handler) PayMpesa(c *gin.Context) {
	var req payReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plan_id, phone and mac are required"})
		return
	}
	phone, ok := services.NormalizePhone(req.Phone)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid Safaricom phone number"})
		return
	}
	mac, ok := services.NormalizeMAC(req.MAC)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid MAC address"})
		return
	}
	var plan models.Plan
	if err := h.DB.Where("id = ? AND active = ?", req.PlanID, true).First(&plan).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
		return
	}

	pay := models.Payment{
		PlanID: plan.ID, Phone: phone, MAC: mac,
		AmountKES: plan.PriceKES, Status: models.PaymentPending,
		// temporary unique placeholder until Safaricom returns the real ID
		CheckoutRequestID: "pending-" + time.Now().Format("20060102150405.000000000"),
	}
	if err := h.DB.Create(&pay).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}

	stk, err := h.M_pesa.STKPush(phone, plan.PriceKES, "WiFi", plan.Name)
	if err != nil {
		log.Println("stk push error:", err)
		h.DB.Model(&pay).Updates(map[string]any{"status": models.PaymentFailed, "result_desc": "stk push failed"})
		c.JSON(http.StatusBadGateway, gin.H{"error": "could not start M-Pesa payment, try again"})
		return
	}
	h.DB.Model(&pay).Updates(map[string]any{
		"checkout_request_id": stk.CheckoutRequestID,
		"merchant_request_id": stk.MerchantRequestID,
	})

	c.JSON(http.StatusOK, gin.H{
		"checkout_request_id": stk.CheckoutRequestID,
		"message":             "Enter your M-Pesa PIN on your phone to complete payment",
	})
}

// GET /api/v1/pay/status/:checkout_id  -> portal polls this every few seconds
func (h *Handler) PaymentStatus(c *gin.Context) {
	var pay models.Payment
	if err := h.DB.Where("checkout_request_id = ?", c.Param("checkout_id")).First(&pay).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "payment not found"})
		return
	}
	resp := gin.H{"status": pay.Status, "message": pay.ResultDesc}
	if pay.Status == models.PaymentCompleted {
		var s models.Session
		if err := h.DB.Where("mac = ? AND active = ?", pay.MAC, true).Order("expires_at DESC").First(&s).Error; err == nil {
			resp["expires_at"] = s.ExpiresAt
		}
	}
	c.JSON(http.StatusOK, resp)
}

// POST /api/v1/mpesa/callback/:secret  <- called by Safaricom
func (h *Handler) MpesaCallback(c *gin.Context) {
	ack := gin.H{"ResultCode": 0, "ResultDesc": "Accepted"}

	if subtle.ConstantTimeCompare([]byte(c.Param("secret")), []byte(h.Cfg.MpesaCallbackSecret)) != 1 {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	var cb services.STKCallback
	if err := c.ShouldBindJSON(&cb); err != nil {
		c.JSON(http.StatusOK, ack)
		return
	}
	r := cb.Body.StkCallback

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		var pay models.Payment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("checkout_request_id = ?", r.CheckoutRequestID).First(&pay).Error; err != nil {
			return err
		}
		if pay.Status != models.PaymentPending { // idempotent: callbacks can repeat
			return nil
		}
		if r.ResultCode != 0 {
			return tx.Model(&pay).Updates(map[string]any{
				"status": models.PaymentFailed, "result_desc": r.ResultDesc}).Error
		}

		receipt, amount := cb.Meta()
		if int(amount) < pay.AmountKES { // underpaid: don't grant access
			return tx.Model(&pay).Updates(map[string]any{
				"status": models.PaymentFailed, "mpesa_receipt": receipt,
				"result_desc": "amount mismatch"}).Error
		}
		var plan models.Plan
		if err := tx.First(&plan, pay.PlanID).Error; err != nil {
			return err
		}
		if err := tx.Model(&pay).Updates(map[string]any{
			"status": models.PaymentCompleted, "mpesa_receipt": receipt,
			"result_desc": "paid"}).Error; err != nil {
			return err
		}
		_, err := services.ActivateSession(tx, pay.MAC, plan, &pay.ID, nil)
		return err
	})
	if err != nil {
		log.Println("mpesa callback error:", err)
	}
	c.JSON(http.StatusOK, ack)
}

type redeemReq struct {
	Code string `json:"code" binding:"required"`
	MAC  string `json:"mac" binding:"required"`
}

// POST /api/v1/voucher/redeem
func (h *Handler) RedeemVoucher(c *gin.Context) {
	var req redeemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code and mac are required"})
		return
	}
	mac, ok := services.NormalizeMAC(req.MAC)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid MAC address"})
		return
	}
	code := strings.ToUpper(strings.TrimSpace(req.Code))

	var sess *models.Session
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		var v models.Voucher
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("code = ?", code).First(&v).Error; err != nil {
			return err
		}
		if v.Used {
			return errUsed
		}
		var plan models.Plan
		if err := tx.First(&plan, v.PlanID).Error; err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&v).Updates(map[string]any{
			"used": true, "used_by_mac": mac, "used_at": now}).Error; err != nil {
			return err
		}
		s, err := services.ActivateSession(tx, mac, plan, nil, &v.ID)
		sess = s
		return err
	})
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "invalid voucher code"})
	case errors.Is(err, errUsed):
		c.JSON(http.StatusConflict, gin.H{"error": "voucher already used"})
	case err != nil:
		log.Println("redeem error:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not redeem voucher"})
	default:
		c.JSON(http.StatusOK, gin.H{"message": "voucher redeemed", "expires_at": sess.ExpiresAt})
	}
}

var errUsed = errors.New("voucher used")

// GET /api/v1/session?mac=AA:BB:...  -> portal checks if a device already has access
func (h *Handler) SessionStatus(c *gin.Context) {
	mac, ok := services.NormalizeMAC(c.Query("mac"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid MAC address"})
		return
	}
	var s models.Session
	err := h.DB.Where("mac = ? AND active = ? AND expires_at > ?", mac, true, time.Now()).
		Order("expires_at DESC").First(&s).Error
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"active": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"active":            true,
		"expires_at":        s.ExpiresAt,
		"remaining_seconds": int(time.Until(s.ExpiresAt).Seconds()),
		"data_used_bytes":   s.DataUsedBytes,
		"data_limit_bytes":  s.DataLimitBytes,
	})
}
