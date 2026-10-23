package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wifibilling/config"
	"wifibilling/handlers"
	"wifibilling/middleware"
	"wifibilling/models"
	"wifibilling/services"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DBPath), &gorm.Config{})
	if err != nil {
		log.Fatal("open db: ", err)
	}
	if err := db.AutoMigrate(&models.Admin{}, &models.Plan{}, &models.Payment{}, &models.Voucher{}, &models.Session{}); err != nil {
		log.Fatal("migrate: ", err)
	}
	seed(db, cfg)

	h := &handlers.Handler{DB: db, Cfg: cfg, M_pesa: services.NewMpesa(cfg)}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go services.ExpiryWorker(ctx, db)

	r := gin.Default()
	r.Use(middleware.CORS())
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.StaticFile("/", "./web/index.html") // captive portal page

	v1 := r.Group("/api/v1")
	{
		// Captive portal (public)
		v1.GET("/plans", h.ListPlans)
		v1.POST("/pay/mpesa", h.PayMpesa)
		v1.GET("/pay/status/:checkout_id", h.PaymentStatus)
		v1.POST("/voucher/redeem", h.RedeemVoucher)
		v1.GET("/session", h.SessionStatus)

		// Safaricom callback (secret in path)
		v1.POST("/mpesa/callback/:secret", h.MpesaCallback)

		// Router / hotspot controller (API key)
		gw := v1.Group("/gateway", middleware.GatewayAuth(cfg.GatewayAPIKey))
		{
			gw.GET("/check", h.GatewayCheck)
			gw.GET("/active", h.GatewayActive)
			gw.POST("/usage", h.GatewayUsage)
		}

		// Admin
		v1.POST("/admin/login", h.AdminLogin)
		adm := v1.Group("/admin", middleware.AdminAuth(cfg.JWTSecret))
		{
			adm.GET("/stats", h.AdminStats)
			adm.GET("/plans", h.AdminListPlans)
			adm.POST("/plans", h.AdminCreatePlan)
			adm.PUT("/plans/:id", h.AdminUpdatePlan)
			adm.DELETE("/plans/:id", h.AdminDeletePlan)
			adm.GET("/vouchers", h.AdminListVouchers)
			adm.POST("/vouchers", h.AdminGenerateVouchers)
			adm.GET("/payments", h.AdminListPayments)
			adm.GET("/sessions", h.AdminListSessions)
			adm.POST("/sessions/:id/revoke", h.AdminRevokeSession)
		}
	}

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Println("listening on :" + cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// seed creates the first admin and some default plans on a fresh database.
func seed(db *gorm.DB, cfg *config.Config) {
	var n int64
	db.Model(&models.Admin{}).Count(&n)
	if n == 0 {
		hash, _ := bcrypt.GenerateFromPassword([]byte(cfg.AdminPass), bcrypt.DefaultCost)
		db.Create(&models.Admin{Username: cfg.AdminUser, PasswordHash: string(hash)})
		log.Println("created admin user:", cfg.AdminUser)
	}
	db.Model(&models.Plan{}).Count(&n)
	if n == 0 {
		db.Create(&[]models.Plan{
			{Name: "30 Minutes", PriceKES: 5, DurationMinutes: 30, DownKbps: 2048, UpKbps: 1024, Active: true},
			{Name: "1 Hour", PriceKES: 10, DurationMinutes: 60, DownKbps: 3072, UpKbps: 1024, Active: true},
			{Name: "Daily", PriceKES: 50, DurationMinutes: 1440, DownKbps: 5120, UpKbps: 2048, Active: true},
			{Name: "Weekly", PriceKES: 250, DurationMinutes: 10080, DownKbps: 5120, UpKbps: 2048, Active: true},
		})
	}
}

//good start for me
