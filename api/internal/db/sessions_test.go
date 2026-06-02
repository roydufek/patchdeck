package db

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	d.SetMaxOpenConns(1) // keep the single in-memory connection alive for the test
	if err := Migrate(d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}

func TestSessionLifecycle(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	const ttl = time.Hour

	tok, err := CreateSession(d, "u1", "alice", "admin", ttl)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if tok == "" {
		t.Fatal("CreateSession returned empty token")
	}

	uid, un, role, ok, err := ValidateSession(d, tok, ttl)
	if err != nil || !ok {
		t.Fatalf("validate: ok=%v err=%v", ok, err)
	}
	if uid != "u1" || un != "alice" || role != "admin" {
		t.Fatalf("claims wrong: uid=%q user=%q role=%q", uid, un, role)
	}

	if _, _, _, ok, _ := ValidateSession(d, "not-a-real-token", ttl); ok {
		t.Fatal("a bogus token validated")
	}

	if err := DeleteSession(d, tok); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, _, ok, _ := ValidateSession(d, tok, ttl); ok {
		t.Fatal("deleted session still validates")
	}
}

func TestSessionExpiry(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	// Negative TTL -> already expired at creation.
	tok, err := CreateSession(d, "u1", "alice", "admin", -time.Minute)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, _, ok, _ := ValidateSession(d, tok, time.Hour); ok {
		t.Fatal("expired session validated")
	}
}
