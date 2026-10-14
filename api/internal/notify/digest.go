package notify

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Digest collects per-host outcomes — one scheduled run, or a burst of dashboard actions —
// so they go out as ONE message instead of a push per host. Callers add only the events
// the operator's notification toggles allow; the digest just formats what it is given.
type Digest struct {
	Pending  []PendingHost
	Applied  []AppliedHost
	Failed   []FailedHost
	UpToDate int
	// Scheduled marks a scheduled run: the title counts every host in it ("5 of 12") and
	// the body names the schedule.
	Scheduled bool
	Job       string
}

// PendingHost is a host a scan found with updates waiting.
type PendingHost struct {
	Name     string
	Packages int
	Security int
	New      bool // updates that weren't there at the host's previous scan
	Reboot   bool
	Restarts int // services flagged for a restart
}

// AppliedHost is a host whose updates were applied.
type AppliedHost struct {
	Name        string
	Changed     int
	Reboot      bool
	Interrupted bool // the connection dropped mid-apply (restart/reboot); almost certainly applied
}

// FailedHost is a host whose scan or apply failed.
type FailedHost struct {
	Name  string
	Phase string // "scan" | "apply"
	Err   string
}

// AddApplied records an apply. An apply that changed nothing counts as up to date, and a
// host that was pending earlier in the same digest is no longer listed as pending.
func (d *Digest) AddApplied(h AppliedHost) {
	kept := d.Pending[:0]
	for _, p := range d.Pending {
		if p.Name != h.Name {
			kept = append(kept, p)
		}
	}
	d.Pending = kept
	if h.Changed == 0 && !h.Interrupted {
		d.UpToDate++
		return
	}
	d.Applied = append(d.Applied, h)
}

// Notable reports whether the digest is worth a push: new updates, an apply, or a failure.
// Known, unchanged pending updates alone stay quiet (the operator already heard about them).
func (d Digest) Notable() bool {
	if len(d.Applied) > 0 || len(d.Failed) > 0 {
		return true
	}
	for _, p := range d.Pending {
		if p.New {
			return true
		}
	}
	return false
}

// Empty reports whether the digest holds no hosts at all.
func (d Digest) Empty() bool {
	return len(d.Pending)+len(d.Applied)+len(d.Failed)+d.UpToDate == 0
}

func (d Digest) total() int { return len(d.Pending) + len(d.Applied) + len(d.Failed) + d.UpToDate }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Title is Summary prefixed with "Patchdeck: " — for destinations where the title alone must
// say where the message came from (email subject, ntfy, webhooks…).
func (d Digest) Title() string { return "Patchdeck: " + d.Summary() }

// Summary is the one-line summary — what a phone's lock screen shows, so it carries the totals.
func (d Digest) Summary() string {
	var parts []string
	if n := len(d.Pending); n > 0 {
		pkgs, sec := 0, 0
		for _, p := range d.Pending {
			pkgs += p.Packages
			sec += p.Security
		}
		var who string
		switch {
		case d.Scheduled:
			who = fmt.Sprintf("%d of %d hosts need updates", n, d.total())
		case n == 1:
			who = d.Pending[0].Name + " needs updates"
		default:
			who = fmt.Sprintf("%d hosts need updates", n)
		}
		detail := plural(pkgs, "package", "packages")
		if sec > 0 {
			detail += fmt.Sprintf(", %d security", sec)
		}
		parts = append(parts, who+" ("+detail+")")
	}
	if n := len(d.Applied); n > 0 {
		changed := 0
		for _, a := range d.Applied {
			changed += a.Changed
		}
		who := fmt.Sprintf("updated %d hosts", n)
		if n == 1 {
			who = "updated " + d.Applied[0].Name
		}
		parts = append(parts, who+" ("+plural(changed, "package", "packages")+")")
	}
	reboots := 0
	for _, a := range d.Applied {
		if a.Reboot {
			reboots++
		}
	}
	for _, p := range d.Pending {
		if p.Reboot {
			reboots++
		}
	}
	if reboots > 0 {
		parts = append(parts, plural(reboots, "needs a reboot", "need a reboot"))
	}
	if n := len(d.Failed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", n))
	}
	if len(parts) == 0 {
		if d.Scheduled {
			if t := d.total(); t == 1 {
				parts = append(parts, "1 host up to date")
			} else {
				parts = append(parts, fmt.Sprintf("all %d hosts up to date", t))
			}
		} else {
			parts = append(parts, "nothing to report")
		}
	}
	s := strings.Join(parts, " · ")
	// Capitalise a leading word ("Updated 3 hosts…"), never a leading host name ("nova needs…").
	for _, w := range []string{"updated ", "all ", "nothing "} {
		if strings.HasPrefix(s, w) {
			return strings.ToUpper(s[:1]) + s[1:]
		}
	}
	return s
}

