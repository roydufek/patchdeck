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
	// BulkConcurrency caps how many hosts a fleet-wide action (scan-all / apply-all /
	// reboot-all) drives at once, so a large fleet doesn't open hundreds of SSH sessions in
	// one burst. The fan-out is client-driven; this value is handed to the dashboard JS.
	BulkConcurrency int
	// ApplyStaggerSeconds inserts a minimum gap between successive apply-all starts, to avoid
	// bouncing services across the whole fleet simultaneously. 0 = no stagger (start as soon
	// as a concurrency slot frees).
	ApplyStaggerSeconds int
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
	// Bulk fan-out caps. Default concurrency 4 keeps a burst modest while staying brisk on a
	// homelab; 0/blank falls back to the default, a value <1 means "no cap".
	bulkConcurrency := 4
	if raw := os.Getenv("PATCHDECK_BULK_CONCURRENCY"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			bulkConcurrency = v
		}
	}
	applyStagger := 0
	if raw := os.Getenv("PATCHDECK_APPLY_STAGGER_SECONDS"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			applyStagger = v
		}
	}
	cfg := Config{
		AppVersion:          envOr("PATCHDECK_VERSION", "2.5.2"),
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
		BulkConcurrency:     bulkConcurrency,
		ApplyStaggerSeconds: applyStagger,
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
