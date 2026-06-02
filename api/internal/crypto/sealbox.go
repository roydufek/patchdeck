package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

type SealBox struct{ key []byte }

func NewSealBox(masterKey string) (*SealBox, error) {
	if len(masterKey) < 32 {
		return nil, fmt.Errorf("master key too short")
	}
	// Derive the AES-256 key from the full master key via HKDF-SHA256 rather than
	// using the raw ASCII bytes truncated to 32 (which both ignored any material
	// beyond 32 chars and used low-entropy ASCII directly as key bytes). HKDF spreads
	// the entire key over a uniform 32-byte key. Fixed salt/info → deterministic, so
	// existing ciphertexts decrypt across restarts (the key itself is the secret).
	key := make([]byte, 32)
	r := hkdf.New(sha256.New, []byte(masterKey), []byte("patchdeck.sealbox.v1"), []byte("aes-256-gcm host secrets"))
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	return &SealBox{key: key}, nil
}

func (s *SealBox) Encrypt(plain []byte) (string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil { return "", err }
	gcm, err := cipher.NewGCM(block)
	if err != nil { return "", err }
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil { return "", err }
	out := gcm.Seal(nonce, nonce, plain, nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

func (s *SealBox) Decrypt(ciphertext string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil { return nil, err }
	block, err := aes.NewCipher(s.key)
	if err != nil { return nil, err }
	gcm, err := cipher.NewGCM(block)
	if err != nil { return nil, err }
	ns := gcm.NonceSize()
	if len(raw) < ns { return nil, fmt.Errorf("cipher too short") }
	nonce, enc := raw[:ns], raw[ns:]
	return gcm.Open(nil, nonce, enc, nil)
}
