package sshx

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitRestartBatch(t *testing.T) {
	batchable, special := splitRestartBatch([]string{"cron.service", "dbus.service", "rsyslog.service", "systemd-logind.service", "containerd.service"})
	// dbus + logind are risky (coordinated-handler only) -> special; the rest batch.
	wantBatch := []string{"cron.service", "rsyslog.service", "containerd.service"}
	wantSpecial := []string{"dbus.service", "systemd-logind.service"}
	if !reflect.DeepEqual(batchable, wantBatch) {
		t.Errorf("batchable=%v want %v", batchable, wantBatch)
	}
	if !reflect.DeepEqual(special, wantSpecial) {
		t.Errorf("special=%v want %v", special, wantSpecial)
	}
}

func TestBatchRestartScript(t *testing.T) {
	s := batchRestartScript([]string{"cron.service", "rsyslog.service"})
	// One escalation, N units: each unit emits a parseable marker line, and a normal unit uses the
	// detached systemd-run restart. The whole thing is a single script (one su).
	if !strings.Contains(s, "set +e") {
		t.Error("script should not abort on first failure (set +e)")
	}
	for _, svc := range []string{"cron.service", "rsyslog.service"} {
		if !strings.Contains(s, "'"+svc+"'") {
			t.Errorf("script missing single-quoted svc arg for %q", svc)
		}
	}
	if strings.Count(s, restartBatchMarker) != 2 {
		t.Errorf("expected 2 %s markers, got %d", restartBatchMarker, strings.Count(s, restartBatchMarker))
	}
	if !strings.Contains(s, "systemd-run") {
		t.Error("normal unit should restart via detached systemd-run")
	}
}

func TestParseRestartBatch(t *testing.T) {
	// Simulated output: cron ok (rc 0), apcupsd failed escalation (rc 1 with reason), an unrelated line.
	out := "some preamble\n" +
		restartBatchMarker + "\tcron.service\t0\t\n" +
		"noise line\n" +
		restartBatchMarker + "\tapcupsd.service\t1\tPassword: su: Authentication failure\n"
	got := parseRestartBatch(out)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(got), got)
	}
	if r := got["cron.service"]; r.rc != 0 {
		t.Errorf("cron rc=%d want 0", r.rc)
	}
	if r := got["apcupsd.service"]; r.rc != 1 || !strings.Contains(r.out, "Authentication failure") {
		t.Errorf("apcupsd parsed wrong: %+v", r)
	}
}

func TestIsUnitNotFoundText(t *testing.T) {
	if !isUnitNotFoundText("Unit foo.service not found.") || !isUnitNotFoundText("could not be found: No such file") {
		t.Error("should detect not-found text")
	}
	if isUnitNotFoundText("Job for cron.service failed") {
		t.Error("should not flag a generic failure as not-found")
	}
}
