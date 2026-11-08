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

func TestBatchRestartScriptReexecFirst(t *testing.T) {
	// needrestart lists journald before the manager (alphabetical). The re-exec must still run
	// first and be waited on, or journald's restart hits PID 1 mid-re-exec (2026-11-07).
	s := batchRestartScript([]string{"systemd-journald.service", "systemd-manager", "cron.service"})
	reexec := strings.Index(s, "systemctl daemon-reexec")
	wait := strings.Index(s, waitPID1Cmd)
	journald := strings.Index(s, "systemctl restart systemd-journald.service")
	if reexec < 0 || wait < reexec || journald < wait {
		t.Errorf("want re-exec, then wait, then journald; got positions %d, %d, %d", reexec, wait, journald)
	}
	if strings.Contains(s, "systemctl restart systemd-manager") {
		t.Error("the manager must be re-exec'd, never restarted as a unit")
	}
	// Both detached units get a verified outcome line; the manager does not.
	if strings.Count(s, restartDoneMarker) != 4 { // 2 units × (done + pending) printf
		t.Errorf("expected 4 %s printfs, got %d", restartDoneMarker, strings.Count(s, restartDoneMarker))
	}
	if !strings.Contains(s, "/run/patchdeck-restart-systemd-journald_service-$$.rc") {
		t.Error("journald restart should record its outcome under /run")
	}
}

func TestParseRestartDone(t *testing.T) {
	out := restartBatchMarker + "\tcron.service\t0\t\n" +
		restartDoneMarker + "\tcron.service\t0\t\n" +
		restartDoneMarker + "\tsystemd-journald.service\t1\tFailed to restart systemd-journald.service: Transport endpoint is not connected \n" +
		restartDoneMarker + "\tslow.service\tpending\t\n"
	got := parseRestartDone(out)
	if len(got) != 2 {
		t.Fatalf("expected 2 finished results (pending skipped), got %d: %v", len(got), got)
	}
	if r := got["systemd-journald.service"]; r.rc != 1 || !strings.Contains(r.out, "Transport endpoint") {
		t.Errorf("journald parsed wrong: %+v", r)
	}
	if _, ok := got["slow.service"]; ok {
		t.Error("a pending restart must not count as finished")
	}
}

func TestManagersFirst(t *testing.T) {
	got := managersFirst([]string{"a.service", "systemd-manager", "b.service"})
	want := []string{"systemd-manager", "a.service", "b.service"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("managersFirst=%v want %v", got, want)
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
