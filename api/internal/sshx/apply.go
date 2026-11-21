package sshx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"patchdeck/api/internal/crypto"
	"patchdeck/api/internal/models"
)

// Detached apply. The upgrade used to run as a plain child of the SSH session, so anything that
// cut the session — a tailscale/network restart (a package's own postinst, or the bastion host
// patching itself), a Warpgate hiccup, the exec timeout — left Patchdeck blind to the result, and
// could kill apt partway. Now the upgrade runs in its own session on the host (setsid, output to a
// log file, stdin from /dev/null) and Patchdeck only FOLLOWS that log. If the follow drops, it
// reconnects and resumes from the next unseen line until the upgrade's exit status is known, so
// the recorded result is what actually happened on the host.

const (
	// applyDir holds each run's log / pid / exit status on the host (root-only; 7-day cleanup).
	applyDir = "/var/lib/patchdeck"
	// applyMaxDuration caps the whole apply, however many reattaches it takes.
	applyMaxDuration = 2 * time.Hour
)

var (
	// applyReconnectWindow: how long to keep trying to reach the host again after a drop.
	applyReconnectWindow = 5 * time.Minute
	// applyRetryDelay is the pause between reattach attempts. Both are vars so tests can shorten them.
	applyRetryDelay = 3 * time.Second
)

func newApplyID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b)
}

var applyIDRe = regexp.MustCompile(`^[0-9A-Za-z-]+$`)

// applyStartScript launches the upgrade detached and records its pid, then follows it.
// Steps: finish any earlier interrupted dpkg run, refresh package lists (so the apply sees what's
// current, not what the last scan saw), then dist-upgrade keeping existing config files on
// conflicts (no prompt can hang or fail a non-interactive run). The exit status is written
// atomically to <run>.rc when apt-get finishes.
func applyStartScript(id string) string {
	return fmt.Sprintf(`D=%s; L="$D/apply-%s"
mkdir -p "$D" && chmod 700 "$D"
find "$D" -maxdepth 1 -name 'apply-*' -mtime +7 -exec rm -f {} + 2>/dev/null

%s

if [ -z "$PM" ]; then
	echo "__PD_UNSUPPORTED_PM__"
	exit 1
fi

nohup setsid sh -c '
echo $$ > "$0.pid"
export LANG=C LC_ALL=C DEBIAN_FRONTEND=noninteractive
dpkg --configure -a
apt-get update || echo __APT_UPDATE_FAILED__
S=$(%[3]s) && printf "%%s\n" "$S" | cut -d" " -f1 | sort -u > "$0.stopped0"
apt-get -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold dist-upgrade
R=$?
if [ -f "$0.stopped0" ] && grep -q "^Setting up " "$0.log"; then
  echo "%[5]s"
  sleep %[4]d
  S=$(%[3]s) && printf "%%s\n" "$S" | cut -d" " -f1 | sort -u > "$0.stopped1" && comm -13 "$0.stopped0" "$0.stopped1" > "$0.newstopped"
fi
echo $R > "$0.rc.tmp" && mv "$0.rc.tmp" "$0.rc"' "$L" > "$L.log" 2>&1 < /dev/null &
`, applyDir, id, applyListStopped, StoppedCheckSecs(), applyCheckNote()) + applyFollowBody(1)
}

// applyListStopped is stoppedUnitsCmd's systemctl call, written for the inside of the apply's
// single-quoted sh -c (no single quotes there). Its exit status decides whether the check runs:
// if systemctl can't answer before the upgrade (no systemd, no bus), there's no check at all.
const applyListStopped = `systemctl list-units --state=failed,auto-restart --plain --no-legend --no-pager 2>/dev/null`

// applyCheckNote is the progress line the apply log shows while it waits (kept out of error snippets).
func applyCheckNote() string {
	return fmt.Sprintf("Checking that services are still running (%d s)...", StoppedCheckSecs())
}

// StoppedCheckSecs is the check delay in whole seconds, for messages.
func StoppedCheckSecs() int { return int(stoppedCheckDelay.Seconds()) }

// stoppedCheckDelay: how long after an apply or a restart Patchdeck waits before checking that
// services still run. nova 2026-11-13: unattended-upgrades died 22 s after a restart that worked.
// A var so tests can shorten it.
var stoppedCheckDelay = 30 * time.Second

// StoppedCheckDelay is stoppedCheckDelay for callers outside this package (the restart check).
func StoppedCheckDelay() time.Duration { return stoppedCheckDelay }

// applyFollowScript re-attaches to a running (or finished) upgrade, resuming at line `from`.
func applyFollowScript(id string, from int) string {
	return fmt.Sprintf(`D=%s; L="$D/apply-%s"
`, applyDir, id) + applyFollowBody(from)
}

