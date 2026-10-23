package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"wifibilling/models"
	"wifibilling/services"
)

// paginate applies ?limit= (default 50, max 200) and ?offset= to a query.
func Paginate(c *gin.Context, q *gorm.DB) *gorm.DB {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return q.Limit(limit).Offset(offset)
}

// GET /api/v1/gateway/check?mac=AA:BB:...
// The router calls this to decide whether to let a device through
// and which speed limits to apply.
func (h *Handler) GatewayCheck(c *gin.Context) {
	mac, ok := services.NormalizeMAC(c.Query("mac"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid MAC address"})
		return
	}
	var s models.Session
	err := h.DB.Where("mac = ? AND active = ? AND expires_at > ?", mac, true, time.Now()).
		Order("expires_at DESC").First(&s).Error
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"allowed": false})
		return
	}
	resp := gin.H{
		"allowed":           true,
		"remaining_seconds": int(time.Until(s.ExpiresAt).Seconds()),
		"expires_at":        s.ExpiresAt,
		"down_kbps":         s.DownKbps,
		"up_kbps":           s.UpKbps,
	}
	if s.DataLimitBytes > 0 {
		resp["data_remaining_bytes"] = s.DataLimitBytes - s.DataUsedBytes
	}
	c.JSON(http.StatusOK, resp)
}

// GET /api/v1/gateway/active
// Full list of currently-authorised MACs, so the router can sync its allow-list.
func (h *Handler) GatewayActive(c *gin.Context) {
	var sessions []models.Session
	h.DB.Where("active = ? AND expires_at > ?", true, time.Now()).Find(&sessions)
	out := make([]gin.H, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, gin.H{
			"mac": s.MAC, "expires_at": s.ExpiresAt,
			"down_kbps": s.DownKbps, "up_kbps": s.UpKbps,
		})
	}
	c.JSON(http.StatusOK, out)
}

// POST /api/v1/gateway/usage {mac, bytes}
// The router reports bytes transferred since its last report. If a data cap is
// reached the session is closed and the response tells the router to kick the device.
func (h *Handler) GatewayUsage(c *gin.Context) {
	var req struct {
		MAC   string `json:"mac" binding:"required"`
		Bytes int64  `json:"bytes" binding:"required,gt=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mac and positive bytes required"})
		return
	}
	mac, ok := services.NormalizeMAC(req.MAC)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid MAC address"})
		return
	}

	var s models.Session
	if err := h.DB.Where("mac = ? AND active = ?", mac, true).
		Order("expires_at DESC").First(&s).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"allowed": false})
		return
	}
	s.DataUsedBytes += req.Bytes
	allowed := true
	if s.DataLimitBytes > 0 && s.DataUsedBytes >= s.DataLimitBytes {
		s.Active, s.EndReason, allowed = false, "data_exhausted", false
	}
	if err := h.DB.Save(&s).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"allowed": allowed, "data_used_bytes": s.DataUsedBytes})
}
