// Package config tüm yapılandırmayı ortam değişkenlerinden okur.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config API servisinin yapılandırması.
type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	DBMaxConns      int32
	JWTSecret       []byte
	AccessTTL       time.Duration
	RefreshTTL      time.Duration
	HealthKey       []byte // AES-256 anahtarı (32 bayt)
	BarcodeSecret   []byte
	DemoPassword    string
	JobInterval     time.Duration
	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration
	LogLevel        string
	CORSOrigin      string
	AlertCritical   float64 // gün
	AlertLow        float64 // gün
}

// Load ortam değişkenlerini okur ve doğrular.
func Load() (Config, error) {
	c := Config{
		HTTPAddr:        env("HTTP_ADDR", ":8080"),
		DatabaseURL:     env("DATABASE_URL", "postgres://damar:damar@localhost:5432/damar?sslmode=disable"),
		DBMaxConns:      int32(envInt("DB_MAX_CONNS", 20)),
		AccessTTL:       envDur("JWT_ACCESS_TTL", 15*time.Minute),
		RefreshTTL:      envDur("JWT_REFRESH_TTL", 7*24*time.Hour),
		DemoPassword:    env("DEMO_PASSWORD", "Demo1234!"),
		JobInterval:     envDur("JOB_INTERVAL", 5*time.Second),
		RequestTimeout:  envDur("REQUEST_TIMEOUT", 10*time.Second),
		ShutdownTimeout: envDur("SHUTDOWN_TIMEOUT", 20*time.Second),
		LogLevel:        env("LOG_LEVEL", "info"),
		CORSOrigin:      env("CORS_ORIGIN", "http://localhost:3000"),
		AlertCritical:   envFloat("ALERT_CRITICAL_DAYS", 2),
		AlertLow:        envFloat("ALERT_LOW_DAYS", 4),
	}
	c.JWTSecret = []byte(os.Getenv("JWT_SECRET"))
	if len(c.JWTSecret) < 32 {
		return c, fmt.Errorf("JWT_SECRET en az 32 karakter olmalı")
	}
	var err error
	if c.HealthKey, err = Key32("HEALTH_ENC_KEY"); err != nil {
		return c, err
	}
	c.BarcodeSecret = []byte(os.Getenv("BARCODE_SECRET"))
	if len(c.BarcodeSecret) < 16 {
		return c, fmt.Errorf("BARCODE_SECRET en az 16 karakter olmalı")
	}
	return c, nil
}

// Key32 base64 kodlu 32 baytlık bir anahtar okur.
func Key32(name string) ([]byte, error) {
	k, err := base64.StdEncoding.DecodeString(os.Getenv(name))
	if err != nil || len(k) != 32 {
		return nil, fmt.Errorf("%s base64 kodlu 32 bayt olmalı (openssl rand -base64 32)", name)
	}
	return k, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil {
		return v
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(k)); err == nil {
		return v
	}
	return def
}
