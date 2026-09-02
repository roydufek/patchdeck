// Package restartpolicy is the single source of truth for how a service-needing-restart is
// treated: whether it's the D-Bus bus (special-cased), whether restarting it is session-risky,
// whether it's disruptive enough to warn about, and — given the host's needrestart handlers and
// the learned "restarted-but-still-flagged" set — which units a smart restart can act on vs which
// genuinely need a reboot. Both the SSH client (which runs the restarts) and the scanner/db
// (which classifies a snapshot) import this so they can never drift apart.
package restartpolicy

import "strings"

func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// IsDbusFamily reports whether the unit is the D-Bus bus itself. These are the ONLY units for
// which "no needrestart handler" must mean "reboot" rather than a detached restart — a naive
// restart of the bus severs it and strands its clients (logind, etc.) → SSH lockout.
func IsDbusFamily(svc string) bool {
	switch norm(svc) {
	case "dbus", "dbus.service", "dbus.socket", "dbus-broker", "dbus-broker.service":
		return true
	}
	return false
}

// IsRisky reports whether a plain `systemctl restart` over SSH is destructive to the session
// (the bus + the login-session manager). These must go through needrestart's coordinated
// handler or a detached restart — never a naive live restart.
func IsRisky(svc string) bool {
	if IsDbusFamily(svc) {
		return true
	}
	switch norm(svc) {
	case "systemd-logind", "systemd-logind.service":
		return true
	}
	return false
}

// IsDisruptive reports whether restarting the unit is likely to drop the operator's SSH session
// or bounce running workloads (containers, the network). Used only to warn in the confirm step —
// it does not change what a restart does, just what the user is told to expect.
func IsDisruptive(svc string) bool {
	if IsRisky(svc) {
		return true
	}
	switch norm(svc) {
	case "docker", "docker.service", "containerd", "containerd.service",
		"ssh", "ssh.service", "sshd", "sshd.service",
		"systemd-networkd", "systemd-networkd.service",
		"networkmanager", "networkmanager.service",
		"networking", "networking.service",
		"systemd-resolved", "systemd-resolved.service",
		"tailscaled", "tailscaled.service", "wg-quick@wg0.service":
		return true
	}
	return false
}

// Classify splits a host's needs-restart list into the units a smart "Restart services" can act
// on (restartable) versus the units that genuinely need a reboot (rebootOnly). A unit is
// reboot-only when it's learned-resistant (we restarted it and it came back on the same boot), or
// it's the D-Bus bus with no coordinated handler on this host. Inputs:
//   - handlers[svc]:  the host has /etc/needrestart/restart.d/<svc> (a coordinated handler)
//   - resistant[svc]: it was restarted but is still flagged on the current boot
func Classify(needsRestart []string, handlers, resistant map[string]bool) (restartable, rebootOnly []string) {
	for _, s := range needsRestart {
		switch {
		case resistant[s]:
			rebootOnly = append(rebootOnly, s)
		case IsDbusFamily(s) && !handlers[s]:
			rebootOnly = append(rebootOnly, s)
		default:
			restartable = append(restartable, s)
		}
	}
	return
}
