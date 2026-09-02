package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"patchdeck/api/internal/auth"
	"patchdeck/api/internal/db"
	"patchdeck/api/internal/models"
	"patchdeck/api/internal/sshx"
)

// --- The server-rendered UI (Go html/template + HTMX + SSE), served at the root. It reuses
// the same data layer as the JSON API (ListHosts / ListScanSnapshots / ScanHostStreaming /
// probeConnectivity). Handlers are named nextXxx for historical reasons — the UI was first
// built under a /next path prefix (v2.0–v2.2) before it took over the root in v2.3.

//go:embed spikeassets/htmx.min.js spikeassets/sse.min.js spikeassets/logo.png
var nextAssets embed.FS

//go:embed spiketmpl/*.html
var nextTmplFS embed.FS

var nextTmpl = template.Must(template.New("next").Funcs(template.FuncMap{
	"isSecurity": isSecuritySource,
	"evtTone":    eventTone,
	"evtLabel":   eventLabel,
	"dict":       tmplDict,
}).ParseFS(nextTmplFS, "spiketmpl/*.html"))

// tmplDict builds a map from alternating key/value args, so a template can pass a small
// sub-object to a partial: {{template "x" (dict "ID" .ID "P" .Prefs)}}.
func tmplDict(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			m[k] = kv[i+1]
		}
	}
	return m
}

// isSecuritySource reports whether an apt suite string denotes a security update
// (e.g. "jammy-security", "bookworm-security"). Patchdeck stores no dedicated security
// flag, so this is derived from PackageInfo.Source.
func isSecuritySource(source string) bool {
	return strings.Contains(strings.ToLower(source), "security")
}

// nextHostView is the per-host summary a dashboard card renders — signal at rest.
type nextHostView struct {
	ID, Name, Address          string
	HasScan                    bool
	UpdateCount, SecurityCount int
	NeedsReboot                bool
	RebootReason               string
	RestartCount               int
	DeferredCount              int
	OsName, OsVersion, OSShort string
	Uptime                     string
	UpdatedAt                  time.Time
	ScanAge                    string
	State                      string   // uptodate | updates | attention | noscan | unverified
	Tags                       []string // host tags (for chips + grouping)
	Unverified                 bool     // approval-required host whose first key isn't approved yet
	PendingKey                 bool     // a host key is captured and awaiting operator approval
	// restart-intel: NeedsRestart split into the units a smart restart can act on vs the ones
	// that need a reboot (learned-resistant, or dbus with no coordinated handler). RebootAny is
	// true when the host needs a reboot for any reason (kernel/needsreboot OR a reboot-only unit).
	RestartServices []string
	RebootServices  []string
	RebootAny       bool
	IsSelf          bool // this host is the machine Patchdeck runs on (boot_id match)
	ExcludeFromBulk bool // operator-protected: kept out of fleet-wide reboots
}

// resolveRestartBuckets splits a snapshot's needs-restart list into the restartable bucket and
// the reboot-only bucket. New scans carry the buckets pre-classified; for pre-restart-intel
// snapshots (both buckets empty but NeedsRestart populated) it falls back to treating everything
// as restartable so the UI still offers the action.
func resolveRestartBuckets(s models.ScanSnapshot) (restartable, rebootOnly []string) {
	if len(s.RestartServices) == 0 && len(s.RebootServices) == 0 && len(s.NeedsRestart) > 0 {
		return s.NeedsRestart, nil
	}
	return s.RestartServices, s.RebootServices
}

