package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

type SealBox struct { key []byte }

func NewSealBox(masterKey string) (*SealBox, error) {
	if len(masterKey) < 32 {
		return nil, fmt.Errorf("master key too short")
	}
	k := []byte(masterKey)
	if len(k) > 32 { k = k[:32] }
	return &SealBox{key: k}, nil
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
