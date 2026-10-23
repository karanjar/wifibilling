package models

import "time"

type Admin struct {
	ID           uint   `gorm:"primaryKey"`
	Username     string `gorm:"uniqueIndex;not null"`
	PasswordHash string `gorm:"not null"`
	CreatedAt    time.Time
}

// Plan is a purchasable package, e.g. "1 Hour - KES 20".
type Plan struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Name            string    `gorm:"not null" json:"name"`
	PriceKES        int       `gorm:"not null" json:"price_kes"`
	DurationMinutes int       `gorm:"not null" json:"duration_minutes"`
	DataLimitMB     int       `json:"data_limit_mb"` // 0 = unlimited
	DownKbps        int       `json:"down_kbps"`     // 0 = unlimited
	UpKbps          int       `json:"up_kbps"`
	Active          bool      `gorm:"default:true" json:"active"`
	CreatedAt       time.Time `json:"created_at"`
}

const (
	PaymentPending   = "pending"
	PaymentCompleted = "completed"
	PaymentFailed    = "failed"
)

type Payment struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	PlanID            uint      `gorm:"index" json:"plan_id"`
	Phone             string    `json:"phone"`
	MAC               string    `gorm:"index" json:"mac"`
	AmountKES         int       `json:"amount_kes"`
	Status            string    `gorm:"index;default:pending" json:"status"`
	CheckoutRequestID string    `gorm:"uniqueIndex" json:"checkout_request_id"`
	MerchantRequestID string    `json:"merchant_request_id"`
	MpesaReceipt      string    `json:"mpesa_receipt"`
	ResultDesc        string    `json:"result_desc"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Voucher struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	Code      string     `gorm:"uniqueIndex;not null" json:"code"`
	PlanID    uint       `gorm:"index" json:"plan_id"`
	Used      bool       `gorm:"index" json:"used"`
	UsedByMAC string     `json:"used_by_mac"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// Session is one customer's active (or past) internet access.
type Session struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	MAC            string    `gorm:"index;not null" json:"mac"`
	PlanID         uint      `json:"plan_id"`
	PaymentID      *uint     `json:"payment_id"`
	VoucherID      *uint     `json:"voucher_id"`
	StartsAt       time.Time `json:"starts_at"`
	ExpiresAt      time.Time `gorm:"index" json:"expires_at"`
	DataLimitBytes int64     `json:"data_limit_bytes"` // 0 = unlimited
	DataUsedBytes  int64     `json:"data_used_bytes"`
	DownKbps       int       `json:"down_kbps"`
	UpKbps         int       `json:"up_kbps"`
	Active         bool      `gorm:"index" json:"active"`
	EndReason      string    `json:"end_reason"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
