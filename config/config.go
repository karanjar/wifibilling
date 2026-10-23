package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port          string
	DBPath        string
	JWTSecret     string
	GatewayAPIKey string
	AdminUser     string
	AdminPass     string

	MpesaEnv            string // "sandbox" or "production"
	MpesaConsumerKey    string
	MpesaConsumerSecret string
	MpesaShortcode      string
	MpesaPasskey        string
	MpesaCallbackBase   string // public https URL, e.g. https://wifi.example.com
	MpesaCallbackSecret string // random string embedded in callback path
}

func get(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func must(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing required env var %s", key)
	}
	return v
}

func Load() *Config {
	_ = godotenv.Load() // reads .env from the working directory, if it exists

	return &Config{
		Port:          get("PORT", "8080"),
		DBPath:        get("DB_PATH", "wifi.db"),
		JWTSecret:     must("JWT_SECRET"),
		GatewayAPIKey: must("GATEWAY_API_KEY"),
		AdminUser:     get("ADMIN_USER", "admin"),
		AdminPass:     must("ADMIN_PASS"),

		MpesaEnv:            get("MPESA_ENV", "sandbox"),
		MpesaConsumerKey:    get("MPESA_CONSUMER_KEY", ""),
		MpesaConsumerSecret: get("MPESA_CONSUMER_SECRET", ""),
		MpesaShortcode:      get("MPESA_SHORTCODE", "174379"),
		MpesaPasskey:        get("MPESA_PASSKEY", ""),
		MpesaCallbackBase:   get("MPESA_CALLBACK_BASE", ""),
		MpesaCallbackSecret: get("MPESA_CALLBACK_SECRET", "change-me"),
	}
}
