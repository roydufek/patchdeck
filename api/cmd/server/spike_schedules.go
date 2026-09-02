package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"patchdeck/api/internal/db"
	"patchdeck/api/internal/models"
	"patchdeck/api/internal/scheduler"
)

// spike_schedules.go — /next scheduled jobs (cron scan/apply). Reuses db.ListJobs /
// CreateJob / UpdateJobEnabled / DeleteJob and the same cron + host-control validation the
// JSON API enforces. The /next form targets a single host (the common case); multi-host
// and tag-filter jobs remain available via the API.

// loadJobs lists jobs with their computed last/next run, mirroring the JSON listJobs, plus
// resolving a single-host job's host name for display.
func (a *app) loadJobs() []models.Job {
	jobs, err := db.ListJobs(a.db)
	if err != nil {
		return nil
	}
	now := time.Now()
	for i := range jobs {
		if jobs[i].HostID != "" && strings.TrimSpace(jobs[i].HostName) == "" {
			if h, e := db.GetHost(a.db, jobs[i].HostID); e == nil {
				jobs[i].HostName = h.Name
			}
		}
		if lr, e := db.GetLastJobRun(a.db, jobs[i].ID); e == nil {
			jobs[i].LastRun = lr
		}
		jobs[i].NextRun = scheduler.NextRun(jobs[i].CronExpr, now)
	}
	return jobs
}

func (a *app) renderSchedules(w http.ResponseWriter, errMsg string, form map[string]string) {
	hosts, _ := db.ListHosts(a.db)
	tags, _ := db.ListAllTags(a.db)
	a.renderNext(w, "schedules.html", map[string]any{
		"Jobs": a.loadJobs(), "Hosts": hosts, "Tags": tags, "Err": errMsg, "Form": form,
	})
}

// nextScheduleRuns renders the recent run history for one job (expand-in-place).
func (a *app) nextScheduleRuns(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	runs, _ := db.ListJobRuns(a.db, id, 10)
	a.renderNext(w, "jobruns", map[string]any{"ID": id, "Runs": runs})
}

func (a *app) nextSchedules(w http.ResponseWriter, r *http.Request) {
	a.renderSchedules(w, "", map[string]string{})
}

func (a *app) nextScheduleCreate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	cron := strings.TrimSpace(r.FormValue("cron_expr"))
	mode := strings.TrimSpace(r.FormValue("mode"))
	target := strings.TrimSpace(r.FormValue("target"))
	if target == "" {
		target = "one"
	}
	hostID := strings.TrimSpace(r.FormValue("host_id"))
	tag := strings.TrimSpace(r.FormValue("tag_filter"))
	form := map[string]string{"name": name, "cron_expr": cron, "mode": mode, "target": target, "host_id": hostID, "tag_filter": tag}
	fail := func(msg string) { a.renderSchedules(w, msg, form) }

	if cron == "" {
		fail("A cron expression is required (e.g. 0 3 * * *).")
		return
	}
	if msg := validateCronExpression(cron); msg != "" {
		fail(msg)
		return
	}
	if mode == "" {
		mode = "scan"
	}
	if mode != "scan" && mode != "apply" && mode != "scan_apply" {
		fail("Mode must be scan, apply, or scan + apply.")
		return
	}

	job := models.Job{Name: name, CronExpr: cron, Mode: mode, Enabled: true}
	switch target {
	case "tag":
		if tag == "" {
			fail("Pick a tag to target.")
			return
		}
		job.TagFilter = tag
	case "multi":
		for _, id := range r.Form["host_ids"] {
			if id = strings.TrimSpace(id); id != "" {
				job.HostIDs = append(job.HostIDs, id)
			}
		}
		if len(job.HostIDs) == 0 {
			fail("Pick at least one host.")
			return
		}
	case "all":
		hosts, _ := db.ListHosts(a.db)
		for _, h := range hosts {
			job.HostIDs = append(job.HostIDs, h.ID)
		}
		if len(job.HostIDs) == 0 {
			fail("There are no hosts to schedule.")
			return
		}
	default: // one host
		if hostID == "" {
			fail("Pick a host to run this on.")
			return
		}
		host, err := db.GetHost(a.db, hostID)
		if err != nil {
			fail("That host no longer exists.")
			return
		}
		if mode == "apply" || mode == "scan_apply" {
			if msg := validateJobModeAgainstHostControls(host, "apply"); msg != "" {
				fail(msg)
				return
			}
		}
		job.HostID = hostID
	}
	if err := db.CreateJob(a.db, job); err != nil {
		fail("Failed to create the schedule.")
		return
	}
	w.Header().Set("HX-Redirect", "/next/schedules")
	w.WriteHeader(http.StatusOK)
}

func (a *app) nextScheduleToggle(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_ = r.ParseForm()
	if err := db.UpdateJobEnabled(a.db, id, r.FormValue("enabled") == "1"); err != nil {
		w.Header().Set("HX-Redirect", "/next/schedules")
		w.WriteHeader(http.StatusOK)
		return
	}
	for _, j := range a.loadJobs() {
		if j.ID == id {
			a.renderNext(w, "jobcard", j)
			return
		}
	}
	w.Header().Set("HX-Redirect", "/next/schedules")
	w.WriteHeader(http.StatusOK)
}

func (a *app) nextScheduleDelete(w http.ResponseWriter, r *http.Request) {
	_ = db.DeleteJob(a.db, chi.URLParam(r, "id"))
	w.Header().Set("HX-Redirect", "/next/schedules")
	w.WriteHeader(http.StatusOK)
}