func fillFromSnapshot(v *nextHostView, s models.ScanSnapshot) {
	v.HasScan = true
	v.UpdateCount = len(s.Packages)
	for _, p := range s.Packages {
		if isSecuritySource(p.Source) {
			v.SecurityCount++
		}
	}
	v.NeedsReboot = s.NeedsReboot
	v.RebootReason = s.RebootReason
	v.RestartCount = len(s.NeedsRestart)
	v.RestartServices, v.RebootServices = resolveRestartBuckets(s)
	v.RebootAny = s.NeedsReboot || len(v.RebootServices) > 0
	v.DeferredCount = len(s.DeferredPackages)
	v.OsName = s.OsName
	v.OsVersion = s.OsVersion
	v.OSShort = shortOS(s.OsName, s.OsVersion)
	v.Uptime = s.Uptime
	v.UpdatedAt = s.UpdatedAt
	v.ScanAge = timeAgoShort(s.UpdatedAt)
}

// shortOS compacts a scan's OS pretty-name into a card-friendly label, e.g.
// "Debian GNU/Linux 12 (bookworm)" + "12" -> "Debian 12". Keeps the distro word plus
// the version so a long PRETTY_NAME doesn't dominate the card row.
func shortOS(name, version string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	distro := name
	if i := strings.IndexByte(name, ' '); i > 0 {
		distro = name[:i]
	}
	if version = strings.TrimSpace(version); version != "" {
		return distro + " " + version
	}
	return distro
}

func deriveState(v nextHostView) string {
	switch {
	case v.PendingKey:
		return "attention" // a captured host key needs approval before anything else
	case v.Unverified:
		return "unverified" // approval-required host, first connection not approved yet
	case !v.HasScan:
		return "noscan"
	case v.NeedsReboot || v.RestartCount > 0:
		return "attention"
	case v.UpdateCount > 0:
		return "updates"
	default:
		return "uptodate"
	}
}

func timeAgoShort(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func (a *app) nextHostViews() ([]nextHostView, error) {
	hosts, err := db.ListHosts(a.db)
	if err != nil {
		return nil, err
	}
	snaps, err := db.ListScanSnapshots(a.db)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]models.ScanSnapshot, len(snaps))
	for _, s := range snaps {
		byID[s.HostID] = s
	}
	out := make([]nextHostView, 0, len(hosts))
	for _, h := range hosts {
		v := nextHostView{ID: h.ID, Name: h.Name, Address: h.Address}
		setHostMeta(&v, h)
		if s, ok := byID[h.ID]; ok {
			fillFromSnapshot(&v, s)
			v.IsSelf = a.isSelfHost(s)
		}
		v.State = deriveState(v)
		out = append(out, v)
	}
	return out, nil
}

// setHostMeta copies host-level (non-snapshot) fields onto a view: tags and the host-key
// verification state (unverified = no trusted key yet / awaiting first-connection approval;
// keyChanged = a rotated key is pending operator approval).
func setHostMeta(v *nextHostView, h models.Host) {
	v.Tags = h.Tags
	v.ExcludeFromBulk = h.ExcludeFromBulk
	// Approval-required = pinned trust mode with no trusted key yet (new /next hosts start
	// "pinned, no fingerprint" so the first connection must be reviewed + approved). Existing
	// TOFU hosts keep trust-on-first-use and are never shown as unverified.
	v.Unverified = strings.EqualFold(strings.TrimSpace(h.HostKeyTrustMode), "pinned") && strings.TrimSpace(h.HostKeyTrusted) == ""
	v.PendingKey = strings.TrimSpace(h.HostKeyPending) != ""
}

// nextHostView loads one host's view plus its raw snapshot (for the detail page). There is
// no single-host snapshot getter in db, so it filters ListScanSnapshots (fleets are small).
func (a *app) nextHostView(id string) (nextHostView, models.ScanSnapshot, error) {
	h, err := db.GetHost(a.db, id)
	if err != nil {
		return nextHostView{}, models.ScanSnapshot{}, err
	}
	v := nextHostView{ID: h.ID, Name: h.Name, Address: h.Address}
	setHostMeta(&v, h)
	var snap models.ScanSnapshot
	snaps, _ := db.ListScanSnapshots(a.db)
	for _, s := range snaps {
		if s.HostID == id {
			snap = s
			fillFromSnapshot(&v, s)
			v.IsSelf = a.isSelfHost(s)
			break
		}
	}
	v.State = deriveState(v)
	return v, snap, nil
}

