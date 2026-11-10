package tasks

import (
	"fmt"
	"log"
	"time"

	"patchdeck/api/internal/db"
)

// The tables (tasks, task_hosts) are created in db.Migrate.

// keepFor is how long finished tasks stay in the DB.
const keepFor = 30 * 24 * time.Hour

func (m *Manager) insertTask(id, kind, by string, now time.Time, hosts []HostRef) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO tasks(id,kind,created_by,created_at,status) VALUES(?,?,?,?,?)`, id, kind, by, now, TaskRunning); err != nil {
		return err
	}
	for i, h := range hosts {
		if _, err := tx.Exec(`INSERT INTO task_hosts(task_id,host_id,host_name,seq,status,summary,ref) VALUES(?,?,?,?,?,'','')`, id, h.ID, h.Name, i, StQueued); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *Manager) setHostStatus(taskID, hostID, status, summary string, started, finished time.Time) {
	var err error
	switch {
	case !started.IsZero():
		_, err = m.db.Exec(`UPDATE task_hosts SET status=?, started_at=? WHERE task_id=? AND host_id=?`, status, started, taskID, hostID)
	default:
		_, err = m.db.Exec(`UPDATE task_hosts SET status=?, summary=?, finished_at=? WHERE task_id=? AND host_id=?`, status, summary, finished, taskID, hostID)
	}
	if err != nil {
		log.Printf("tasks: update %s/%s: %v", taskID, hostID, err)
	}
}

// finishTask closes a task: interrupted if any host never ran (a restart), else done.
func (m *Manager) finishTask(taskID string) {
	var n int
	_ = m.db.QueryRow(`SELECT count(*) FROM task_hosts WHERE task_id=? AND status=?`, taskID, StInterrupted).Scan(&n)
	status := TaskDone
	if n > 0 {
		status = TaskInterrupted
	}
	if _, err := m.db.Exec(`UPDATE tasks SET status=?, finished_at=? WHERE id=?`, status, time.Now().UTC(), taskID); err != nil {
		log.Printf("tasks: finish %s: %v", taskID, err)
	}
}

func (m *Manager) prune(now time.Time) {
	cut := now.Add(-keepFor)
	_, _ = m.db.Exec(`DELETE FROM task_hosts WHERE task_id IN (SELECT id FROM tasks WHERE created_at < ? AND status <> ?)`, cut, TaskRunning)
	_, _ = m.db.Exec(`DELETE FROM tasks WHERE created_at < ? AND status <> ?`, cut, TaskRunning)
}

type pending struct {
	taskID, kind string
	host         HostRef
	status, ref  string
}

// Recover runs once at startup. Work still running on a host is picked back up (resumable kinds:
// an apply with a recorded id, a reboot's watch); every other running or queued host is marked
// interrupted. Nothing that hadn't started is started again: that's the operator's call
// ("Resume remaining").
func (m *Manager) Recover() {
	rows, err := m.db.Query(`SELECT th.task_id, t.kind, th.host_id, th.host_name, th.status, th.ref
		FROM task_hosts th JOIN tasks t ON t.id = th.task_id
		WHERE t.status = ? AND th.status IN (?, ?) ORDER BY th.task_id, th.seq`, TaskRunning, StQueued, StRunning)
	if err != nil {
		log.Printf("tasks: recover: %v", err)
		return
	}
	var all []pending
	for rows.Next() {
		var p pending
		if rows.Scan(&p.taskID, &p.kind, &p.host.ID, &p.host.Name, &p.status, &p.ref) == nil {
			all = append(all, p)
		}
	}
	rows.Close()

	resume := map[string][]pending{}
	now := time.Now().UTC()
	for _, p := range all {
		// The ref says how far the work got (the apply's id, the reboot's send time).
		switch {
		case p.status == StRunning && m.cfg.Resumable[p.kind] && p.ref != "":
			resume[p.taskID] = append(resume[p.taskID], p)
		case p.status == StRunning && p.kind == KindReboot:
			// The reboot may or may not have been sent: never offer to send it again. Say so in the
			// activity log, where the operator will see it.
			msg := "Patchdeck restarted while sending the reboot: check the host, then scan it"
			m.setHostStatus(p.taskID, p.host.ID, StFailed, msg, time.Time{}, now)
			_ = db.RecordActivity(m.db, p.host.ID, p.host.Name, "reboot_fail", msg)
		case p.status == StRunning:
			// A scan or restart cut off mid-run: running it again is safe.
			m.setHostStatus(p.taskID, p.host.ID, StInterrupted, "cut off: Patchdeck restarted mid-run", time.Time{}, now)
		default:
			m.setHostStatus(p.taskID, p.host.ID, StInterrupted, "not run: Patchdeck restarted", time.Time{}, now)
		}
	}
	// Close the tasks with nothing left to resume: interrupted if a host can be resumed, else done.
	_, _ = m.db.Exec(`UPDATE tasks SET status=?, finished_at=? WHERE status=?
		AND id NOT IN (SELECT DISTINCT task_id FROM task_hosts WHERE status=?)
		AND id IN (SELECT DISTINCT task_id FROM task_hosts WHERE status=?)`,
		TaskInterrupted, now, TaskRunning, StRunning, StInterrupted)
	_, _ = m.db.Exec(`UPDATE tasks SET status=?, finished_at=? WHERE status=? AND id NOT IN (SELECT DISTINCT task_id FROM task_hosts WHERE status=?)`,
		TaskDone, now, TaskRunning, StRunning)
	for taskID, ps := range resume {
		hosts := make([]HostRef, 0, len(ps))
		refs := map[string]string{}
		m.mu.Lock()
		for _, p := range ps {
			hosts = append(hosts, p.host)
			refs[p.host.ID] = p.ref
			m.hosts[p.host.ID] = &HostState{TaskID: taskID, Kind: p.kind, Label: label(p.kind), Status: StRunning, Since: now, Managed: true}
		}
		m.mu.Unlock()
		log.Printf("tasks: resuming %d host(s) of %s task %s after restart", len(hosts), ps[0].kind, taskID)
		go m.run(taskID, ps[0].kind, hosts, len(hosts), refs)
	}
	// A resumed task may also hold interrupted hosts; finishTask marks it interrupted when it ends.
	if n := len(all); n > 0 {
		log.Printf("tasks: recovered %d unfinished host step(s) (%d task(s) resuming)", n, len(resume))
	}
}

// Summary describes a finished task for the dashboard's "interrupted" banner.
type Summary struct {
	ID, Kind  string
	CreatedAt time.Time
	OK        int
	Failed    int
	NotRun    int // interrupted
	Resumed   int // handed to a new task by "Resume remaining" (its outcome shows there, not here)
	Hosts     []string
}

// Interrupted lists tasks a restart cut short that the operator hasn't resumed or dismissed.
func (m *Manager) Interrupted() []Summary {
	rows, err := m.db.Query(`SELECT id, kind, created_at FROM tasks WHERE status=? ORDER BY created_at DESC LIMIT 5`, TaskInterrupted)
	if err != nil {
		return nil
	}
	var out []Summary
	for rows.Next() {
		var s Summary
		if rows.Scan(&s.ID, &s.Kind, &s.CreatedAt) == nil {
			out = append(out, s)
		}
	}
	rows.Close()
	for i := range out {
		hr, err := m.db.Query(`SELECT host_name, status FROM task_hosts WHERE task_id=? ORDER BY seq`, out[i].ID)
		if err != nil {
			continue
		}
		for hr.Next() {
			var name, st string
			if hr.Scan(&name, &st) != nil {
				continue
			}
			switch st {
			case StOK, StSkipped:
				out[i].OK++
			case StResumed:
				out[i].Resumed++
			case StInterrupted:
				out[i].NotRun++
				out[i].Hosts = append(out[i].Hosts, name)
			default:
				out[i].Failed++
			}
		}
		hr.Close()
	}
	return out
}

// Resume starts a new task for the hosts an interrupted task never ran.
func (m *Manager) Resume(taskID, by string) (string, int, error) {
	var kind, status string
	if err := m.db.QueryRow(`SELECT kind, status FROM tasks WHERE id=?`, taskID).Scan(&kind, &status); err != nil {
		return "", 0, fmt.Errorf("task not found")
	}
	if status != TaskInterrupted {
		return "", 0, fmt.Errorf("task is %s, not interrupted", status)
	}
	rows, err := m.db.Query(`SELECT host_id, host_name FROM task_hosts WHERE task_id=? AND status=? ORDER BY seq`, taskID, StInterrupted)
	if err != nil {
		return "", 0, err
	}
	var hosts []HostRef
	for rows.Next() {
		var h HostRef
		if rows.Scan(&h.ID, &h.Name) == nil {
			hosts = append(hosts, h)
		}
	}
	rows.Close()
	newID, take, err := m.start(kind, by, hosts)
	if err != nil {
		return "", 0, err
	}
	// Only the hosts the new task took leave the banner; any still busy elsewhere stay resumable.
	for _, h := range take {
		_, _ = m.db.Exec(`UPDATE task_hosts SET status=? WHERE task_id=? AND host_id=?`, StResumed, taskID, h.ID)
	}
	if len(take) == len(hosts) {
		_, _ = m.db.Exec(`UPDATE tasks SET status=? WHERE id=?`, TaskResumed, taskID)
	}
	return newID, len(take), nil
}

// Dismiss hides an interrupted task's banner.
func (m *Manager) Dismiss(taskID string) {
	_, _ = m.db.Exec(`UPDATE tasks SET status=? WHERE id=? AND status=?`, TaskDismissed, taskID, TaskInterrupted)
}
