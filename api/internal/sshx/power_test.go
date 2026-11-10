package sshx

import (
	"errors"
	"testing"
)

// TestPowerNeverSent: errors from before the command could run are reported (a reboot would
// otherwise wait 15 minutes for a host that never went down); a dropped connection is not.
func TestPowerNeverSent(t *testing.T) {
	for _, e := range []error{
		errors.New("privilege escalation failed: sudo -n, sudo -S, and su fallback all failed"),
		errors.New("ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey]"),
		&HostKeyError{Message: "host key changed"},
	} {
		if !powerNeverSent(e) {
			t.Errorf("should be reported: %v", e)
		}
	}
	for _, e := range []error{ErrConnectionLost, errors.New("SSH error: EOF"), errors.New("wait: remote command exited without exit status or exit signal")} {
		if powerNeverSent(e) {
			t.Errorf("a dropped connection is the reboot working, not an error: %v", e)
		}
	}
}