// isSelfHost reports whether a scanned host is the machine Patchdeck itself runs on, by
// matching the scan's boot_id against Patchdeck's own. boot_id is the host kernel's and is not
// namespaced, so the container and its host share it. Empty on either side => not self (safe:
// self-detection simply doesn't fire, and the operator can still mark the host manually).
func (a *app) isSelfHost(s models.ScanSnapshot) bool {
	return a.selfBootID != "" && s.BootID != "" && s.BootID == a.selfBootID
}

// nextAuth gates the /next pages on a session cookie, redirecting a browser to the login
// page when unauthenticated rather than returning a bare 401 (cookie only — a browser
// EventSource/navigation carries it automatically; pd_ API tokens aren't used here).
func (a *app) nextAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if claims := a.sessionClaims(r); claims != nil {
			next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), claims)))
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	})
}

func (a *app) nextAsset(w http.ResponseWriter, r *http.Request) {
	var path, ctype string
	switch chi.URLParam(r, "file") {
	case "htmx.min.js":
		path, ctype = "spikeassets/htmx.min.js", "application/javascript; charset=utf-8"
	case "sse.min.js":
		path, ctype = "spikeassets/sse.min.js", "application/javascript; charset=utf-8"
	case "logo.png":
		path, ctype = "spikeassets/logo.png", "image/png"
	default:
		http.NotFound(w, r)
		return
	}
	b, err := nextAssets.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(b)
}

// nextFavicon serves the embedded Patchdeck logo as the site favicon (the SPA's static
// favicon is gone with the React build).
func (a *app) nextFavicon(w http.ResponseWriter, r *http.Request) {
	b, err := nextAssets.ReadFile("spikeassets/logo.png")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(b)
}

func (a *app) renderNext(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Pages and fragments are live state — always revalidate so a deploy shows up on the
	// next load instead of a stale cached copy (the browser otherwise heuristically caches
	// a header-less HTML response). The hashed JS assets stay immutable-cacheable.
	w.Header().Set("Cache-Control", "no-cache")
	if err := nextTmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("next render %s: %v", name, err)
	}
}

// nextSummary is the fleet-level "how are things" line above the host list.
type nextSummary struct {
	Total, NeedAttention, UpToDate, Updates, Security int
	// RebootHosts is how many hosts a "Reboot all" would actually reboot: those that need a
	// reboot (kernel or a reboot-only unit) minus the self-host and any operator-protected host.
	// RebootExcluded is how many needing-reboot hosts are held back (self/protected) — surfaced
	// so the confirm can say "3 hosts, 1 held back (Patchdeck's own)".
	RebootHosts, RebootExcluded int
	LastScan                    string
}

// buildSummary aggregates the fleet and splits hosts into the attention vs healthy
// groups the dashboard renders. Attention = anything actionable at rest (updates,
// reboot, or a service restart); healthy = up-to-date or never-scanned.
func buildSummary(views []nextHostView) (nextSummary, []nextHostView, []nextHostView) {
	var s nextSummary
	var attention, healthy []nextHostView
	var latest time.Time
	for _, v := range views {
		s.Total++
		s.Updates += v.UpdateCount
		s.Security += v.SecurityCount
		if v.RebootAny {
			if v.IsSelf || v.ExcludeFromBulk {
				s.RebootExcluded++
			} else {
				s.RebootHosts++
			}
		}
		if v.PendingKey || v.Unverified || (v.HasScan && (v.UpdateCount > 0 || v.NeedsReboot || v.RestartCount > 0)) {
			s.NeedAttention++
			attention = append(attention, v)
		} else {
			if v.State == "uptodate" {
				s.UpToDate++
			}
			healthy = append(healthy, v)
		}
		if v.HasScan && v.UpdatedAt.After(latest) {
			latest = v.UpdatedAt
		}
	}
	if !latest.IsZero() {
		s.LastScan = timeAgoShort(latest)
	}
	return s, attention, healthy
}

