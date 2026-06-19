package ratelimit

import (
	"testing"
	"time"
)

func TestHostLimiterPerOperation(t *testing.T) {
	l := NewHostLimiter(50 * time.Millisecond)
	// A repeat of the SAME operation on a host within the cooldown is blocked (debounce).
	if ok, _ := l.Allow("h1:apply"); !ok {
		t.Fatal("first apply should be allowed")
	}
	if ok, retry := l.Allow("h1:apply"); ok || retry <= 0 {
		t.Fatalf("immediate repeat of the same op should be rate-limited: ok=%v retry=%d", ok, retry)
	}
	// A DIFFERENT operation on the same host is independent — this is what lets apply, its
	// follow-up scan, and a restart run back-to-back without blocking each other.
	if ok, _ := l.Allow("h1:scan"); !ok {
		t.Fatal("scan must not be blocked by a recent apply on the same host")
	}
	if ok, _ := l.Allow("h1:restart"); !ok {
		t.Fatal("restart must not be blocked by a recent apply on the same host")
	}
	// A different host is independent.
	if ok, _ := l.Allow("h2:apply"); !ok {
		t.Fatal("a different host must be independent")
	}
	// After the cooldown elapses the same op is allowed again.
	time.Sleep(60 * time.Millisecond)
	if ok, _ := l.Allow("h1:apply"); !ok {
		t.Fatal("same op should be allowed again after the cooldown")
	}
}

func TestLoginLimiterLockout(t *testing.T) {
	l := NewLoginLimiter(3, time.Hour)
	key := "1.2.3.4"
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allowed(key); !ok {
			t.Fatalf("attempt %d should be allowed before lockout", i)
		}
		l.RecordFailure(key)
	}
	if ok, retry := l.Allowed(key); ok || retry <= 0 {
		t.Fatalf("should be locked out after maxFailures: ok=%v retry=%d", ok, retry)
	}
	l.Reset(key)
	if ok, _ := l.Allowed(key); !ok {
		t.Fatal("should be allowed again after Reset")
	}
}

func TestLoginLimiterWindowExpiry(t *testing.T) {
	l := NewLoginLimiter(2, 10*time.Millisecond)
	key := "k"
	l.RecordFailure(key)
	l.RecordFailure(key)
	if ok, _ := l.Allowed(key); ok {
		t.Fatal("should be locked immediately after reaching maxFailures")
	}
	time.Sleep(20 * time.Millisecond)
	if ok, _ := l.Allowed(key); !ok {
		t.Fatal("should unlock after the window elapses")
	}
}

func TestLoginLimiterIsolatesKeys(t *testing.T) {
	l := NewLoginLimiter(1, time.Hour)
	l.RecordFailure("a")
	if ok, _ := l.Allowed("a"); ok {
		t.Fatal("key a should be locked")
	}
	if ok, _ := l.Allowed("b"); !ok {
		t.Fatal("key b should be unaffected by key a")
	}
}
