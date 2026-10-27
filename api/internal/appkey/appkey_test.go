package appkey

import (
	"database/sql"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	_ "modernc.org/sqlite"

	"patchdeck/api/internal/crypto"
	"patchdeck/api/internal/db"
)

func testDB(t *testing.T) (*sql.DB, *crypto.SealBox) {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	seal, err := crypto.NewSealBox(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	return conn, seal
}

func TestOpenGeneratesOnceAndPersists(t *testing.T) {
	conn, seal := testDB(t)
	s, generated, err := Open(conn, seal)
	if err != nil || !generated {
		t.Fatalf("first open: generated=%v err=%v", generated, err)
	}
	cur := s.Current()
	if !strings.HasPrefix(cur.AuthorizedKey, "ssh-ed25519 AAAA") || !strings.HasSuffix(cur.AuthorizedKey, " "+cur.Comment()) ||
		!strings.HasPrefix(cur.Fingerprint, "SHA256:") || !strings.HasPrefix(cur.Comment(), "patchdeck-") {
		t.Fatalf("key: %+v", cur)
	}
	// The private key is stored encrypted, never as plaintext.
	st, _ := db.GetAppKeyState(conn)
	if st.CurrentCipher == "" || strings.Contains(st.CurrentCipher, "PRIVATE KEY") {
		t.Fatalf("stored cipher looks wrong: %.40q", st.CurrentCipher)
	}
	s2, generated, err := Open(conn, seal)
	if err != nil || generated || s2.Current().Fingerprint != cur.Fingerprint {
		t.Fatalf("reopen: generated=%v err=%v fp=%s want %s", generated, err, s2.Current().Fingerprint, cur.Fingerprint)
	}
	if !s2.Current().Created.Equal(cur.Created) {
		t.Fatalf("created %v != %v", s2.Current().Created, cur.Created)
	}
}

func TestRotationFinish(t *testing.T) {
	conn, seal := testDB(t)
	s, _, _ := Open(conn, seal)
	old := s.Current()
	if len(s.Signers()) != 1 {
		t.Fatal("one signer before rotation")
	}
	if _, err := s.FinishRotation(); err != ErrNotRotating {
		t.Fatalf("finish without rotation: %v", err)
	}
	next, err := s.StartRotation()
	if err != nil || next.Fingerprint == old.Fingerprint {
		t.Fatalf("start: %v", err)
	}
	if _, err := s.StartRotation(); err != ErrRotating {
		t.Fatalf("double start: %v", err)
	}
	sg := s.Signers()
	if len(sg) != 2 || !samePub(sg[0], old) || !samePub(sg[1], next) {
		t.Fatal("mid-rotation: offer current then next")
	}
	// A restart mid-rotation keeps both keys.
	s2, _, _ := Open(conn, seal)
	if n, ok := s2.Next(); !ok || n.Fingerprint != next.Fingerprint {
		t.Fatal("rotation state not persisted")
	}
	retired, err := s.FinishRotation()
	if err != nil || retired.Fingerprint != old.Fingerprint || s.Current().Fingerprint != next.Fingerprint {
		t.Fatalf("finish: err=%v", err)
	}
	if _, ok := s.Next(); ok || len(s.Signers()) != 1 {
		t.Fatal("after finish only the new key remains")
	}
	s3, _, _ := Open(conn, seal)
	if s3.Current().Fingerprint != next.Fingerprint {
		t.Fatal("finished rotation not persisted")
	}
	if _, ok := s3.Next(); ok {
		t.Fatal("next key should be gone on disk")
	}
}

func TestRotationCancel(t *testing.T) {
	conn, seal := testDB(t)
	s, _, _ := Open(conn, seal)
	old := s.Current()
	next, _ := s.StartRotation()
	dropped, err := s.CancelRotation()
	if err != nil || dropped.Fingerprint != next.Fingerprint || s.Current().Fingerprint != old.Fingerprint {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := s.CancelRotation(); err != ErrNotRotating {
		t.Fatalf("double cancel: %v", err)
	}
	s2, _, _ := Open(conn, seal)
	if _, ok := s2.Next(); ok || s2.Current().Fingerprint != old.Fingerprint {
		t.Fatal("cancel not persisted")
	}
}

func samePub(s ssh.Signer, k Key) bool { return ssh.FingerprintSHA256(s.PublicKey()) == k.Fingerprint }
