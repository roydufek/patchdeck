package scheduler

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"patchdeck/api/internal/db"
	"patchdeck/api/internal/models"
	"patchdeck/api/internal/notify"

	_ "modernc.org/sqlite"
)

// captureGotify is a fake Gotify that records every message title it receives.
func captureGotify(t *testing.T) (target string, titles func() []string) {
	t.Helper()
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &m)
		mu.Lock()
		got = append(got, m["title"].(string)+"|"+m["message"].(string))
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return "gotify://" + strings.TrimPrefix(srv.URL, "http://") + "/tok", func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

func digestEngine(t *testing.T, s models.NotificationSettings) *Engine {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertNotificationSettings(conn, s); err != nil {
		t.Fatal(err)
	}
	return NewEngine(conn, nil, nil, notify.NewDispatcher(2*time.Second), "")
}

func pkgs(names ...string) []models.PackageInfo {
	var out []models.PackageInfo
	for _, n := range names {
		out = append(out, models.PackageInfo{Name: n, NewVersion: "2", Source: "bookworm-security"})
	}
	return out
}

// simulateNightly feeds one run's worth of host outcomes through the same note* paths the
// real scan/apply functions use (SSH stubbed out), then ends the run.
func simulateNightly(e *Engine) {
	n := e.newRunNotes(models.Job{Name: "Nightly scan"})
	e.notePending(n, models.Host{ID: "h1", Name: "nova"}, nil, models.ScanResult{Packages: pkgs("a", "b"), NeedsReboot: true})
	e.notePending(n, models.Host{ID: "h2", Name: "mira"}, nil, models.ScanResult{Packages: pkgs("c")})
	e.notePending(n, models.Host{ID: "h3", Name: "arc"}, nil, models.ScanResult{})
	e.noteScanFailure(n, models.Host{ID: "h4", Name: "orbit"}, "Patchdeck scheduled scan FAILED: orbit (boom)", "boom")
	e.sendDigest(n)
}

func TestScheduledRunSendsOneDigest(t *testing.T) {
	target, titles := captureGotify(t)
	e := digestEngine(t, models.NotificationSettings{AppriseURL: target, UpdatesAvailable: true, ScanFailure: true,
		AutoApplySuccess: true, AutoApplyFailure: true, Delivery: models.DeliveryDigest})
	simulateNightly(e)
	got := titles()
	if len(got) != 1 {
		t.Fatalf("want exactly one message, got %d: %q", len(got), got)
	}
	if !strings.HasPrefix(got[0], "2 of 4 hosts need updates (3 packages, 3 security) · 1 needs a reboot · 1 failed|") ||
		!strings.Contains(got[0], "- orbit: scan failed (boom)") || !strings.Contains(got[0], "Up to date: 1 host") {
		t.Errorf("digest:\n%s", got[0])
	}
}

func TestScheduledRunPerHostDelivery(t *testing.T) {
	target, titles := captureGotify(t)
	e := digestEngine(t, models.NotificationSettings{AppriseURL: target, UpdatesAvailable: true, ScanFailure: true,
		AutoApplySuccess: true, AutoApplyFailure: true, Delivery: models.DeliveryPerHost})
	simulateNightly(e)
	if got := titles(); len(got) != 3 { // nova + mira (new updates) + orbit (failure); arc is up to date
		t.Fatalf("per-host delivery: want 3 messages, got %d: %q", len(got), got)
	}
}

func TestScheduledRunQuietWhenNothingNew(t *testing.T) {
	target, titles := captureGotify(t)
	for _, always := range []bool{false, true} {
		e := digestEngine(t, models.NotificationSettings{AppriseURL: target, UpdatesAvailable: true, ScanFailure: true,
			AutoApplySuccess: true, AutoApplyFailure: true, Delivery: models.DeliveryDigest, DigestAlways: always})
		n := e.newRunNotes(models.Job{Name: "Nightly scan"})
		known := pkgs("a")
		e.notePending(n, models.Host{ID: "h1", Name: "nova"}, known, models.ScanResult{Packages: known}) // same as last scan
		e.sendDigest(n)
	}
	got := titles()
	if len(got) != 1 || !strings.HasPrefix(got[0], "1 of 1 hosts need updates") {
		t.Fatalf("nothing new: silent by default, one message with digest_always; got %q", got)
	}
}

func TestDigestHonoursToggles(t *testing.T) {
	target, titles := captureGotify(t)
	e := digestEngine(t, models.NotificationSettings{AppriseURL: target, UpdatesAvailable: false, ScanFailure: true,
		AutoApplySuccess: true, AutoApplyFailure: true, Delivery: models.DeliveryDigest})
	simulateNightly(e)
	got := titles()
	if len(got) != 1 || strings.Contains(got[0], "nova") || !strings.Contains(got[0], "orbit") {
		t.Fatalf("updates_available off: only the failure should be reported; got %q", got)
	}
}
