// Package tasks runs host actions (scan, apply, restart, reboot) on the SERVER, so they keep going
// when the browser leaves the page (decision 0009). The browser used to drive bulk actions itself;
// closing the tab cancelled scans and stranded every host still queued.
//
// A task is one action on one or more hosts. The manager queues it, runs it with a concurrency cap
// (apply also staggered, and Patchdeck's own host last), records every host's progress in the DB,
// keeps each running host's recent output in memory, and publishes events the dashboard follows.
// One host does one thing at a time: a host queued or running anywhere (a task, a scheduled job,
// the JSON API) can't be given a second action.
//
// After a Patchdeck restart, Recover picks back up the work that is still running on a host (an
// apply's detached upgrade, a reboot's recovery watch) and marks everything else interrupted, for
// the operator to resume by hand. Nothing new starts on its own after a restart.
package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Action kinds.
const (
	KindScan    = "scan"
	KindApply   = "apply"
	KindRestart = "restart"
	KindReboot  = "reboot"
)

// Per-host statuses.
const (
	StQueued      = "queued"
	StRunning     = "running"
	StOK          = "ok"
	StFailed      = "failed"
	StSkipped     = "skipped"
	StInterrupted = "interrupted" // never ran (or ran nothing) because Patchdeck restarted: resumable
	StResumed     = "resumed"     // an interrupted host that "Resume remaining" handed to a new task
)

// Task statuses.
const (
	TaskRunning     = "running"
	TaskDone        = "done"
	TaskInterrupted = "interrupted" // some hosts never ran because Patchdeck restarted
	TaskDismissed   = "dismissed"   // the operator dismissed the interrupted banner
	TaskResumed     = "resumed"     // the operator resumed the remaining hosts as a new task
)

// logCap is how many output lines a running host keeps for a page that (re)attaches.
const logCap = 500

// Line is one line of a host's output. Seq increases per run, so a page can skip lines it has.
type Line struct {
	Seq   int
	Class string // "", "ok", "err"
	Text  string
}

// Event is what the dashboard follows: a host's state changed, or it printed a line.
type Event struct {
	Type   string // "state" | "line"
	HostID string
	State  string // state: queued | running | idle
	Line   Line   // line
}

// HostRef names a host to act on.
type HostRef struct{ ID, Name string }

// HostState is what a busy host is doing right now.
type HostState struct {
	TaskID  string
	Kind    string
	Label   string // e.g. "Applying updates", "Scheduled apply"
	Status  string // queued | running
	Since   time.Time
	Managed bool // false = held by the scheduler / JSON API (no live log)
	lines   []Line
	seq     int
}

// Run is a step's handle on its host: who it is, how to print, how to record a resume ref.
type Run struct {
	TaskID, HostID, HostName, Kind string
	// Ref is set when resuming after a Patchdeck restart (e.g. the apply id); empty on a fresh run.
	Ref string
	// Hosts is how many hosts the task covers (steps notify only for multi-host tasks).
	Hosts int
	m     *Manager
}

// Line prints one line of output for this host.
func (r *Run) Line(class, text string) { r.m.emitLine(r.HostID, class, text) }

// SetRef records what a resume after a Patchdeck restart needs (e.g. the detached apply's id).
func (r *Run) SetRef(ref string) {
	if _, err := r.m.db.Exec(`UPDATE task_hosts SET ref=? WHERE task_id=? AND host_id=?`, ref, r.TaskID, r.HostID); err != nil {
		log.Printf("tasks: save ref %s/%s: %v", r.TaskID, r.HostID, err)
	}
}

// Result is a step's outcome.
type Result struct {
	Status  string // StOK | StFailed | StSkipped | StInterrupted (nothing ran; safe to resume)
	Summary string
}

// Step performs one action on one host.
type Step func(ctx context.Context, r *Run) Result

// Config wires the manager to the app.
type Config struct {
	Concurrency  int // hosts a task runs at once; <1 = no cap (PATCHDECK_BULK_CONCURRENCY)
	ApplyStagger time.Duration
	// IsSelf reports whether a host is the machine Patchdeck runs on (applies patch it last).
	IsSelf func(hostID string) bool
	Steps  map[string]Step
	// Resumable kinds pick a running host back up after a restart, if the step had recorded a ref
	// (Run.SetRef) — proof the work reached the host. The resumed step sees Run.Ref.
	Resumable map[string]bool
}

