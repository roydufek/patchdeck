package restartpolicy

import (
	"reflect"
	"testing"
)

func TestPredicates(t *testing.T) {
	cases := []struct {
		svc                   string
		dbus, risky, disrupt  bool
	}{
		{"dbus.service", true, true, true},
		{"dbus-broker.service", true, true, true},
		{"systemd-logind.service", false, true, true},
		{"containerd.service", false, false, true},
		{"tailscaled", false, false, true},
		{"cron.service", false, false, false},
		{"rsyslog.service", false, false, false},
	}
	for _, c := range cases {
		if got := IsDbusFamily(c.svc); got != c.dbus {
			t.Errorf("IsDbusFamily(%q)=%v want %v", c.svc, got, c.dbus)
		}
		if got := IsRisky(c.svc); got != c.risky {
			t.Errorf("IsRisky(%q)=%v want %v", c.svc, got, c.risky)
		}
		if got := IsDisruptive(c.svc); got != c.disrupt {
			t.Errorf("IsDisruptive(%q)=%v want %v", c.svc, got, c.disrupt)
		}
	}
}

func TestClassify(t *testing.T) {
	needs := []string{"cron.service", "dbus.service", "containerd.service", "systemd-logind.service"}
	// dbus has no handler -> reboot-only; containerd is learned-resistant -> reboot-only;
	// cron and logind are restartable (logind is risky but a smart restart handles it detached).
	handlers := map[string]bool{"systemd-logind.service": true}
	resistant := map[string]bool{"containerd.service": true}

	restartable, rebootOnly := Classify(needs, handlers, resistant)

	wantRestart := []string{"cron.service", "systemd-logind.service"}
	wantReboot := []string{"dbus.service", "containerd.service"}
	if !reflect.DeepEqual(restartable, wantRestart) {
		t.Errorf("restartable=%v want %v", restartable, wantRestart)
	}
	if !reflect.DeepEqual(rebootOnly, wantReboot) {
		t.Errorf("rebootOnly=%v want %v", rebootOnly, wantReboot)
	}
}

func TestKnownRebootOnly(t *testing.T) {
	if !IsKnownRebootOnly("containerd.service") || !IsKnownRebootOnly("containerd") {
		t.Error("containerd should be known reboot-only")
	}
	// Keep the seed tight — these are restartable/disruptive, NOT statically reboot-only.
	for _, s := range []string{"docker.service", "cron.service", "dbus.service", "tailscaled"} {
		if IsKnownRebootOnly(s) {
			t.Errorf("%s should NOT be in the known-reboot-only seed", s)
		}
	}
	// containerd with no handler and no learned mark -> reboot-only via the static seed (spares the
	// operator a known-futile restart); cron stays restartable.
	restartable, rebootOnly := Classify([]string{"cron.service", "containerd.service"}, nil, nil)
	if !reflect.DeepEqual(rebootOnly, []string{"containerd.service"}) {
		t.Errorf("containerd (no handler) should be reboot-only, got reboot=%v restart=%v", rebootOnly, restartable)
	}
	if !reflect.DeepEqual(restartable, []string{"cron.service"}) {
		t.Errorf("cron should be restartable, got %v", restartable)
	}
	// Escape hatch: a host with a coordinated needrestart handler for containerd can restart it.
	r2, rb2 := Classify([]string{"containerd.service"}, map[string]bool{"containerd.service": true}, nil)
	if len(rb2) != 0 || !reflect.DeepEqual(r2, []string{"containerd.service"}) {
		t.Errorf("containerd with handler should be restartable, got reboot=%v restart=%v", rb2, r2)
	}
}

func TestClassifyDbusWithHandlerIsRestartable(t *testing.T) {
	// With a coordinated handler present, dbus is restartable (needrestart owns the bus restart).
	restartable, rebootOnly := Classify([]string{"dbus.service"}, map[string]bool{"dbus.service": true}, nil)
	if len(rebootOnly) != 0 {
		t.Errorf("dbus with handler should not be reboot-only, got %v", rebootOnly)
	}
	if !reflect.DeepEqual(restartable, []string{"dbus.service"}) {
		t.Errorf("dbus with handler should be restartable, got %v", restartable)
	}
}
