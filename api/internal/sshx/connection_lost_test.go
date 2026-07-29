package sshx

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestIsConnectionLost(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"exit-missing (reboot/sshd restart)", &ssh.ExitMissingError{}, true},
		{"io.EOF", io.EOF, true},
		{"wrapped io.EOF", fmt.Errorf("read tcp: %w", io.EOF), true},
		{"connection reset", errors.New("read tcp 10.0.0.2:22: connection reset by peer"), true},
		{"broken pipe", errors.New("write: broken pipe"), true},
		{"closed network", errors.New("use of closed network connection"), true},
		{"remote command exited without", errors.New("wait: remote command exited without exit status or exit signal"), true},
		// Genuine command failures / timeouts must NOT be classified as a connection loss.
		{"apt sub-process error", errors.New("E: Sub-process /usr/bin/dpkg returned an error code (1)"), false},
		{"exec timeout", errors.New("command timed out after 10m0s (host may have a held apt/dpkg lock or be unresponsive)"), false},
		{"dpkg progress line", errors.New("Setting up libc6:amd64 ..."), false},
		{"cancelled", errors.New("operation cancelled"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isConnectionLost(c.err); got != c.want {
				t.Fatalf("isConnectionLost(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestErrConnectionLostWrapsAndIsDetectable(t *testing.T) {
	wrapped := fmt.Errorf("%w: %v", ErrConnectionLost, errors.New("Setting up linux-image-amd64"))
	if !errors.Is(wrapped, ErrConnectionLost) {
		t.Fatalf("errors.Is should detect ErrConnectionLost in wrapped error")
	}
}

func TestIsSystemdManagerUnit(t *testing.T) {
	managers := []string{"systemd", "systemd.service", "systemd-manager", "systemd-manager.service", "init", "init.scope", "SYSTEMD", " systemd "}
	for _, m := range managers {
		if !isSystemdManagerUnit(m) {
			t.Errorf("isSystemdManagerUnit(%q) = false, want true", m)
		}
	}
	regular := []string{"systemd-logind.service", "systemd-journald.service", "dbus.service", "cron.service", "user@1000.service", "ssh.service"}
	for _, r := range regular {
		if isSystemdManagerUnit(r) {
			t.Errorf("isSystemdManagerUnit(%q) = true, want false (must restart normally)", r)
		}
	}
}

func TestIsUserManagerUnit(t *testing.T) {
	for _, s := range []string{"systemd-user", "systemd-user.service", "SYSTEMD-USER", " systemd-user "} {
		if !isUserManagerUnit(s) {
			t.Errorf("isUserManagerUnit(%q) = false, want true", s)
		}
	}
	// The real per-user instance and the sessions unit ARE genuine restartable units.
	for _, s := range []string{"user@1000.service", "systemd-user-sessions.service", "systemd-logind.service", "ssh.service"} {
		if isUserManagerUnit(s) {
			t.Errorf("isUserManagerUnit(%q) = true, want false", s)
		}
	}
}

func TestIsRiskyRestartUnit(t *testing.T) {
	// Restarting any of these live severs the session bus / logind and locks out SSH —
	// they must be routed to needrestart's coordinated handler or a reboot, never a naive restart.
	risky := []string{
		"dbus", "dbus.service", "dbus.socket",
		"dbus-broker", "dbus-broker.service",
		"systemd-logind", "systemd-logind.service",
		"DBUS.SERVICE", " systemd-logind ",
	}
	for _, s := range risky {
		if !isRiskyRestartUnit(s) {
			t.Errorf("isRiskyRestartUnit(%q) = false, want true (must not be restarted live)", s)
		}
	}
	// Normal units that MUST stay directly restartable — guard against over-blocking.
	safe := []string{
		"containerd.service", "ssh.service", "cron.service", "nginx.service",
		"systemd-journald.service", "systemd-user.service", "user@1000.service",
		"NetworkManager.service", "docker.service",
	}
	for _, s := range safe {
		if isRiskyRestartUnit(s) {
			t.Errorf("isRiskyRestartUnit(%q) = true, want false (over-blocking a normally-restartable unit)", s)
		}
	}
}

func TestRiskyRestartCmd(t *testing.T) {
	cmd := riskyRestartCmd("dbus.service")
	// Must invoke the host's needrestart coordinated handler for the unit...
	if !strings.Contains(cmd, "/etc/needrestart/restart.d/dbus.service") {
		t.Errorf("riskyRestartCmd missing needrestart handler path: %q", cmd)
	}
	// ...non-interactively, so the handler skips its prompt and dispatches via systemd-run...
	if !strings.Contains(cmd, "DEBIAN_FRONTEND=noninteractive") {
		t.Errorf("riskyRestartCmd not non-interactive: %q", cmd)
	}
	// ...and fall back to the absent-marker (NOT a destructive restart) when the host has no
	// handler, so the caller marks the unit reboot-required.
	if !strings.Contains(cmd, needrestartHandlerAbsentMarker) {
		t.Errorf("riskyRestartCmd missing absent-marker fallback: %q", cmd)
	}
	if strings.Contains(cmd, "systemctl restart") {
		t.Errorf("riskyRestartCmd must NOT run a naive `systemctl restart`: %q", cmd)
	}
}

func TestIsUnitNotFound(t *testing.T) {
	yes := []error{
		errors.New("Failed to restart systemd-user.service: Unit systemd-user.service not found."),
		errors.New("Unit foo.service not loaded."),
		errors.New("No such file or directory"),
	}
	for _, e := range yes {
		if !isUnitNotFound(e) {
			t.Errorf("isUnitNotFound(%v) = false, want true", e)
		}
	}
	no := []error{
		nil,
		errors.New("Job for nginx.service failed because the control process exited"),
		errors.New("Interactive authentication required"),
	}
	for _, e := range no {
		if isUnitNotFound(e) {
			t.Errorf("isUnitNotFound(%v) = true, want false", e)
		}
	}
}

func TestStripAuthPromptNoise(t *testing.T) {
	cases := map[string]string{
		"Password: Failed to restart systemd-user.service: Unit systemd-user.service not found.": "Failed to restart systemd-user.service: Unit systemd-user.service not found.",
		"Password: Password: boom": "boom",
		"no prompt here":           "no prompt here",
		"  Password: trimmed  ":    "trimmed",
	}
	for in, want := range cases {
		if got := stripAuthPromptNoise(in); got != want {
			t.Errorf("stripAuthPromptNoise(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestApplyInterruptedError(t *testing.T) {
	underlying := fmt.Errorf("%w: boom", ErrConnectionLost)
	ai := &ApplyInterruptedError{Underlying: underlying, ChangedSoFar: 3, DpkgStarted: true}

	var target *ApplyInterruptedError
	if !errors.As(error(ai), &target) {
		t.Fatalf("errors.As should match *ApplyInterruptedError")
	}
	if target.ChangedSoFar != 3 || !target.DpkgStarted {
		t.Fatalf("fields not preserved: %+v", target)
	}
	if !errors.Is(ai, ErrConnectionLost) {
		t.Fatalf("ApplyInterruptedError should unwrap to ErrConnectionLost")
	}
}
