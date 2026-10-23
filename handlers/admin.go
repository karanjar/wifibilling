package handlers

import (
	"net/http"
	"time"
	//"wifibilling"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"wifibilling/models"
	"wifibilling/services"
)

// POST /api/v1/admin/login
func (h *Handler) AdminLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and password required"})
		return
	}
	var a models.Admin
	if err := h.DB.Where("username = ?", req.Username).First(&a).Error; err != nil ||
		bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(req.Password)) != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": a.Username,
		"exp": time.Now().Add(12 * time.Hour).Unix(),
	})
	signed, err := tok.SignedString([]byte(h.Cfg.JWTSecret))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": signed})
}

// ---- Plans ----

type planInput struct {
	Name            string `json:"name" binding:"required"`
	PriceKES        int    `json:"price_kes" binding:"required,gt=0"`
	DurationMinutes int    `json:"duration_minutes" binding:"required,gt=0"`
	DataLimitMB     int    `json:"data_limit_mb" binding:"gte=0"`
	DownKbps        int    `json:"down_kbps" binding:"gte=0"`
	UpKbps          int    `json:"up_kbps" binding:"gte=0"`
	Active          *bool  `json:"active"`
}

func (in planInput) apply(p *models.Plan) {
	p.Name, p.PriceKES, p.DurationMinutes = in.Name, in.PriceKES, in.DurationMinutes
	p.DataLimitMB, p.DownKbps, p.UpKbps = in.DataLimitMB, in.DownKbps, in.UpKbps
	if in.Active != nil {
		p.Active = *in.Active
	}
}

func (h *Handler) AdminListPlans(c *gin.Context) {
	var plans []models.Plan
	h.DB.Order("price_kes ASC").Find(&plans)
	c.JSON(http.StatusOK, plans)
}

func (h *Handler) AdminCreatePlan(c *gin.Context) {
	var in planInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p := models.Plan{Active: true}
	in.apply(&p)
	if err := h.DB.Create(&p).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	c.JSON(http.StatusCreated, p)
}

func (h *Handler) AdminUpdatePlan(c *gin.Context) {
	var p models.Plan
	if err := h.DB.First(&p, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
		return
	}
	var in planInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	in.apply(&p)
	h.DB.Save(&p)
	c.JSON(http.StatusOK, p)
}

// DELETE soft-disables the plan so historical payments keep their reference.
func (h *Handler) AdminDeletePlan(c *gin.Context) {
	res := h.DB.Model(&models.Plan{}).Where("id = ?", c.Param("id")).Update("active", false)
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
		return
	}
	c.Status(http.StatusNoContent)
}

// ---- Vouchers ----

// POST /admin/vouchers {plan_id, count}
func (h *Handler) AdminGenerateVouchers(c *gin.Context) {
	var req struct {
		PlanID uint `json:"plan_id" binding:"required"`
		Count  int  `json:"count" binding:"required,min=1,max=500"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plan_id and count (1-500) required"})
		return
	}
	var plan models.Plan
	if err := h.DB.First(&plan, req.PlanID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
		return
	}
	vouchers := make([]models.Voucher, 0, req.Count)
	for len(vouchers) < req.Count {
		code, err := services.GenerateVoucherCode(10)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "rng error"})
			return
		}
		vouchers = append(vouchers, models.Voucher{Code: code, PlanID: plan.ID})
	}
	if err := h.DB.Create(&vouchers).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error (duplicate code, retry)"})
		return
	}
	c.JSON(http.StatusCreated, vouchers)
}

func (h *Handler) AdminListVouchers(c *gin.Context) {
	q := h.DB.Model(&models.Voucher{})
	if used := c.Query("used"); used != "" {
		q = q.Where("used = ?", used == "true")
	}
	if pid := c.Query("plan_id"); pid != "" {
		q = q.Where("plan_id = ?", pid)
	}
	var out []models.Voucher
	Paginate(c, q.Order("id DESC")).Find(&out)
	c.JSON(http.StatusOK, out)
}

// ---- Payments / Sessions ----

func (h *Handler) AdminListPayments(c *gin.Context) {
	q := h.DB.Model(&models.Payment{})
	if s := c.Query("status"); s != "" {
		q = q.Where("status = ?", s)
	}
	if p := c.Query("phone"); p != "" {
		q = q.Where("phone = ?", p)
	}
	var out []models.Payment
	Paginate(c, q.Order("id DESC")).Find(&out)
	c.JSON(http.StatusOK, out)
}

func (h *Handler) AdminListSessions(c *gin.Context) {
	q := h.DB.Model(&models.Session{})
	if c.Query("active") == "true" {
		q = q.Where("active = ? AND expires_at > ?", true, time.Now())
	}
	var out []models.Session
	Paginate(c, q.Order("id DESC")).Find(&out)
	c.JSON(http.StatusOK, out)
}

// POST /admin/sessions/:id/revoke
func (h *Handler) AdminRevokeSession(c *gin.Context) {
	res := h.DB.Model(&models.Session{}).Where("id = ? AND active = ?", c.Param("id"), true).
		Updates(map[string]any{"active": false, "end_reason": "revoked"})
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "active session not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "session revoked"})
}

// GET /admin/stats
func (h *Handler) AdminStats(c *gin.Context) {
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	var today, month int64
	var active, pending int64
	sum := func(from time.Time, dst *int64) {
		h.DB.Model(&models.Payment{}).
			Where("status = ? AND created_at >= ?", models.PaymentCompleted, from).
			Select("COALESCE(SUM(amount_kes),0)").Scan(dst)
	}
	sum(startOfDay, &today)
	sum(startOfMonth, &month)
	h.DB.Model(&models.Session{}).Where("active = ? AND expires_at > ?", true, now).Count(&active)
	h.DB.Model(&models.Payment{}).Where("status = ?", models.PaymentPending).Count(&pending)

	c.JSON(http.StatusOK, gin.H{
		"revenue_today_kes": today,
		"revenue_month_kes": month,
		"active_sessions":   active,
		"pending_payments":  pending,
	})
}
