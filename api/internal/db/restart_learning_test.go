package db

import (
	"database/sql"
	"reflect"
	"testing"

	"patchdeck/api/internal/models"
)

// snapFor returns the stored snapshot for a host (fleets are small; filter the list).
func snapFor(t *testing.T, d *sql.DB, hostID string) models.ScanSnapshot {
	t.Helper()
	snaps, err := ListScanSnapshots(d)
	if err != nil {
		t.Fatalf("ListScanSnapshots: %v", err)
	}
	for _, s := range snaps {
		if s.HostID == hostID {
			return s
		}
	}
	t.Fatalf("no snapshot for host %s", hostID)
	return models.ScanSnapshot{}
}

func mkScan(needs []string, handlers map[string]bool, bootID string) models.ScanResult {
	return models.ScanResult{NeedsRestart: needs, RestartHandlers: handlers, BootID: bootID, NeedrestartFound: true}
}

func assertServices(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

// TestRestartLearningCycle drives the whole restart→reboot-only learning through the real scan
// path: classification, promotion of a restart-resistant unit on the same boot, self-heal on a
// reboot (boot_id change), and self-heal when the unit simply stops being flagged.
func TestRestartLearningCycle(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	h := mkHost(t, d, "learn")
	up := func(sr models.ScanResult) {
		t.Helper()
		if err := UpsertScanResult(d, h, sr); err != nil {
			t.Fatalf("UpsertScanResult: %v", err)
		}
	}

	// 1. Baseline (no marks): cron is restartable; dbus with no handler is reboot-only.
	up(mkScan([]string{"cron.service", "dbus.service"}, nil, "boot1"))
	s := snapFor(t, d, h)
	assertServices(t, "baseline restartable", s.RestartServices, []string{"cron.service"})
	assertServices(t, "baseline rebootOnly", s.RebootServices, []string{"dbus.service"})

	// 2. Learn: we restarted cron on boot1 (mark it); a rescan on the SAME boot still shows it
	//    flagged -> it's restart-resistant and must move to reboot-only.
	if err := SetRestartMark(d, h, "cron.service", "boot1"); err != nil {
		t.Fatalf("SetRestartMark: %v", err)
	}
	up(mkScan([]string{"cron.service"}, nil, "boot1"))
	s = snapFor(t, d, h)
	assertServices(t, "learned restartable", s.RestartServices, nil)
	assertServices(t, "learned rebootOnly", s.RebootServices, []string{"cron.service"})

	// 3. Reboot self-heals: a new boot_id clears the mark, so cron is restartable again.
	up(mkScan([]string{"cron.service"}, nil, "boot2"))
	s = snapFor(t, d, h)
	assertServices(t, "post-reboot restartable", s.RestartServices, []string{"cron.service"})
	assertServices(t, "post-reboot rebootOnly", s.RebootServices, nil)

	// 4. Drop-off self-heals: mark cron on boot2, then it stops being flagged (mark pruned); when
	//    it reappears on the same boot it is NOT stuck as reboot-only.
	if err := SetRestartMark(d, h, "cron.service", "boot2"); err != nil {
		t.Fatalf("SetRestartMark: %v", err)
	}
	up(mkScan(nil, nil, "boot2"))
	up(mkScan([]string{"cron.service"}, nil, "boot2"))
	s = snapFor(t, d, h)
	assertServices(t, "post-dropoff restartable", s.RestartServices, []string{"cron.service"})
	assertServices(t, "post-dropoff rebootOnly", s.RebootServices, nil)
}

// TestExcludeFromBulkRoundTrips confirms the protect flag persists and reads back on the host.
func TestExcludeFromBulkRoundTrips(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	h := mkHost(t, d, "prot")

	if err := SetHostExcludeFromBulk(d, h, true); err != nil {
		t.Fatalf("SetHostExcludeFromBulk: %v", err)
	}
	got, err := GetHost(d, h)
	if err != nil {
		t.Fatalf("GetHost: %v", err)
	}
	if !got.ExcludeFromBulk {
		t.Error("ExcludeFromBulk should be true after set")
	}
	if err := SetHostExcludeFromBulk(d, h, false); err != nil {
		t.Fatalf("SetHostExcludeFromBulk: %v", err)
	}
	got, _ = GetHost(d, h)
	if got.ExcludeFromBulk {
		t.Error("ExcludeFromBulk should be false after unset")
	}
}

// TestDeleteHostClearsRestartMarks confirms restart_marks is part of the host delete cascade.
func TestDeleteHostClearsRestartMarks(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	h := mkHost(t, d, "del")
	if err := SetRestartMark(d, h, "cron.service", "boot1"); err != nil {
		t.Fatalf("SetRestartMark: %v", err)
	}
	if err := DeleteHost(d, h); err != nil {
		t.Fatalf("DeleteHost: %v", err)
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM restart_marks WHERE host_id=?`, h).Scan(&n); err != nil {
		t.Fatalf("count restart_marks: %v", err)
	}
	if n != 0 {
		t.Errorf("restart_marks not cleared on host delete: %d row(s) remain", n)
	}
}

// TestGetScanSnapshot covers the single-host snapshot getter: absent before a scan, present with
// its buckets after, and ok=false for an unknown host.
func TestGetScanSnapshot(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	h := mkHost(t, d, "snap")
	if _, ok, err := GetScanSnapshot(d, h); err != nil || ok {
		t.Fatalf("expected no snapshot before scan (ok=%v err=%v)", ok, err)
	}
	if err := UpsertScanResult(d, h, mkScan([]string{"cron.service"}, nil, "boot1")); err != nil {
		t.Fatalf("UpsertScanResult: %v", err)
	}
	snap, ok, err := GetScanSnapshot(d, h)
	if err != nil || !ok {
		t.Fatalf("expected a snapshot after scan (ok=%v err=%v)", ok, err)
	}
	if snap.HostID != h {
		t.Errorf("wrong host id %q", snap.HostID)
	}
	if len(snap.RestartServices) != 1 || snap.RestartServices[0] != "cron.service" {
		t.Errorf("restart bucket not loaded: %v", snap.RestartServices)
	}
	if _, ok, _ := GetScanSnapshot(d, "does-not-exist"); ok {
		t.Error("expected ok=false for unknown host")
	}
}