func (a *app) nextDashboard(w http.ResponseWriter, r *http.Request) {
	views, err := a.nextHostViews()
	if err != nil {
		http.Error(w, "failed to load hosts", http.StatusInternalServerError)
		return
	}
	summary, _, _ := buildSummary(views)
	// Cards render into a flat pool; the client groups + sorts + filters them live (so a host
	// re-files itself the instant a scan finishes, no reload). HasTags gates the Group-by control.
	hasTags := false
	for _, v := range views {
		if len(v.Tags) > 0 {
			hasTags = true
			break
		}
	}
	// Fan-out watchdog: how long a host may hold a concurrency slot before it's force-freed, so a
	// severed SSE stream (proxy idle-timeout, server restart) can never permanently stall the
	// remaining hosts. A generous ceiling — the exec timeout plus a minute — since a real scan can
	// legitimately run that long; it's a failsafe, not the normal completion path.
	watchdogMs := a.cfg.ExecTimeout.Milliseconds() + 60000
	a.renderNext(w, "dashboard.html", map[string]any{
		"Summary": summary, "AllHosts": views, "HasTags": hasTags,
		"BulkConcurrency": a.cfg.BulkConcurrency, "ApplyStagger": a.cfg.ApplyStaggerSeconds,
		"WatchdogMs": watchdogMs,
	})
}

func (a *app) nextDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	v, snap, err := a.nextHostView(id)
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	host, _ := db.GetHost(a.db, id)
	prefs, _ := db.GetHostNotificationPrefs(a.db, id)
	a.renderNext(w, "detail.html", map[string]any{"V": v, "Snap": snap, "Host": host, "Prefs": prefs})
}

