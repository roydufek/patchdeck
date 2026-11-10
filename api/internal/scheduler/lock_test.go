package scheduler

import (
	"sync"
	"testing"
	"time"
)

type fakeLocker struct {
	mu   sync.Mutex
	busy map[string]bool
}

func (f *fakeLocker) TryLock(id, _ string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy[id] {
		return false
	}
	f.busy[id] = true
	return true
}

func (f *fakeLocker) Unlock(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.busy, id)
}

// TestWaitLock: a scheduled job waits for a host a manual action holds, rather than skipping it,
// and gives up (reported, not silent) only after schedLockWait.
func TestWaitLock(t *testing.T) {
	oldWait, oldPoll := schedLockWait, schedLockPoll
	schedLockWait, schedLockPoll = 300*time.Millisecond, 10*time.Millisecond
	defer func() { schedLockWait, schedLockPoll = oldWait, oldPoll }()

	l := &fakeLocker{busy: map[string]bool{"h1": true}}
	e := &Engine{locker: l}
	go func() { time.Sleep(50 * time.Millisecond); l.Unlock("h1") }()
	if !e.waitLock("h1", "Scheduled apply") {
		t.Fatal("should get the host once the manual action releases it")
	}
	if e.waitLock("h1", "Scheduled apply") {
		t.Fatal("should give up when the host stays busy past schedLockWait")
	}
	if !(&Engine{}).waitLock("h1", "x") {
		t.Fatal("no locker = no waiting")
	}
}
