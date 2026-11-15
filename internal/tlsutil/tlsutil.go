// Package tlsutil manages self-signed certificates for a local EVA station.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EnsureCert reuses a valid certificate covering hosts, or generates a local
// ECDSA P-256 certificate and private key. Loopback names are always included.
func EnsureCert(dir string, hosts []string) (certFile, keyFile string, created bool, err error) {
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	names := append([]string{"localhost", "127.0.0.1", "::1"}, hosts...)
	now := time.Now()
	if pair, loadErr := tls.LoadX509KeyPair(certFile, keyFile); loadErr == nil {
		cert, parseErr := x509.ParseCertificate(pair.Certificate[0])
		if parseErr == nil && !now.Before(cert.NotBefore) && cert.NotAfter.After(now.Add(30*24*time.Hour)) {
			covers := true
			for _, host := range names {
				if cert.VerifyHostname(host) != nil {
					covers = false
					break
				}
			}
			if covers {
				return certFile, keyFile, false, nil
			}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return certFile, keyFile, false, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return certFile, keyFile, false, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return certFile, keyFile, false, err
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "EVA Station (local)", Organization: []string{"EVA"}},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.Add(825 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	seen := make(map[string]bool)
	for _, host := range names {
		if seen[host] {
			continue
		}
		seen[host] = true
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return certFile, keyFile, false, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return certFile, keyFile, false, err
	}
	if err := writePEMFile(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}, 0o600); err != nil {
		return certFile, keyFile, false, err
	}
	if err := writePEMFile(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: der}, 0o644); err != nil {
		return certFile, keyFile, false, err
	}
	return certFile, keyFile, true, nil
}

func writePEMFile(path string, block *pem.Block, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".eva-tls-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := pem.Encode(f, block); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Fingerprint returns the certificate DER's SHA-256 digest as uppercase hex pairs.
func Fingerprint(certFile string) (string, error) {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("no PEM certificate in %s", certFile)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(cert.Raw)
	hexDigest := strings.ToUpper(hex.EncodeToString(digest[:]))
	pairs := make([]string, len(digest))
	for i := range pairs {
		pairs[i] = hexDigest[2*i : 2*i+2]
	}
	return strings.Join(pairs, ":"), nil
}
