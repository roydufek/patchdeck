package sshx

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func fastApplyRetries(t *testing.T) {
	t.Helper()
	d, w := applyRetryDelay, applyReconnectWindow
	applyRetryDelay, applyReconnectWindow = 20*time.Millisecond, 2*time.Second
	t.Cleanup(func() { applyRetryDelay, applyReconnectWindow = d, w })
}

type lines struct {
	mu sync.Mutex
	l  []string
}

func (s *lines) add(l string) { s.mu.Lock(); s.l = append(s.l, l); s.mu.Unlock() }
func (s *lines) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.l...)
}

// The start script must detach apt from the SSH session and refresh lists before upgrading.
func TestApplyStartScriptShape(t *testing.T) {
	s := applyStartScript("20261103T051200Z-abcd1234")
	for _, want := range []string{"nohup setsid sh -c", "< /dev/null &", "dpkg --configure -a", "apt-get update", "--force-confdef", "--force-confold", "dist-upgrade", `"$0.rc"`, `tail -n +1 --pid="$P" -f`} {
		if !strings.Contains(s, want) {
			t.Errorf("start script lacks %q", want)
		}
	}
	if f := applyFollowScript("x", 42); !strings.Contains(f, "tail -n +42 ") || strings.Contains(f, "setsid") {
		t.Errorf("follow script must resume at line 42 and never restart the upgrade:\n%s", f)
	}
}

func TestApplyDetachedHappyPath(t *testing.T) {
	key := newSigner(t)
	f := newFakeSSH(t, key)
	f.handler = func(cmd, _ string) (string, uint32) {
		if cmd == escalationProbe {
			return "__PD_ROOT__\n", 0
		}
		return "Get:1 http://deb a\nSetting up a (2)\nSetting up b (2)\n__PD_APPLY_RC__=0\n__PD_SETUP_COUNT__=2\n__REBOOT__\n", 0
	}
	host, seal := appKeyHost(t, f, "")
	var got lines
	res, err := testClient(key).applyDetached(context.Background(), host, seal, got.add)
	if err != nil || res.ChangedPackages != 2 || !res.NeedsReboot {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if l := got.all(); len(l) != 3 || l[0] != "Get:1 http://deb a" {
		t.Fatalf("streamed lines (markers must be hidden): %q", l)
	}
}

// The heart of it: the connection dies mid-apply; Patchdeck reattaches, resumes at the next
// unseen line (no duplicates, nothing lost) and records the host's real result.
func TestApplyDetachedReattachesAfterDrop(t *testing.T) {
	fastApplyRetries(t)
	key := newSigner(t)
	f := newFakeSSH(t, key)
	var fmu sync.Mutex
	var follows []string
	f.handler = func(cmd, _ string) (string, uint32) {
		switch {
		case cmd == escalationProbe:
			return "__PD_ROOT__\n", 0
		case strings.Contains(cmd, "setsid"): // the start: some output, then the path dies
			return "Get:1 http://deb tailscale\nSetting up tailscale (1.104.5)\n", dropConn
		default: // a reattach
			fmu.Lock()
			follows = append(follows, cmd)
			fmu.Unlock()
			return "Setting up linux-image-amd64 (6.12.115-2)\n__PD_APPLY_RC__=0\n__PD_SETUP_COUNT__=2\n", 0
		}
	}
	host, seal := appKeyHost(t, f, "")
	var got lines
	res, err := testClient(key).applyDetached(context.Background(), host, seal, got.add)
	if err != nil || res.ChangedPackages != 2 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	fmu.Lock()
	defer fmu.Unlock()
	if len(follows) != 1 || !strings.Contains(follows[0], "tail -n +3 ") {
		t.Fatalf("reattach should resume at line 3: %q", follows)
	}
	l := got.all()
	want := []string{"Get:1 http://deb tailscale", "Setting up tailscale (1.104.5)", "", "Setting up linux-image-amd64 (6.12.115-2)"}
	if len(l) != 4 || l[0] != want[0] || l[1] != want[1] || !strings.Contains(l[2], "Reattaching") || l[3] != want[3] {
		t.Fatalf("stream: %q", l)
	}
	if f.conns.Load() < 2 {
		t.Fatalf("expected a fresh connection for the reattach, got %d", f.conns.Load())
	}
}

func TestApplyDetachedFailureExitCode(t *testing.T) {
	key := newSigner(t)
	f := newFakeSSH(t, key)
	f.handler = func(cmd, _ string) (string, uint32) {
		if cmd == escalationProbe {
			return "__PD_ROOT__\n", 0
		}
		return "E: Could not get lock /var/lib/dpkg/lock-frontend\n__PD_APPLY_RC__=100\n__PD_SETUP_COUNT__=0\n", 0
	}
	host, seal := appKeyHost(t, f, "")
	_, err := testClient(key).applyDetached(context.Background(), host, seal, nil)
	if err == nil || !strings.Contains(err.Error(), "exit 100") || !strings.Contains(err.Error(), "Could not get lock") {
		t.Fatalf("want apt's failure, got %v", err)
	}
}

// Stopped without an exit status (host rebooted mid-run) → interrupted, so callers verify by rescanning.
func TestApplyDetachedLost(t *testing.T) {
	key := newSigner(t)
	f := newFakeSSH(t, key)
	f.handler = func(cmd, _ string) (string, uint32) {
		if cmd == escalationProbe {
			return "__PD_ROOT__\n", 0
		}
		return "Setting up a (2)\n__PD_APPLY_LOST__\n", 0
	}
	host, seal := appKeyHost(t, f, "")
	_, err := testClient(key).applyDetached(context.Background(), host, seal, nil)
	var ie *ApplyInterruptedError
	if !errors.As(err, &ie) || ie.ChangedSoFar != 1 {
		t.Fatalf("want ApplyInterruptedError with 1 changed, got %v", err)
	}
}

// A failure before anything started (bad auth) is reported at once — no reattach loop.
func TestApplyDetachedPreStartFailureIsImmediate(t *testing.T) {
	fastApplyRetries(t)
	f := newFakeSSH(t) // trusts no key
	host, seal := appKeyHost(t, f, "")
	t0 := time.Now()
	_, err := testClient(newSigner(t)).applyDetached(context.Background(), host, seal, nil)
	if err == nil || !strings.Contains(err.Error(), "authentication failed") || time.Since(t0) > time.Second {
		t.Fatalf("want an immediate auth error, got %v after %v", err, time.Since(t0))
	}
}

// If the host stays unreachable past the reconnect window, give up as interrupted (verify later).
func TestApplyDetachedGivesUpAfterWindow(t *testing.T) {
	fastApplyRetries(t)
	applyReconnectWindow = 300 * time.Millisecond
	key := newSigner(t)
	f := newFakeSSH(t, key)
	f.handler = func(cmd, _ string) (string, uint32) {
		if cmd == escalationProbe {
			return "__PD_ROOT__\n", 0
		}
		if strings.Contains(cmd, "setsid") {
			return "Setting up a (2)\n", dropConn
		}
		return "", dropConn // every reattach dies too
	}
	host, seal := appKeyHost(t, f, "")
	_, err := testClient(key).applyDetached(context.Background(), host, seal, nil)
	var ie *ApplyInterruptedError
	if !errors.As(err, &ie) || !strings.Contains(err.Error(), "couldn't reattach") {
		t.Fatalf("want interrupted after the window, got %v", err)
	}
}
