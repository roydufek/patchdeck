package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func nightly() Digest {
	d := Digest{Scheduled: true, Job: "Nightly scan"}
	d.Pending = []PendingHost{
		{Name: "arc", Packages: 2, Restarts: 1},
		{Name: "nova", Packages: 14, Security: 3, New: true, Reboot: true},
		{Name: "mira", Packages: 5, Security: 1, New: true},
	}
	d.Failed = []FailedHost{{Name: "orbit", Phase: "scan", Err: "dial tcp: i/o timeout\nmore detail"}}
	d.UpToDate = 7
	return d
}

func TestDigestScheduledScan(t *testing.T) {
	d := nightly()
	if got, want := d.Title(), "Patchdeck: 3 of 11 hosts need updates (21 packages, 4 security) · 1 needs a reboot · 1 failed"; got != want {
		t.Errorf("title\n got %q\nwant %q", got, want)
	}
	want := strings.Join([]string{
		"Failed:",
		"- orbit: scan failed (dial tcp: i/o timeout)",
		"Needs updates:",
		"- nova: 14 updates (3 security) · reboot needed · new",
		"- mira: 5 updates (1 security) · new",
		"- arc: 2 updates · 1 service to restart",
		"Up to date: 7 hosts",
		"Schedule: Nightly scan",
	}, "\n")
	if got := d.Body(); got != want {
		t.Errorf("body\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDigestNotable(t *testing.T) {
	quiet := Digest{Scheduled: true, Pending: []PendingHost{{Name: "arc", Packages: 2}}, UpToDate: 3}
	if quiet.Notable() {
		t.Error("known, unchanged pending updates must not be notable")
	}
	if !nightly().Notable() {
		t.Error("new updates must be notable")
	}
	if f := (Digest{Failed: []FailedHost{{Name: "x", Phase: "scan"}}}); !f.Notable() {
		t.Error("a failure must be notable")
	}
	if (&Digest{}).Empty() != true || nightly().Empty() {
		t.Error("Empty")
	}
}

func TestDigestAllUpToDate(t *testing.T) {
	d := Digest{Scheduled: true, UpToDate: 12}
	if got := d.Title(); got != "Patchdeck: all 12 hosts up to date" {
		t.Errorf("title %q", got)
	}
	if got := (&Digest{Scheduled: true, UpToDate: 1}).Title(); got != "Patchdeck: 1 host up to date" {
		t.Errorf("title %q", got)
	}
}

func TestDigestApplied(t *testing.T) {
	d := Digest{Pending: []PendingHost{{Name: "nova", Packages: 14, New: true}, {Name: "mira", Packages: 3, New: true}}}
	d.AddApplied(AppliedHost{Name: "nova", Changed: 14, Reboot: true})
	d.AddApplied(AppliedHost{Name: "atlas", Changed: 0}) // nothing to apply → up to date, not listed
	d.AddApplied(AppliedHost{Name: "cosmos", Changed: 2, Interrupted: true})
	if len(d.Pending) != 1 || d.Pending[0].Name != "mira" {
		t.Fatalf("applied host must leave Pending: %+v", d.Pending)
	}
	if d.UpToDate != 1 || len(d.Applied) != 2 {
		t.Fatalf("applied=%d uptodate=%d", len(d.Applied), d.UpToDate)
	}
	if got, want := d.Title(), "Patchdeck: mira needs updates (3 packages) · updated 2 hosts (16 packages) · 1 needs a reboot"; got != want {
		t.Errorf("title\n got %q\nwant %q", got, want)
	}
	body := d.Body()
	for _, s := range []string{
		"Updated:\n- nova: 14 packages · reboot needed\n- cosmos: 2 packages · connection lost mid-apply (likely restarting)",
		"- mira: 3 updates\n", // dashboard batches don't mark "new" (everything in them is)
	} {
		if !strings.Contains(body, s) {
			t.Errorf("body lacks %q:\n%s", s, body)
		}
	}
}

func TestDigestSingleHostTitles(t *testing.T) {
	d := Digest{Pending: []PendingHost{{Name: "nova", Packages: 1, New: true}}}
	if got := d.Title(); got != "Patchdeck: nova needs updates (1 package)" {
		t.Errorf("title %q", got)
	}
	d = Digest{Applied: []AppliedHost{{Name: "nova", Changed: 4}}}
	if got := d.Title(); got != "Patchdeck: updated nova (4 packages)" {
		t.Errorf("title %q", got)
	}
}

func TestDigestBodyCapped(t *testing.T) {
	var d Digest
	for i := 0; i < 400; i++ {
		d.Pending = append(d.Pending, PendingHost{Name: strings.Repeat("h", 20) + string(rune('a'+i%26)), Packages: i + 1, New: true})
	}
	b := d.Body()
	if len(b) > maxBody+40 || !strings.Contains(b, "more lines") {
		t.Errorf("body not capped: %d bytes", len(b))
	}
}

func TestShortErr(t *testing.T) {
	if got := shortErr(strings.Repeat("x", 300)); len([]rune(got)) != 140 {
		t.Errorf("len %d", len([]rune(got)))
	}
	if got := shortErr(" a\nb "); got != "a" {
		t.Errorf("%q", got)
	}
}

func TestCoalescerGroupsABurst(t *testing.T) {
	out := make(chan Digest, 4)
	c := NewCoalescer(400*time.Millisecond, 5*time.Second, func(d Digest) { out <- d })
	for _, h := range []string{"a", "b", "c"} {
		h := h
		c.Add(func(d *Digest) { d.AddApplied(AppliedHost{Name: h, Changed: 1}) })
		time.Sleep(10 * time.Millisecond) // each event lands well inside the quiet window
	}
	select {
	case d := <-out:
		if len(d.Applied) != 3 {
			t.Fatalf("want one batch of 3, got %+v", d.Applied)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("batch never flushed")
	}
	select {
	case d := <-out:
		t.Fatalf("unexpected second batch %+v", d)
	case <-time.After(600 * time.Millisecond):
	}
}

func TestCoalescerMaxHold(t *testing.T) {
	var mu sync.Mutex
	var batches []int
	c := NewCoalescer(100*time.Millisecond, 250*time.Millisecond, func(d Digest) {
		mu.Lock()
		batches = append(batches, len(d.Failed))
		mu.Unlock()
	})
	// A steady trickle every 50 ms never goes quiet for 100 ms; max-hold must still flush,
	// so a trickle that outlasts max-hold arrives in more than one batch — and nothing is lost.
	adds := 0
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		c.Add(func(d *Digest) { d.Failed = append(d.Failed, FailedHost{Name: "x", Phase: "scan"}) })
		adds++
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(batches) < 2 {
		t.Fatalf("max-hold should split a steady trickle into several batches, got %v", batches)
	}
	total := 0
	for _, n := range batches {
		total += n
	}
	if total != adds {
		t.Fatalf("events lost: added %d, delivered %v", adds, batches)
	}
}

func TestSendDigestUsesSummaryAsTitle(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
	}))
	defer srv.Close()
	target := "gotify://" + strings.TrimPrefix(srv.URL, "http://") + "/tok"
	d := nightly()
	if err := NewDispatcher(2*time.Second).SendDigest(target, d); err != nil {
		t.Fatal(err)
	}
	if got["title"] != d.Title() || got["message"] != d.Body() {
		t.Errorf("payload %+v", got)
	}
}
