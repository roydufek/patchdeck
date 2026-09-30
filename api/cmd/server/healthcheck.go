package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// runHealthcheck backs `patchdeck healthcheck`, the image's baked-in HEALTHCHECK. It probes this
// instance's own /healthz over loopback in whichever mode it serves — plain HTTP, or HTTPS with the
// auto-generated self-signed cert — so the runtime image needs no curl/wget, and a generic
// http/wget probe can't false-fail on the self-signed cert. It deliberately runs BEFORE
// config.Load (no DB open, no master key needed) and mirrors config's PATCHDECK_PORT /
// PATCHDECK_TLS parsing exactly, so it always dials what the server actually bound.
func runHealthcheck() int {
	port := "6070"
	if p := os.Getenv("PATCHDECK_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = strconv.Itoa(v)
		}
	}
	scheme := "https"
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("PATCHDECK_TLS"))); v == "false" || v == "0" {
		scheme = "http"
	}
	client := &http.Client{
		Timeout: 4 * time.Second,
		// Loopback self-probe of our own self-signed cert — there's no remote identity to verify.
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec
	}
	resp, err := client.Get(scheme + "://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: /healthz returned", resp.StatusCode)
		return 1
	}
	return 0
}
