package ratelimit

import (
	"testing"
	"time"
)

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