// applyFollowBody streams the log from line `from` until the upgrade's shell exits (tail --pid),
// then reports the exit status, the number of packages set up (counted over the WHOLE log, so a
// reattach can't skew it) and the reboot flag. __PD_APPLY_NOTFOUND__ = it never started;
// __PD_APPLY_LOST__ = it stopped without an exit status (e.g. the host rebooted mid-run).
func applyFollowBody(from int) string {
	return fmt.Sprintf(`i=0; while [ ! -s "$L.pid" ] && [ ! -f "$L.rc" ] && [ $i -lt 40 ]; do sleep 0.25; i=$((i+1)); done
if [ ! -f "$L.log" ]; then echo __PD_APPLY_NOTFOUND__; exit 0; fi
P=$(cat "$L.pid" 2>/dev/null)
if [ -n "$P" ]; then tail -n +%d --pid="$P" -f "$L.log"; else tail -n +%d "$L.log"; fi
i=0; while [ ! -f "$L.rc" ] && [ $i -lt 8 ]; do sleep 0.25; i=$((i+1)); done
if [ -f "$L.rc" ]; then
  echo "__PD_APPLY_RC__=$(cat "$L.rc")"
  echo "__PD_SETUP_COUNT__=$(grep -c '^Setting up ' "$L.log")"
  test -s "$L.newstopped" && echo "__PD_NEWSTOPPED__=$(tr '\n' ' ' < "$L.newstopped")" || true
  test -f /var/run/reboot-required && echo __REBOOT__ || true
  command -v needrestart >/dev/null 2>&1 && needrestart -b 2>/dev/null | grep -Eq '^NEEDRESTART-(KSTA: [23]|UCSTA: 2)' && echo __REBOOT__ || true
else echo __PD_APPLY_LOST__; fi
`, from, from)
}

// ErrApplyNotStarted: a reattach found no trace of the upgrade on the host — the Patchdeck that
// recorded its id stopped before the start script got there. Nothing ran, so it's safe to run again.
var ErrApplyNotStarted = errors.New("the upgrade never started on the host")

var (
	applyRCRe    = regexp.MustCompile(`(?m)^__PD_APPLY_RC__=(\d+)\s*$`)
	applySetupRe = regexp.MustCompile(`(?m)^__PD_SETUP_COUNT__=(\d+)\s*$`)
	// applyStoppedRe: units that were running before the apply and stopped after it.
	applyStoppedRe = regexp.MustCompile(`(?m)^__PD_NEWSTOPPED__=(.*)$`)
)

