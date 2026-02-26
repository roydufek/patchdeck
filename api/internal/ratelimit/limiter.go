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