// maxBody keeps a large fleet's digest under the smallest common limit (Telegram: 4096).
const maxBody = 3500

// Body lists the hosts, most urgent section first. Plain text with "- " bullets, so it reads
// the same in every destination (Gotify, ntfy, Discord, email…).
func (d Digest) Body() string {
	var lines []string
	if len(d.Failed) > 0 {
		f := append([]FailedHost(nil), d.Failed...)
		sort.Slice(f, func(i, j int) bool { return f[i].Name < f[j].Name })
		lines = append(lines, "Failed:")
		for _, h := range f {
			line := fmt.Sprintf("- %s: %s failed", h.Name, h.Phase)
			if e := shortErr(h.Err); e != "" {
				line += " (" + e + ")"
			}
			lines = append(lines, line)
		}
	}
	if len(d.Applied) > 0 {
		a := append([]AppliedHost(nil), d.Applied...)
		sort.Slice(a, func(i, j int) bool {
			if a[i].Changed != a[j].Changed {
				return a[i].Changed > a[j].Changed
			}
			return a[i].Name < a[j].Name
		})
		lines = append(lines, "Updated:")
		for _, h := range a {
			line := "- " + h.Name + ": " + plural(h.Changed, "package", "packages")
			if h.Interrupted {
				line += " · connection lost mid-apply (likely restarting)"
			}
			if h.Reboot {
				line += " · reboot needed"
			}
			lines = append(lines, line)
		}
	}
	if len(d.Pending) > 0 {
		p := append([]PendingHost(nil), d.Pending...)
		sort.Slice(p, func(i, j int) bool {
			if p[i].Packages != p[j].Packages {
				return p[i].Packages > p[j].Packages
			}
			return p[i].Name < p[j].Name
		})
		lines = append(lines, "Needs updates:")
		for _, h := range p {
			line := "- " + h.Name + ": " + plural(h.Packages, "update", "updates")
			if h.Security > 0 {
				line += fmt.Sprintf(" (%d security)", h.Security)
			}
			if h.Reboot {
				line += " · reboot needed"
			} else if h.Restarts > 0 {
				line += " · " + plural(h.Restarts, "service", "services") + " to restart"
			}
			if h.New && d.Scheduled {
				line += " · new"
			}
			lines = append(lines, line)
		}
	}
	if d.UpToDate > 0 {
		lines = append(lines, "Up to date: "+plural(d.UpToDate, "host", "hosts"))
	}
	if d.Scheduled && strings.TrimSpace(d.Job) != "" {
		lines = append(lines, "Schedule: "+d.Job)
	}

	var b strings.Builder
	for i, l := range lines {
		if b.Len()+len(l)+1 > maxBody {
			fmt.Fprintf(&b, "… %d more lines", len(lines)-i)
			break
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l)
	}
	return b.String()
}

// shortErr keeps an error to its first line and a phone-friendly length.
func shortErr(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if r := []rune(s); len(r) > 140 {
		s = string(r[:139]) + "…"
	}
	return s
}

// Coalescer groups dashboard/API notifications. Each Add restarts a quiet timer; the batch
// goes out as one digest once nothing new has arrived for `quiet`, or `maxHold` after the
// batch's first event, so a steady trickle can't hold messages back forever. Batches live
// in memory only: a restart loses at most one unsent batch.
type Coalescer struct {
	quiet, maxHold time.Duration
	flush          func(Digest)

	mu    sync.Mutex
	cur   *Digest
	first time.Time
	gen   uint64
	timer *time.Timer
}

// NewCoalescer returns a coalescer that hands each finished batch to flush (on its own goroutine).
func NewCoalescer(quiet, maxHold time.Duration, flush func(Digest)) *Coalescer {
	return &Coalescer{quiet: quiet, maxHold: maxHold, flush: flush}
}

// Add records one event into the current batch.
func (c *Coalescer) Add(add func(*Digest)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cur == nil {
		c.cur = &Digest{}
		c.first = time.Now()
	}
	add(c.cur)
	wait := c.quiet
	if rem := c.maxHold - time.Since(c.first); rem < wait {
		wait = rem
	}
	if wait < 0 {
		wait = 0
	}
	if c.timer != nil {
		c.timer.Stop()
	}
	c.gen++
	gen := c.gen
	c.timer = time.AfterFunc(wait, func() { c.fire(gen) })
}

func (c *Coalescer) fire(gen uint64) {
	c.mu.Lock()
	if gen != c.gen || c.cur == nil { // superseded by a later Add (its own timer will flush)
		c.mu.Unlock()
		return
	}
	d := *c.cur
	c.cur, c.timer = nil, nil
	c.mu.Unlock()
	c.flush(d)
}
