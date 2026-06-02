package ratelimit

import (
	"sync"
	"time"
)

// HostLimiter enforces per-host cooldowns for SSH operations.
type HostLimiter struct {
	cooldown time.Duration
	mu       sync.Mutex
	last     map[string]time.Time
}

// NewHostLimiter creates a rate limiter with the given per-host cooldown.
func NewHostLimiter(cooldown time.Duration) *HostLimiter {
	return &HostLimiter{cooldown: cooldown, last: make(map[string]time.Time)}
}

// Allow checks whether an operation on hostID is allowed. If not, it returns
// (false, retryAfterSeconds). If allowed it records the timestamp and returns (true, 0).
func (l *HostLimiter) Allow(hostID string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if last, ok := l.last[hostID]; ok {
		remaining := l.cooldown - now.Sub(last)
		if remaining > 0 {
			secs := int(remaining.Seconds()) + 1
			if secs < 1 {
				secs = 1
			}
			return false, secs
		}
	}
	l.last[hostID] = now
	return true, 0
}

// LoginLimiter throttles authentication attempts per key (e.g. client IP) using a
// sliding window: once maxFailures failures occur within the window, further attempts
// are blocked until the oldest failure ages out. A successful login clears the key.
type LoginLimiter struct {
	mu          sync.Mutex
	failures    map[string][]time.Time
	maxFailures int
	window      time.Duration
}

func NewLoginLimiter(maxFailures int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{failures: make(map[string][]time.Time), maxFailures: maxFailures, window: window}
}

// prune drops failures older than the window and stores the result. Caller holds mu.
func (l *LoginLimiter) prune(key string, now time.Time) []time.Time {
	cutoff := now.Add(-l.window)
	out := l.failures[key][:0:0]
	for _, t := range l.failures[key] {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		delete(l.failures, key)
	} else {
		l.failures[key] = out
	}
	return out
}

// Allowed reports whether a login attempt for key is currently allowed. If locked out,
// returns (false, retryAfterSeconds).
func (l *LoginLimiter) Allowed(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	times := l.prune(key, now)
	if len(times) >= l.maxFailures {
		remaining := l.window - now.Sub(times[0])
		secs := int(remaining.Seconds()) + 1
		if secs < 1 {
			secs = 1
		}
		return false, secs
	}
	return true, 0
}

// RecordFailure registers a failed attempt for key.
func (l *LoginLimiter) RecordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	times := l.prune(key, now)
	l.failures[key] = append(times, now)
}

// Reset clears the failure history for key (call on successful login).
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}
