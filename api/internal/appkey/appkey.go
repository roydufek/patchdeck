// Package appkey owns Patchdeck's own SSH keypair: an Ed25519 key generated on first start,
// stored encrypted with the master key, and offered to hosts whose auth type is "app_key".
//
// Rotation is two-phase so no host is locked out: StartRotation creates a NEXT key and from
// then on Signers() offers both (current first), so a host works whichever public key it
// trusts; once the new public key is installed everywhere, FinishRotation promotes it and
// destroys the old private key. CancelRotation drops the next key instead.
package appkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"patchdeck/api/internal/crypto"
	"patchdeck/api/internal/db"
	"patchdeck/api/internal/models"
)

var (
	ErrRotating    = errors.New("a key rotation is already in progress")
	ErrNotRotating = errors.New("no key rotation is in progress")
)

// Key is one keypair as the rest of the app sees it. The private half lives only inside Signer.
type Key struct {
	Signer        ssh.Signer
	AuthorizedKey string // "ssh-ed25519 AAAA… patchdeck-20261026T153012Z", ready for authorized_keys
	Fingerprint   string // "SHA256:…"
	Created       time.Time
}

// Comment is the key's authorized_keys comment, derived from its creation time so each
// generation is recognisable (and removable) in authorized_keys or a Warpgate user.
func (k Key) Comment() string { return comment(k.Created) }

func comment(t time.Time) string { return "patchdeck-" + t.UTC().Format("20060102T150405Z") }

// Store holds the current key and, mid-rotation, the next one.
type Store struct {
	db   *sql.DB
	seal *crypto.SealBox

	mu   sync.RWMutex
	cur  Key
	next *Key
}

// Open loads the keypair, generating and persisting the first one if none exists yet.
// generated reports whether that happened (on first start, or the first start after upgrade).
func Open(conn *sql.DB, seal *crypto.SealBox) (s *Store, generated bool, err error) {
	s = &Store{db: conn, seal: seal}
	st, err := db.GetAppKeyState(conn)
	if err != nil {
		return nil, false, err
	}
	if st.CurrentCipher == "" {
		k, cipher, err := s.generate(time.Now())
		if err != nil {
			return nil, false, err
		}
		if err := db.PutAppKeyState(conn, models.AppKeyState{CurrentCipher: cipher, CurrentCreated: k.Created.Format(time.RFC3339)}); err != nil {
			return nil, false, err
		}
		s.cur = k
		return s, true, nil
	}
	if s.cur, err = s.load(st.CurrentCipher, st.CurrentCreated); err != nil {
		return nil, false, fmt.Errorf("load current SSH key: %w", err)
	}
	if st.NextCipher != "" {
		n, err := s.load(st.NextCipher, st.NextCreated)
		if err != nil {
			return nil, false, fmt.Errorf("load next SSH key: %w", err)
		}
		s.next = &n
	}
	return s, false, nil
}

func (s *Store) generate(now time.Time) (Key, string, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Key{}, "", err
	}
	created := now.UTC().Truncate(time.Second)
	block, err := ssh.MarshalPrivateKey(priv, comment(created))
	if err != nil {
		return Key{}, "", err
	}
	cipher, err := s.seal.Encrypt(pem.EncodeToMemory(block))
	if err != nil {
		return Key{}, "", err
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return Key{}, "", err
	}
	return keyOf(signer, created), cipher, nil
}

func (s *Store) load(cipher, created string) (Key, error) {
	plain, err := s.seal.Decrypt(cipher)
	if err != nil {
		return Key{}, err
	}
	signer, err := ssh.ParsePrivateKey(plain)
	if err != nil {
		return Key{}, err
	}
	t, err := time.Parse(time.RFC3339, created)
	if err != nil {
		t = time.Time{}
	}
	return keyOf(signer, t), nil
}

func keyOf(signer ssh.Signer, created time.Time) Key {
	pub := signer.PublicKey()
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))) + " " + comment(created)
	return Key{Signer: signer, AuthorizedKey: line, Fingerprint: ssh.FingerprintSHA256(pub), Created: created}
}

// Current returns the key hosts are expected to trust.
func (s *Store) Current() Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Next returns the rotation's new key, if a rotation is in progress.
func (s *Store) Next() (Key, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.next == nil {
		return Key{}, false
	}
	return *s.next, true
}

// Signers is what an "app_key" host is offered: the current key, plus the next one mid-rotation.
func (s *Store) Signers() []ssh.Signer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ssh.Signer{s.cur.Signer}
	if s.next != nil {
		out = append(out, s.next.Signer)
	}
	return out
}

func (s *Store) persist(cur Key, curCipher string, next *Key, nextCipher string) error {
	st := models.AppKeyState{CurrentCipher: curCipher, CurrentCreated: cur.Created.Format(time.RFC3339)}
	if next != nil {
		st.NextCipher, st.NextCreated = nextCipher, next.Created.Format(time.RFC3339)
	}
	return db.PutAppKeyState(s.db, st)
}

// StartRotation generates the next key. Until Finish or Cancel, both keys are offered.
func (s *Store) StartRotation() (Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next != nil {
		return Key{}, ErrRotating
	}
	st, err := db.GetAppKeyState(s.db)
	if err != nil {
		return Key{}, err
	}
	n, cipher, err := s.generate(time.Now())
	if err != nil {
		return Key{}, err
	}
	if err := s.persist(s.cur, st.CurrentCipher, &n, cipher); err != nil {
		return Key{}, err
	}
	s.next = &n
	return n, nil
}

// FinishRotation promotes the next key to current and destroys the old private key.
// It returns the retired key (public parts only matter) so callers can log what went away.
func (s *Store) FinishRotation() (retired Key, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next == nil {
		return Key{}, ErrNotRotating
	}
	st, err := db.GetAppKeyState(s.db)
	if err != nil {
		return Key{}, err
	}
	if err := s.persist(*s.next, st.NextCipher, nil, ""); err != nil {
		return Key{}, err
	}
	retired, s.cur, s.next = s.cur, *s.next, nil
	return retired, nil
}

// CancelRotation discards the next key; the current key stays.
func (s *Store) CancelRotation() (dropped Key, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next == nil {
		return Key{}, ErrNotRotating
	}
	st, err := db.GetAppKeyState(s.db)
	if err != nil {
		return Key{}, err
	}
	if err := s.persist(s.cur, st.CurrentCipher, nil, ""); err != nil {
		return Key{}, err
	}
	dropped, s.next = *s.next, nil
	return dropped, nil
}