// Manager runs tasks and tracks which hosts are busy.
type Manager struct {
	db  *sql.DB
	cfg Config

	mu    sync.Mutex
	hosts map[string]*HostState
	subs  map[chan Event]struct{}
}

// New builds a manager. Call Recover once at startup, before serving.
func New(db *sql.DB, cfg Config) *Manager {
	if cfg.IsSelf == nil {
		cfg.IsSelf = func(string) bool { return false }
	}
	return &Manager{db: db, cfg: cfg, hosts: map[string]*HostState{}, subs: map[chan Event]struct{}{}}
}

// Labels for the in-progress panel.
func label(kind string) string {
	switch kind {
	case KindScan:
		return "Scanning"
	case KindApply:
		return "Applying updates"
	case KindRestart:
		return "Restarting services"
	case KindReboot:
		return "Rebooting"
	}
	return "Working"
}

// Start queues kind on hosts and runs it in the background. Hosts that are already busy are left
// out; the returned count says how many were queued. Zero queued = no task is created.
func (m *Manager) Start(kind, createdBy string, hosts []HostRef) (taskID string, queued int, err error) {
	taskID, take, err := m.start(kind, createdBy, hosts)
	return taskID, len(take), err
}

func (m *Manager) start(kind, createdBy string, hosts []HostRef) (taskID string, take []HostRef, err error) {
	if m.cfg.Steps[kind] == nil {
		return "", nil, fmt.Errorf("unknown action %q", kind)
	}
	now := time.Now().UTC()
	taskID = uuid.NewString()
	m.mu.Lock()
	for _, h := range hosts {
		if _, busy := m.hosts[h.ID]; busy {
			continue
		}
		take = append(take, h)
		m.hosts[h.ID] = &HostState{TaskID: taskID, Kind: kind, Label: label(kind), Status: StQueued, Since: now, Managed: true}
	}
	m.mu.Unlock()
	if len(take) == 0 {
		return "", nil, nil
	}
	if err := m.insertTask(taskID, kind, createdBy, now, take); err != nil {
		m.mu.Lock()
		for _, h := range take {
			delete(m.hosts, h.ID)
		}
		m.mu.Unlock()
		return "", nil, err
	}
	for _, h := range take {
		m.publish(Event{Type: "state", HostID: h.ID, State: StQueued})
	}
	m.prune(now)
	go m.run(taskID, kind, take, len(take), nil)
	return taskID, take, nil
}

// order puts the hosts in run order: for an apply, Patchdeck's own host goes last.
func (m *Manager) order(kind string, hosts []HostRef) (rest, last []HostRef) {
	for _, h := range hosts {
		if kind == KindApply && m.cfg.IsSelf(h.ID) {
			last = append(last, h)
		} else {
			rest = append(rest, h)
		}
	}
	return
}

// run executes a task's hosts. refs (resume only) maps host → the ref to resume with.
func (m *Manager) run(taskID, kind string, hosts []HostRef, total int, refs map[string]string) {
	rest, last := m.order(kind, hosts)
	limit := m.cfg.Concurrency
	if limit < 1 || limit > len(hosts) {
		limit = len(hosts)
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	var lastStart time.Time
	launch := func(h HostRef) {
		sem <- struct{}{}
		if kind == KindApply && m.cfg.ApplyStagger > 0 && !lastStart.IsZero() {
			if wait := m.cfg.ApplyStagger - time.Since(lastStart); wait > 0 {
				time.Sleep(wait)
			}
		}
		lastStart = time.Now()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			m.runHost(taskID, kind, h, total, refs[h.ID])
		}()
	}
	for _, h := range rest {
		launch(h)
	}
	if len(last) > 0 {
		wg.Wait() // Patchdeck's own host starts only once every other host has finished
		for _, h := range last {
			launch(h)
		}
	}
	wg.Wait()
	m.finishTask(taskID)
}

