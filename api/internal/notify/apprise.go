package notify

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Dispatcher struct {
	binPath string
	timeout time.Duration
}

type RuntimeInfo struct {
	Available bool   `json:"available"`
	BinPath   string `json:"bin_path"`
	Version   string `json:"version"`
	Error     string `json:"error,omitempty"`
}

func NewDispatcher(binPath string, timeout time.Duration) *Dispatcher {
	binPath = strings.TrimSpace(binPath)
	if binPath == "" {
		binPath = "apprise"
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Dispatcher{binPath: binPath, timeout: timeout}
}

func (d *Dispatcher) RuntimeInfo() RuntimeInfo {
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, d.binPath, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(bytes.TrimSpace(out)))
		msg := strings.TrimSpace(err.Error())
		if trimmed != "" {
			msg = msg + ": " + trimmed
		}
		return RuntimeInfo{Available: false, BinPath: d.binPath, Error: msg}
	}
	return RuntimeInfo{Available: true, BinPath: d.binPath, Version: strings.TrimSpace(string(out))}
}

func (d *Dispatcher) Send(url, body string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, d.binPath, "-t", "Patchdeck", "-b", body, url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(bytes.TrimSpace(out)))
		if trimmed == "" {
			return fmt.Errorf("apprise notify failed: %w", err)
		}
		return fmt.Errorf("apprise notify failed: %w: %s", err, trimmed)
	}
	return nil
}
