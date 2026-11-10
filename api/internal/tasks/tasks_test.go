package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"patchdeck/api/internal/db"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	if err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func hostsN(n int) []HostRef {
	out := make([]HostRef, n)
	for i := range out {
		out[i] = HostRef{ID: fmt.Sprintf("h%d", i), Name: fmt.Sprintf("host%d", i)}
	}
	return out
}

func waitIdle(t *testing.T, m *Manager) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(m.BusyHosts()) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("still busy: %v", m.BusyHosts())
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // let finishTask write
}

func TestCapAndSelfLast(t *testing.T) {
	var running, peak int32
	var mu sync.Mutex
	var order []string
	selfStartedAfter := -1
	step := func(_ context.Context, r *Run) Result {
		n := atomic.AddInt32(&running, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		mu.Lock()
		if r.HostID == "h0" {
			selfStartedAfter = len(order) // how many hosts had FINISHED before self started
		}
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt32(&running, -1)
		mu.Lock()
		order = append(order, r.HostID)
		mu.Unlock()
		return Result{Status: StOK}
	}
	m := New(testDB(t), Config{Concurrency: 2, IsSelf: func(id string) bool { return id == "h0" },
		Steps: map[string]Step{KindApply: step}})
	_, n, err := m.Start(KindApply, "t", hostsN(5))
	if err != nil || n != 5 {
		t.Fatalf("start: n=%d err=%v", n, err)
	}
	waitIdle(t, m)
	if peak > 2 {
		t.Errorf("peak concurrency %d, cap 2", peak)
	}
	if selfStartedAfter != 4 || order[len(order)-1] != "h0" {
		t.Errorf("self host must start after all 4 others finished; started after %d, order %v", selfStartedAfter, order)
	}
}

func TestBusyHostsAreLeftOutAndLocked(t *testing.T) {
	release := make(chan struct{})
	step := func(_ context.Context, r *Run) Result { <-release; return Result{} }
	m := New(testDB(t), Config{Concurrency: 4, Steps: map[string]Step{KindScan: step, KindApply: step}})
	if _, n, _ := m.Start(KindScan, "t", hostsN(2)); n != 2 {
		t.Fatalf("first start queued %d", n)
	}
	if _, n, _ := m.Start(KindApply, "t", hostsN(3)); n != 1 {
		t.Errorf("second start should only take the idle host, took %d", n)
	}
	if m.TryLock("h0", "Scheduled scan") {
		t.Error("TryLock must fail on a busy host")
	}
	close(release)
	waitIdle(t, m)
	if !m.TryLock("h0", "Scheduled scan") {
		t.Fatal("TryLock should succeed once idle")
	}
	if st, busy := m.Busy("h0"); !busy || st.Managed || st.Label != "Scheduled scan" {
		t.Errorf("locked host state wrong: %+v busy=%v", st, busy)
	}
	if _, n, _ := m.Start(KindScan, "t", hostsN(1)); n != 0 {
		t.Error("a task must not take a host the scheduler holds")
	}
	m.Unlock("h0")
	if _, busy := m.Busy("h0"); busy {
		t.Error("Unlock should free the host")
	}
}

func TestLinesAndEvents(t *testing.T) {
	m := New(testDB(t), Config{Steps: map[string]Step{KindScan: func(_ context.Context, r *Run) Result {
		r.Line("", "one")
		r.Line("ok", "two")
		return Result{Status: StOK, Summary: "done"}
	}}})
	ch, cancel := m.Subscribe()
	defer cancel()
	if _, _, err := m.Start(KindScan, "t", hostsN(1)); err != nil {
		t.Fatal(err)
	}
	var got []string
	timeout := time.After(3 * time.Second)
	for {
		select {
		case e := <-ch:
			switch e.Type {
			case "line":
				got = append(got, fmt.Sprintf("%d:%s", e.Line.Seq, e.Line.Text))
			case "state":
				got = append(got, e.State)
			}
			if e.Type == "state" && e.State == "idle" {
				want := "queued,running,1:one,2:two,idle"
				if s := join(got); s != want {
					t.Errorf("events %s, want %s", s, want)
				}
				return
			}
		case <-timeout:
			t.Fatalf("timed out; got %v", got)
		}
	}
}

func join(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}

// TestRecover: after a restart, an apply still running on its host (it has a ref) is resumed with
// that ref; a queued host and a running scan are marked interrupted, never started again.
func TestRecover(t *testing.T) {
	d := testDB(t)
	now := time.Now().UTC()
	mustExec := func(q string, args ...any) {
		if _, err := d.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO tasks(id,kind,created_by,created_at,status) VALUES('ta','apply','t',?,'running')`, now)
	mustExec(`INSERT INTO task_hosts(task_id,host_id,host_name,seq,status,ref) VALUES('ta','h0','host0',0,'running','20260101T000000Z-ab'),('ta','h1','host1',1,'queued','')`)
	mustExec(`INSERT INTO tasks(id,kind,created_by,created_at,status) VALUES('ts','scan','t',?,'running')`, now)
	mustExec(`INSERT INTO task_hosts(task_id,host_id,host_name,seq,status,ref) VALUES('ts','h2','host2',0,'running','')`)

	var ran []string
	var mu sync.Mutex
	step := func(_ context.Context, r *Run) Result {
		mu.Lock()
		ran = append(ran, r.Kind+":"+r.HostID+":"+r.Ref)
		mu.Unlock()
		return Result{Status: StOK}
	}
	m := New(d, Config{Steps: map[string]Step{KindApply: step, KindScan: step}, Resumable: map[string]bool{KindApply: true}})
	m.Recover()
	waitIdle(t, m)
	if len(ran) != 1 || ran[0] != "apply:h0:20260101T000000Z-ab" {
		t.Fatalf("only the running apply should resume, with its ref; ran %v", ran)
	}
	sums := m.Interrupted()
	if len(sums) != 2 {
		t.Fatalf("want 2 interrupted tasks, got %+v", sums)
	}
	var apply Summary
	for _, s := range sums {
		if s.ID == "ta" {
			apply = s
		}
	}
	if apply.OK != 1 || apply.NotRun != 1 || len(apply.Hosts) != 1 || apply.Hosts[0] != "host1" {
		t.Errorf("apply task summary wrong: %+v", apply)
	}
	// Resume runs only the host that never ran.
	ran = nil
	if _, n, err := m.Resume("ta", "t"); err != nil || n != 1 {
		t.Fatalf("resume: n=%d err=%v", n, err)
	}
	waitIdle(t, m)
	if len(ran) != 1 || ran[0] != "apply:h1:" {
		t.Errorf("resume should run h1 fresh; ran %v", ran)
	}
	m.Dismiss("ts")
	if s := m.Interrupted(); len(s) != 0 {
		t.Errorf("resumed + dismissed tasks should leave no banner, got %+v", s)
	}
}

// TestNoCapWhenZero: PATCHDECK_BULK_CONCURRENCY < 1 means no cap, as it always did.
func TestNoCapWhenZero(t *testing.T) {
	var running, peak int32
	release := make(chan struct{})
	step := func(_ context.Context, r *Run) Result {
		n := atomic.AddInt32(&running, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		<-release
		atomic.AddInt32(&running, -1)
		return Result{}
	}
	m := New(testDB(t), Config{Concurrency: 0, Steps: map[string]Step{KindScan: step}})
	m.Start(KindScan, "t", hostsN(6))
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt32(&peak) < 6 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	waitIdle(t, m)
	if peak != 6 {
		t.Errorf("peak %d, want all 6 at once with no cap", peak)
	}
}

// TestRecoverRebootWithoutRef: a reboot cut off before its ref was recorded may or may not have
// been sent, so it's failed ("check the host"), never offered for resume (no double reboot).
func TestRecoverRebootWithoutRef(t *testing.T) {
	d := testDB(t)
	now := time.Now().UTC()
	if _, err := d.Exec(`INSERT INTO tasks(id,kind,created_by,created_at,status) VALUES('tr','reboot','t',?,'running')`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO task_hosts(task_id,host_id,host_name,seq,status,ref) VALUES('tr','h0','host0',0,'running','')`); err != nil {
		t.Fatal(err)
	}
	ran := int32(0)
	m := New(d, Config{Steps: map[string]Step{KindReboot: func(context.Context, *Run) Result { atomic.AddInt32(&ran, 1); return Result{} }},
		Resumable: map[string]bool{KindReboot: true}})
	m.Recover()
	time.Sleep(50 * time.Millisecond)
	var st, tst string
	_ = d.QueryRow(`SELECT status FROM task_hosts WHERE task_id='tr'`).Scan(&st)
	_ = d.QueryRow(`SELECT status FROM tasks WHERE id='tr'`).Scan(&tst)
	if ran != 0 || st != StFailed || tst != TaskDone || len(m.Interrupted()) != 0 {
		t.Errorf("ran=%d host=%s task=%s banners=%d; want no run, failed, done, no banner", ran, st, tst, len(m.Interrupted()))
	}
}

// TestResumeKeepsBusyHosts: hosts that are busy when Resume is clicked stay on the banner.
func TestResumeKeepsBusyHosts(t *testing.T) {
	d := testDB(t)
	now := time.Now().UTC()
	if _, err := d.Exec(`INSERT INTO tasks(id,kind,created_by,created_at,status) VALUES('ta','scan','t',?,'interrupted')`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO task_hosts(task_id,host_id,host_name,seq,status,ref) VALUES('ta','h0','host0',0,'interrupted',''),('ta','h1','host1',1,'interrupted','')`); err != nil {
		t.Fatal(err)
	}
	m := New(d, Config{Steps: map[string]Step{KindScan: func(context.Context, *Run) Result { return Result{} }}})
	if !m.TryLock("h1", "Scheduled scan") {
		t.Fatal("lock h1")
	}
	if _, n, err := m.Resume("ta", "t"); err != nil || n != 1 {
		t.Fatalf("resume took %d (err %v), want 1", n, err)
	}
	waitIdle2(t, m, "h0")
	s := m.Interrupted()
	if len(s) != 1 || s[0].NotRun != 1 || s[0].Hosts[0] != "host1" {
		t.Errorf("banner should keep the busy host: %+v", s)
	}
	m.Unlock("h1")
	if _, n, _ := m.Resume("ta", "t"); n != 1 {
		t.Errorf("second resume took %d, want 1", n)
	}
	waitIdle(t, m)
	if s := m.Interrupted(); len(s) != 0 {
		t.Errorf("banner should be gone, got %+v", s)
	}
}

func waitIdle2(t *testing.T, m *Manager, host string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, busy := m.Busy(host); !busy {
			time.Sleep(20 * time.Millisecond)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still busy", host)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestLaggingSubscriberIsClosed: a subscriber that stops reading gets its channel closed (so the
// page reconnects and resyncs) instead of silently missing a host's "idle".
func TestLaggingSubscriberIsClosed(t *testing.T) {
	m := New(testDB(t), Config{Steps: map[string]Step{KindScan: func(_ context.Context, r *Run) Result {
		for i := 0; i < 2000; i++ {
			r.Line("", "x")
		}
		return Result{}
	}}})
	ch, cancel := m.Subscribe()
	defer cancel()
	m.Start(KindScan, "t", hostsN(1))
	waitIdle(t, m)
	n := 0
	for range ch { // must terminate: the channel was closed when it filled
		n++
	}
	if n == 0 || n > 1024 {
		t.Errorf("drained %d events, want 1..1024 then closed", n)
	}
}

func TestPanicBecomesFailure(t *testing.T) {
	d := testDB(t)
	m := New(d, Config{Steps: map[string]Step{KindScan: func(context.Context, *Run) Result { panic("boom") }}})
	id, _, _ := m.Start(KindScan, "t", hostsN(1))
	waitIdle(t, m)
	var st, sum string
	_ = d.QueryRow(`SELECT status, summary FROM task_hosts WHERE task_id=?`, id).Scan(&st, &sum)
	if st != StFailed {
		t.Errorf("status %q, want failed (summary %q)", st, sum)
	}
}