// retryableApplyErr: the follow session itself broke (connection lost, exec timeout, can't
// reconnect) — the upgrade may still be running, so reattach rather than give up.
func retryableApplyErr(err error) bool {
	if errors.Is(err, ErrConnectionLost) {
		return true
	}
	msg := err.Error()
	for _, s := range []string{"timed out", "Connection refused", "unreachable", "Host not found", "SSH session error", "SSH handshake failed", "Connection reset", "SSH error: EOF"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// applyDetached runs the upgrade detached on the host and follows it to completion, reattaching
// across dropped connections. onLine (may be nil) receives the upgrade's output lines plus a note
// when a reattach happens.
func (c *Client) applyDetached(ctx context.Context, host models.Host, seal *crypto.SealBox, onLine func(string)) (models.ApplyResult, error) {
	return c.applyTracked(ctx, host, seal, nil, onLine)
}

// ApplyUpdatesTracked is ApplyUpdatesStreaming that first hands the run's id to onID (before the
// upgrade starts), so a caller can record it and reattach with ReattachApply after its OWN restart.
func (c *Client) ApplyUpdatesTracked(ctx context.Context, host models.Host, seal *crypto.SealBox, onID func(id string), onLine func(string)) (models.ApplyResult, error) {
	return c.applyTracked(ctx, host, seal, onID, onLine)
}

// ReattachApply follows an upgrade started earlier (by id) to completion, replaying its log from the
// first line. Used when Patchdeck restarted mid-apply: the upgrade kept running on the host.
func (c *Client) ReattachApply(ctx context.Context, host models.Host, seal *crypto.SealBox, id string, onLine func(string)) (models.ApplyResult, error) {
	if !applyIDRe.MatchString(id) {
		return models.ApplyResult{}, fmt.Errorf("bad apply id")
	}
	return c.followApply(ctx, host, seal, id, applyFollowScript(id, 1), true, onLine)
}

func (c *Client) applyTracked(ctx context.Context, host models.Host, seal *crypto.SealBox, onID func(string), onLine func(string)) (models.ApplyResult, error) {
	id := newApplyID()
	if !applyIDRe.MatchString(id) { // defensive: the id is interpolated into a shell script
		return models.ApplyResult{}, fmt.Errorf("bad apply id")
	}
	if onID != nil {
		onID(id)
	}
	return c.followApply(ctx, host, seal, id, applyStartScript(id), false, onLine)
}

// followApply runs script (the start script, or a follow script when reattaching) and keeps
// following the upgrade until its exit status is known. reattach=true means the upgrade was started
// by an earlier Patchdeck process, so "not found" is an interruption, not a failure to start.
func (c *Client) followApply(ctx context.Context, host models.Host, seal *crypto.SealBox, id, script string, reattach bool, onLine func(string)) (models.ApplyResult, error) {
	var logBuf strings.Builder
	seen := 0
	emit := func(l string) {
		if strings.HasPrefix(l, "__PD_") || l == "__REBOOT__" {
			return
		}
		seen++
		logBuf.WriteString(l)
		logBuf.WriteByte('\n')
		if onLine != nil {
			onLine(l)
		}
	}
	note := func(s string) {
		if onLine != nil {
			onLine(s)
		}
	}

	started := time.Now()
	var dropSince time.Time
	for attempt := 0; ; attempt++ {
		seenBefore := seen
		out, err := c.runPrivilegedStreaming(ctx, host, seal, script, emit)
		if seen > seenBefore {
			dropSince = time.Time{} // the link worked for a while: a fresh drop gets a fresh window
		}
		switch {
		case strings.Contains(out, "__PD_UNSUPPORTED_PM__"):
			return models.ApplyResult{}, errors.New(
				"unsupported operating system: no supported package manager (apt, dnf or yum) was detected",
			)
		case applyRCRe.MatchString(out):
			return finishApply(out, logBuf.String())
		case strings.Contains(out, "__PD_APPLY_LOST__"):
			return models.ApplyResult{}, &ApplyInterruptedError{
				Underlying:   errors.New("the upgrade stopped on the host without an exit status (host rebooted or apt was killed)"),
				ChangedSoFar: countChangedPackages(logBuf.String()),
				DpkgStarted:  true,
			}
		case strings.Contains(out, "__PD_APPLY_NOTFOUND__"):
			if attempt == 0 && !reattach {
				return models.ApplyResult{}, errors.New("the upgrade could not be started on the host")
			}
			if attempt == 0 && reattach {
				return models.ApplyResult{}, ErrApplyNotStarted
			}
			return models.ApplyResult{}, &ApplyInterruptedError{Underlying: errors.New("the upgrade's log is gone from the host")}
		}
		if err == nil {
			err = errors.New("the upgrade's output ended without a result")
		}
		// Before the upgrade started (auth, host key, escalation…) there is nothing to reattach to.
		if attempt == 0 && seen == 0 && !reattach && !retryableApplyErr(err) {
			return models.ApplyResult{}, err
		}
		if ctx.Err() != nil {
			return models.ApplyResult{}, ctx.Err()
		}
		if !retryableApplyErr(err) && attempt > 0 {
			return models.ApplyResult{}, err
		}
		if dropSince.IsZero() {
			dropSince = time.Now()
			note("— connection to the host dropped; the upgrade keeps running there. Reattaching…")
		}
		if time.Since(dropSince) > applyReconnectWindow || time.Since(started) > applyMaxDuration {
			return models.ApplyResult{}, &ApplyInterruptedError{
				Underlying:   fmt.Errorf("couldn't reattach to the running upgrade: %w", err),
				ChangedSoFar: countChangedPackages(logBuf.String()),
				DpkgStarted:  seen > 0,
			}
		}
		select {
		case <-ctx.Done():
			return models.ApplyResult{}, ctx.Err()
		case <-time.After(applyRetryDelay):
		}
		script = applyFollowScript(id, seen+1)
	}
}

func countChangedPackages(log string) int {
	count := 0

	for _, line := range strings.Split(log, "\n") {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "Setting up ") ||
			strings.HasPrefix(line, "Upgraded:") ||
			strings.HasPrefix(line, "Installed:") ||
			strings.HasPrefix(line, "Updated:") {
			count++
		}
	}

	return count
}

// finishApply turns the follow's final output into the result: exit status, packages set up
// (counted on the host over the whole log), reboot flag.
func finishApply(out, log string) (models.ApplyResult, error) {
	rc, _ := strconv.Atoi(applyRCRe.FindStringSubmatch(out)[1])
	changed := strings.Count(log, "Setting up ")
	if m := applySetupRe.FindStringSubmatch(out); m != nil {
		changed, _ = strconv.Atoi(m[1])
	}
	res := models.ApplyResult{ChangedPackages: changed, RawOutput: log, NeedsReboot: strings.Contains(out, "__REBOOT__")}
	if m := applyStoppedRe.FindStringSubmatch(out); m != nil {
		res.StoppedServices = OnlyServices(strings.Fields(m[1]))
	}
	if rc != 0 {
		snippet := lastLines(strings.ReplaceAll(log, applyCheckNote()+"\n", ""), 3)
		if snippet == "" {
			snippet = "no output"
		}
		return res, fmt.Errorf("package upgrade failed (exit %d): %s", rc, snippet)
	}
	return res, nil
}
