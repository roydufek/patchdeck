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
	SSHTimeout          time.Duration
	ExecTimeout         time.Duration
	ConnectivityTimeout time.Duration
	AppriseURL          string
	AppriseTimeout      time.Duration
	TLSEnabled          bool
	TLSCertPath         string
	TLSKeyPath          string
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
	// ExecTimeout caps how long a single remote command may run (scan/apply/etc.),
	// separate from SSHTimeout which only covers the TCP/handshake dial. Without it a
	// host with a held apt/dpkg lock would hang an operation indefinitely.
	execTimeout := 600 * time.Second
	if raw := os.Getenv("PATCHDECK_EXEC_TIMEOUT_SECONDS"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			execTimeout = time.Duration(v) * time.Second
		}
	}
	// ConnectivityTimeout bounds the live "quick" SSH reachability check shown on the
	// dashboard. The old hard-coded 5s falsely reported slow-but-reachable hosts (e.g.
	// over a tailnet) as Disconnected even when full scans succeeded; default 15s.
	connectivityTimeout := 15 * time.Second
	if raw := os.Getenv("PATCHDECK_CONNECTIVITY_TIMEOUT_SECONDS"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			connectivityTimeout = time.Duration(v) * time.Second
		}
	}
	appriseTimeout := 10 * time.Second
	if raw := os.Getenv("PATCHDECK_APPRISE_TIMEOUT_SECONDS"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			appriseTimeout = time.Duration(v) * time.Second
		}
	}
	tlsEnabled := true
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("PATCHDECK_TLS"))); v == "false" || v == "0" {
		tlsEnabled = false
	}
	cfg := Config{
		AppVersion:          envOr("PATCHDECK_VERSION", "1.8.5"),
		Port:                port,
		DatabasePath:        envOr("PATCHDECK_DB_PATH", "/data/patchdeck.db"),
		MasterKey:           os.Getenv("PATCHDECK_MASTER_KEY"),
		SSHTimeout:          t,
		ExecTimeout:         execTimeout,
		ConnectivityTimeout: connectivityTimeout,
		AppriseURL:          os.Getenv("PATCHDECK_APPRISE_URL"),
		AppriseTimeout:      appriseTimeout,
		TLSEnabled:          tlsEnabled,
		TLSCertPath:         envOr("PATCHDECK_TLS_CERT", "/data/tls/cert.pem"),
		TLSKeyPath:          envOr("PATCHDECK_TLS_KEY", "/data/tls/key.pem"),
	}
	if len(cfg.MasterKey) < 32 {
		return Config{}, errors.New("PATCHDECK_MASTER_KEY must be set to 32+ characters")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
