package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppVersion          string
	Port                int
	DatabasePath        string
	MasterKey           string
	JWTSecret           string
	SSHTimeout          time.Duration
	AppriseURL          string
	AppriseBinPath      string
	AppriseTimeout      time.Duration
	RegistrationEnabled bool // REGISTRATION_ENABLED env var; default true. Set to "false" to block bootstrap/register.
}

func Load() (Config, error) {
	port := 6070
	if p := os.Getenv("PATCHDECK_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}
	t := 20 * time.Second
	if raw := os.Getenv("PATCHDECK_SSH_TIMEOUT_SECONDS"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			t = time.Duration(v) * time.Second
		}
	}
	appriseTimeout := 10 * time.Second
	if raw := os.Getenv("PATCHDECK_APPRISE_TIMEOUT_SECONDS"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			appriseTimeout = time.Duration(v) * time.Second
		}
	}
	registrationEnabled := true
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("REGISTRATION_ENABLED"))); v == "false" || v == "0" {
		registrationEnabled = false
	}
	cfg := Config{
		AppVersion:          envOr("PATCHDECK_VERSION", "0.1.0-alpha"),
		Port:                port,
		DatabasePath:        envOr("PATCHDECK_DB_PATH", "./data/patchdeck.db"),
		MasterKey:           os.Getenv("PATCHDECK_MASTER_KEY"),
		JWTSecret:           os.Getenv("PATCHDECK_JWT_SECRET"),
		SSHTimeout:          t,
		AppriseURL:          os.Getenv("PATCHDECK_APPRISE_URL"),
		AppriseBinPath:      envOr("PATCHDECK_APPRISE_BIN", "apprise"),
		AppriseTimeout:      appriseTimeout,
		RegistrationEnabled: registrationEnabled,
	}
	if len(cfg.MasterKey) < 32 || len(cfg.JWTSecret) < 32 {
		return Config{}, errors.New("PATCHDECK_MASTER_KEY and PATCHDECK_JWT_SECRET must be set to 32+ characters")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
