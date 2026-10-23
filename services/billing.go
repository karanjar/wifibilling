package services

import (
	"context"
	"crypto/rand"
	"errors"
	"log"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wifibilling/models"
)

var macRe = regexp.MustCompile(`^([0-9A-F]{2}:){5}[0-9A-F]{2}$`)

// NormalizeMAC accepts aa-bb-cc-dd-ee-ff or aa:bb:... and returns upper-case colon form.
func NormalizeMAC(s string) (string, bool) {
	s = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), "-", ":"))
	return s, macRe.MatchString(s)
}

// ActivateSession grants access for a MAC address. If the device already has an
// active session, the purchased time (and data) is stacked on top of it.
func ActivateSession(tx *gorm.DB, mac string, plan models.Plan, paymentID, voucherID *uint) (*models.Session, error) {
	now := time.Now()
	dur := time.Duration(plan.DurationMinutes) * time.Minute
	dataBytes := int64(plan.DataLimitMB) * 1024 * 1024

	var s models.Session
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("mac = ? AND active = ? AND expires_at > ?", mac, true, now).
		Order("expires_at DESC").First(&s).Error

	switch {
	case err == nil:
		s.ExpiresAt = s.ExpiresAt.Add(dur)
		if s.DataLimitBytes > 0 && dataBytes > 0 {
			s.DataLimitBytes += dataBytes
		} else if dataBytes == 0 {
			s.DataLimitBytes = 0 // unlimited plan upgrades the session
		}
		if plan.DownKbps == 0 || plan.DownKbps > s.DownKbps {
			s.DownKbps, s.UpKbps = plan.DownKbps, plan.UpKbps
		}
		s.PlanID = plan.ID
		return &s, tx.Save(&s).Error
	case errors.Is(err, gorm.ErrRecordNotFound):
		s = models.Session{
			MAC: mac, PlanID: plan.ID, PaymentID: paymentID, VoucherID: voucherID,
			StartsAt: now, ExpiresAt: now.Add(dur),
			DataLimitBytes: dataBytes, DownKbps: plan.DownKbps, UpKbps: plan.UpKbps,
			Active: true,
		}
		return &s, tx.Create(&s).Error
	default:
		return nil, err
	}
}

const voucherAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O/1/I

func GenerateVoucherCode(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i := range b {
		out[i] = voucherAlphabet[int(b[i])%len(voucherAlphabet)]
	}
	return string(out), nil
}

// ExpiryWorker deactivates sessions whose time has run out.
func ExpiryWorker(ctx context.Context, db *gorm.DB) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			res := db.Model(&models.Session{}).
				Where("active = ? AND expires_at <= ?", true, time.Now()).
				Updates(map[string]any{"active": false, "end_reason": "expired"})
			if res.Error != nil {
				log.Println("expiry worker:", res.Error)
			} else if res.RowsAffected > 0 {
				log.Printf("expired %d session(s)", res.RowsAffected)
			}
		}
	}
}
