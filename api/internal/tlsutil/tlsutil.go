package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// EnsureSelfSignedCert checks whether the certificate at certPath exists and
// is valid for at least 30 days.  If the cert is missing or needs renewal it
// calls GenerateSelfSignedCert to create a fresh cert+key pair.
func EnsureSelfSignedCert(certPath, keyPath string) error {
	if CertNeedsRenewal(certPath, 30) {
		return GenerateSelfSignedCert(certPath, keyPath)
	}
	return nil
}

// CertNeedsRenewal returns true when the certificate at certPath does not
// exist, cannot be parsed, or expires within renewBeforeDays days.
func CertNeedsRenewal(certPath string, renewBeforeDays int) bool {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return true
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return true
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true
	}
	threshold := time.Now().Add(time.Duration(renewBeforeDays) * 24 * time.Hour)
	return cert.NotAfter.Before(threshold)
}

// GenerateSelfSignedCert creates an ECDSA P-256 self-signed certificate with
// a 1-year validity period and writes the PEM-encoded cert and key to the
// supplied paths.  Parent directories are created automatically.
//
// The certificate includes:
//   - Subject: CN=patchdeck
//   - SANs: localhost, 127.0.0.1, ::1
func GenerateSelfSignedCert(certPath, keyPath string) error {
	// Ensure parent directories exist.
	for _, p := range []string{certPath, keyPath} {
		dir := filepath.Dir(p)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0750); err != nil {
				return fmt.Errorf("create directory %s: %w", dir, err)
			}
		}
	}

	// Generate ECDSA P-256 private key.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ECDSA key: %w", err)
	}

	// Serial number — random 128-bit integer.
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return fmt.Errorf("generate serial number: %w", err)
	}

	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "patchdeck"},
		NotBefore:    now,
		NotAfter:     now.Add(365 * 24 * time.Hour),

		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},

		DNSNames:    []string{"localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}

	// Write certificate PEM.
	certFile, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("write cert file: %w", err)
	}
	defer certFile.Close()
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return fmt.Errorf("encode cert PEM: %w", err)
	}

	// Write private key PEM.
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal private key: %w", err)
	}
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("write key file: %w", err)
	}
	defer keyFile.Close()
	if err := pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		return fmt.Errorf("encode key PEM: %w", err)
	}

	return nil
}