func (m *Manager) runHost(taskID, kind string, h HostRef, total int, ref string) {
	started := time.Now().UTC()
	m.mu.Lock()
	st := m.hosts[h.ID]
	if st == nil || st.TaskID != taskID { // (resume) claim it
		st = &HostState{TaskID: taskID, Kind: kind, Label: label(kind), Managed: true}
		m.hosts[h.ID] = st
	}
	st.Status, st.Since, st.lines, st.seq = StRunning, started, nil, 0
	m.mu.Unlock()
	m.setHostStatus(taskID, h.ID, StRunning, "", started, time.Time{})
	m.publish(Event{Type: "state", HostID: h.ID, State: StRunning})

	run := &Run{TaskID: taskID, HostID: h.ID, HostName: h.Name, Kind: kind, Ref: ref, Hosts: total, m: m}
	res := m.safeStep(kind, run)

	m.setHostStatus(taskID, h.ID, res.Status, res.Summary, time.Time{}, time.Now().UTC())
	m.mu.Lock()
	delete(m.hosts, h.ID)
	m.mu.Unlock()
	m.publish(Event{Type: "state", HostID: h.ID, State: "idle"})
}

// safeStep runs the step and turns a panic into a failure, so one bad host can't take the
// process (and every other running task) down with it.
func (m *Manager) safeStep(kind string, run *Run) (res Result) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("tasks: %s on %s PANIC recovered: %v", kind, run.HostName, r)
			res = Result{Status: StFailed, Summary: fmt.Sprintf("internal error: %v", r)}
		}
	}()
	res = m.cfg.Steps[kind](context.Background(), run)
	if res.Status == "" {
		res.Status = StOK
	}
	return res
}

func (m *Manager) emitLine(hostID, class, text string) {
	m.mu.Lock()
	st := m.hosts[hostID]
	if st == nil {
		m.mu.Unlock()
		return
	}
	st.seq++
	ln := Line{Seq: st.seq, Class: class, Text: text}
	st.lines = append(st.lines, ln)
	if len(st.lines) > logCap {
		st.lines = st.lines[len(st.lines)-logCap:]
	}
	m.mu.Unlock()
	m.publish(Event{Type: "line", HostID: hostID, Line: ln})
}

// TryLock claims an idle host for work that doesn't go through a task (a scheduled job, the JSON
// API). It fails when the host is queued or running anywhere. Release it with Unlock.
func (m *Manager) TryLock(hostID, what string) bool {
	m.mu.Lock()
	if _, busy := m.hosts[hostID]; busy {
		m.mu.Unlock()
		return false
	}
	m.hosts[hostID] = &HostState{Kind: what, Label: what, Status: StRunning, Since: time.Now().UTC()}
	m.mu.Unlock()
	m.publish(Event{Type: "state", HostID: hostID, State: StRunning})
	return true
}

// Unlock releases a host claimed with TryLock.
func (m *Manager) Unlock(hostID string) {
	m.mu.Lock()
	st := m.hosts[hostID]
	if st == nil || st.Managed {
		m.mu.Unlock()
		return
	}
	delete(m.hosts, hostID)
	m.mu.Unlock()
	m.publish(Event{Type: "state", HostID: hostID, State: "idle"})
}

// Busy reports what a host is doing, if anything.
func (m *Manager) Busy(hostID string) (HostState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.hosts[hostID]
	if st == nil {
		return HostState{}, false
	}
	return *st, true
}

// BusyHosts lists the busy host ids.
func (m *Manager) BusyHosts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.hosts))
	for id := range m.hosts {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Log returns a busy host's recent output.
func (m *Manager) Log(hostID string) []Line {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.hosts[hostID]
	if st == nil {
		return nil
	}
	return append([]Line(nil), st.lines...)
}

// Subscribe returns a channel of events; call cancel when done. A subscriber that falls behind
// (its buffer fills) has its channel CLOSED rather than silently missing events: its stream ends,
// the page's EventSource reconnects, and the fresh "sync" puts every card right again.
func (m *Manager) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 1024)
	m.mu.Lock()
	m.subs[ch] = struct{}{}
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		delete(m.subs, ch)
		m.mu.Unlock()
	}
}

func (m *Manager) publish(e Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch := range m.subs {
		select {
		case ch <- e:
		default:
			delete(m.subs, ch) // lagging: drop it (its cancel then finds nothing to remove)
			close(ch)
		}
	}
}