func (a *app) nextCard(w http.ResponseWriter, r *http.Request) {
	v, _, err := a.nextHostView(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	a.renderNext(w, "card", v)
}

func (a *app) nextScanPanel(w http.ResponseWriter, r *http.Request) {
	h, err := db.GetHost(a.db, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	a.renderNext(w, "scanpanel", map[string]string{"ID": h.ID, "Name": h.Name, "Ctx": r.URL.Query().Get("ctx")})
}

func (a *app) nextDot(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	checker := sshx.NewClient(a.cfg.ConnectivityTimeout, a.cfg.ConnectivityTimeout, a.verifyHostKey)
	st := a.probeConnectivity(checker, id)
	a.renderNext(w, "dot", map[string]any{"HostID": id, "Connected": st.Connected})
}

// nextConnLine is the detail-screen connectivity status: dot + reachable/unreachable text
// (with the failure reason) + live uptime + a manual recheck button.
func (a *app) nextConnLine(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	checker := sshx.NewClient(a.cfg.ConnectivityTimeout, a.cfg.ConnectivityTimeout, a.verifyHostKey)
	st := a.probeConnectivity(checker, id)
	a.renderNext(w, "connline", map[string]any{
		"ID": id, "Connected": st.Connected, "Reason": st.Error, "Uptime": humanUptime(st.UptimeSeconds),
	})
}

// humanUptime renders a compact uptime like "3d 4h", "5h 12m", or "8m" (empty when unknown).
func humanUptime(sec int64) string {
	if sec <= 0 {
		return ""
	}
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

// nextScanStream runs a live scan and streams it as HTML fragments over SSE — the
// server-rendered analogue of scanHostStream (which emits JSON for the SPA). It reuses the
// same backend scan and persists the result, so the card refresh the browser fires on the
// terminal `done` event reads fresh data. Closing the EventSource aborts the scan (ctx).
func (a *app) nextScanStream(w http.ResponseWriter, r *http.Request) {
	host, err := db.GetHost(a.db, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	line := func(class, text string) {
		fmt.Fprintf(w, "event: line\ndata: <div class=\"ln %s\">%s</div>\n\n", class, html.EscapeString(text))
		flusher.Flush()
	}
	done := func() {
		fmt.Fprint(w, "event: done\ndata: {}\n\n")
		flusher.Flush()
	}

	line("", "Connecting to "+host.Name+"…")
	res, err := a.sshClient.ScanHostStreaming(r.Context(), host, a.secrets, func(l string) { line("", l) })
	if err != nil {
		if r.Context().Err() != nil {
			return // client disconnected — scan aborted on purpose, record nothing
		}
		line("err", "Scan failed: "+err.Error())
		done()
		return
	}
	_ = db.UpsertScanResult(a.db, host.ID, res)
	_ = db.RecordActivity(a.db, host.ID, host.Name, "scan_ok", fmt.Sprintf("Scan completed: %d packages available", len(res.Packages)))
	line("ok", fmt.Sprintf("Scan complete — %d update(s) available.", len(res.Packages)))
	done()
}

// nextApplyConfirm renders the "are you sure?" step — applying is destructive.
func (a *app) nextApplyConfirm(w http.ResponseWriter, r *http.Request) {
	v, _, err := a.nextHostView(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	a.renderNext(w, "applyconfirm", map[string]any{
		"ID": v.ID, "Name": v.Name, "Count": v.UpdateCount, "Ctx": r.URL.Query().Get("ctx"),
	})
}

func (a *app) nextApplyPanel(w http.ResponseWriter, r *http.Request) {
	h, err := db.GetHost(a.db, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	a.renderNext(w, "applypanel", map[string]string{"ID": h.ID, "Name": h.Name, "Ctx": r.URL.Query().Get("ctx")})
}

// nextApplyStream runs apt upgrade and streams it as HTML fragments, then re-scans so the
// card/detail reflects the new (lower) update count. Reuses the same backend apply as the
// SPA; apply runs on a background context (aborting mid-configure risks a half-configured
// dpkg), and a dropped connection is reported as "likely restarting" rather than a failure.
func (a *app) nextApplyStream(w http.ResponseWriter, r *http.Request) {
	host, err := db.GetHost(a.db, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	line := func(class, text string) {
		fmt.Fprintf(w, "event: line\ndata: <div class=\"ln %s\">%s</div>\n\n", class, html.EscapeString(text))
		flusher.Flush()
	}
	done := func() {
		fmt.Fprint(w, "event: done\ndata: {}\n\n")
		flusher.Flush()
	}

	line("", "Applying updates on "+host.Name+"…")
	res, err := a.sshClient.ApplyUpdatesStreaming(context.Background(), host, a.secrets, func(l string) { line("", l) })
	if err != nil {
		var interrupted *sshx.ApplyInterruptedError
		if errors.As(err, &interrupted) {
			_ = db.RecordActivity(a.db, host.ID, host.Name, "apply_interrupted", fmt.Sprintf("Connection lost during apply (%d changed) — host likely restarting", interrupted.ChangedSoFar))
			line("ok", "Connection dropped mid-apply — the host may be restarting a service or rebooting. Re-scan to verify.")
		} else {
			_ = db.RecordActivity(a.db, host.ID, host.Name, "apply_fail", fmt.Sprintf("Apply failed: %v", err))
			line("err", "Apply failed: "+err.Error())
		}
		done()
		return
	}
	_ = db.RecordActivity(a.db, host.ID, host.Name, "apply_ok", fmt.Sprintf("Applied updates: %d packages changed", res.ChangedPackages))
	line("ok", fmt.Sprintf("Applied — %d package(s) changed. Re-scanning…", res.ChangedPackages))
	if sres, serr := a.sshClient.ScanHostStreaming(context.Background(), host, a.secrets, func(string) {}); serr == nil {
		_ = db.UpsertScanResult(a.db, host.ID, sres)
		line("ok", fmt.Sprintf("Now %d update(s) remaining.", len(sres.Packages)))
	}
	done()
}
