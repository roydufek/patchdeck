package sshx

import (
	"errors"
	"fmt"
	"io"
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
