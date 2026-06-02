package db

import (
	"database/sql"
	"testing"

	"patchdeck/api/internal/models"
)

func mkHost(t *testing.T, d *sql.DB, name string) string {
	t.Helper()
	id, err := CreateHost(d, models.Host{Name: name, Address: name + ".local", Port: 22, SSHUser: "root", AuthType: "password"})
	if err != nil {
		t.Fatalf("CreateHost(%s): %v", name, err)
	}
	return id
}

func jobsByName(t *testing.T, d *sql.DB) map[string]models.Job {
	t.Helper()
	js, err := ListJobs(d)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	m := map[string]models.Job{}
	for _, j := range js {
		m[j.Name] = j
	}
	return m
}

// TestDeleteHostReconcilesJobs verifies that removing a host drops it from each
// job's target list and deletes a job only when it has no targets left — instead
// of the old behavior that deleted any job whose stored primary host matched.
func TestDeleteHostReconcilesJobs(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	a := mkHost(t, d, "hostA")
	b := mkHost(t, d, "hostB")
	c := mkHost(t, d, "hostC")

	must := func(err error, ctx string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", ctx, err)
		}
	}
	must(CreateJob(d, models.Job{Name: "multi-ab", HostIDs: []string{a, b}, CronExpr: "0 3 * * *", Mode: "scan", Enabled: true}), "create multi-ab")
	must(CreateJob(d, models.Job{Name: "legacy-a", HostID: a, CronExpr: "0 3 * * *", Mode: "scan", Enabled: true}), "create legacy-a")
	must(CreateJob(d, models.Job{Name: "only-a", HostIDs: []string{a}, CronExpr: "0 3 * * *", Mode: "scan", Enabled: true}), "create only-a")
	must(CreateJob(d, models.Job{Name: "tag-web", TagFilter: "web", CronExpr: "0 3 * * *", Mode: "scan", Enabled: true}), "create tag-web")
	must(CreateJob(d, models.Job{Name: "multi-bc", HostIDs: []string{b, c}, CronExpr: "0 3 * * *", Mode: "scan", Enabled: true}), "create multi-bc")

	// Seed a run on only-a so we can confirm its run history is cleaned up on delete.
	onlyAID := jobsByName(t, d)["only-a"].ID
	if _, err := CreateJobRun(d, onlyAID, "scan"); err != nil {
		t.Fatalf("CreateJobRun: %v", err)
	}

	if err := DeleteHost(d, a); err != nil {
		t.Fatalf("DeleteHost: %v", err)
	}

	got := jobsByName(t, d)

	// Jobs whose only target was hostA are deleted.
	if _, ok := got["only-a"]; ok {
		t.Error("only-a should be deleted (its only host was removed)")
	}
	if _, ok := got["legacy-a"]; ok {
		t.Error("legacy-a should be deleted (its only host was removed)")
	}

	// Multi-host job survives, now targeting only hostB, with no dangling primary.
	if j, ok := got["multi-ab"]; !ok {
		t.Error("multi-ab should survive (still targets hostB)")
	} else {
		if len(j.HostIDs) != 1 || j.HostIDs[0] != b {
			t.Errorf("multi-ab HostIDs=%v, want [%s]", j.HostIDs, b)
		}
		if j.HostID == a {
			t.Errorf("multi-ab HostID still points at deleted host %s", a)
		}
	}

	// Tag job is never touched by host deletion.
	if j, ok := got["tag-web"]; !ok {
		t.Error("tag-web should survive untouched")
	} else if j.TagFilter != "web" {
		t.Errorf("tag-web TagFilter=%q, want \"web\"", j.TagFilter)
	}

	// A job that never referenced hostA is unaffected.
	if j, ok := got["multi-bc"]; !ok {
		t.Error("multi-bc should be unaffected")
	} else if len(j.HostIDs) != 2 {
		t.Errorf("multi-bc HostIDs=%v, want 2 entries", j.HostIDs)
	}

	// The deleted job's run history must be gone (no orphaned job_runs).
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM job_runs WHERE job_id=?`, onlyAID).Scan(&n); err != nil {
		t.Fatalf("count job_runs: %v", err)
	}
	if n != 0 {
		t.Errorf("only-a left %d orphaned job_runs after host delete", n)
	}
}
