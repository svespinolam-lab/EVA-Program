package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func loadTLSUtilTestCert(t *testing.T, certFile, keyFile string) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return pair, cert
}

func TestEnsureCertCreatesReusesAndRegenerates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "certificates")
	hosts := []string{"192.0.2.10", "eva.example.test"}
	before := time.Now()
	certFile, keyFile, created, err := EnsureCert(dir, hosts)
	if err != nil || !created {
		t.Fatalf("create: created=%v, err=%v", created, err)
	}
	if certFile != filepath.Join(dir, "cert.pem") || keyFile != filepath.Join(dir, "key.pem") {
		t.Fatalf("unexpected paths: %q, %q", certFile, keyFile)
	}
	_, cert := loadTLSUtilTestCert(t, certFile, keyFile)
	if cert.Subject.CommonName != "EVA Station (local)" || !slices.Equal(cert.Subject.Organization, []string{"EVA"}) {
		t.Fatalf("subject: %+v", cert.Subject)
	}
	key, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		t.Fatalf("unexpected public key: %T", cert.PublicKey)
	}
	if cert.KeyUsage != x509.KeyUsageDigitalSignature|x509.KeyUsageKeyEncipherment || !slices.Equal(cert.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) {
		t.Fatalf("unexpected key usage: %v, %v", cert.KeyUsage, cert.ExtKeyUsage)
	}
	if cert.SerialNumber.Sign() <= 0 || cert.SerialNumber.BitLen() > 128 {
		t.Fatalf("serial number: %v", cert.SerialNumber)
	}
	if cert.NotBefore.Sub(before.Add(-time.Hour)).Abs() > 5*time.Second || cert.NotAfter.Sub(before.Add(825*24*time.Hour)).Abs() > 5*time.Second {
		t.Fatalf("validity: %v to %v", cert.NotBefore, cert.NotAfter)
	}
	for _, host := range append([]string{"localhost", "127.0.0.1", "::1"}, hosts...) {
		if err := cert.VerifyHostname(host); err != nil {
			t.Errorf("missing SAN %q: %v", host, err)
		}
	}
	if err := cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
		t.Fatalf("self signature: %v", err)
	}
	if fi, err := os.Stat(keyFile); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("private key permissions: %o", fi.Mode().Perm())
	}
	fingerprint, err := Fingerprint(certFile)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(cert.Raw)
	var pairs []string
	for _, b := range digest {
		pairs = append(pairs, fmt.Sprintf("%02X", b))
	}
	if fingerprint != strings.Join(pairs, ":") {
		t.Fatalf("fingerprint: %q", fingerprint)
	}
	certAgain, keyAgain, created, err := EnsureCert(dir, hosts)
	if err != nil || created || certAgain != certFile || keyAgain != keyFile {
		t.Fatalf("reuse: created=%v, err=%v", created, err)
	}
	if same, err := Fingerprint(certAgain); err != nil || same != fingerprint {
		t.Fatalf("reused fingerprint: %q, %v", same, err)
	}
	hosts = append(hosts, "new.eva.example.test")
	_, _, created, err = EnsureCert(dir, hosts)
	if err != nil || !created {
		t.Fatalf("new host: created=%v, err=%v", created, err)
	}
	if changed, err := Fingerprint(certFile); err != nil || changed == fingerprint {
		t.Fatalf("regenerated fingerprint: %q, %v", changed, err)
	}
	_, cert = loadTLSUtilTestCert(t, certFile, keyFile)
	if err := cert.VerifyHostname("new.eva.example.test"); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureCertTrustedHTTPS(t *testing.T) {
	certFile, keyFile, _, err := EnsureCert(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	pair, cert := loadTLSUtilTestCert(t, certFile, keyFile)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "EVA TLS ready")
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "EVA TLS ready" {
		t.Fatalf("HTTPS response: status=%d, body=%q, err=%v", response.StatusCode, body, err)
	}
	if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
		t.Fatal("TLS connection was not verified")
	}
}

func TestEnsureCertReplacesUnusablePair(t *testing.T) {
	for _, scenario := range []string{"expiring", "missing-key", "invalid-cert"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			certFile, keyFile, _, err := EnsureCert(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			original, err := Fingerprint(certFile)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "expiring":
				pair, cert := loadTLSUtilTestCert(t, certFile, keyFile)
				cert.NotAfter = time.Now().Add(29 * 24 * time.Hour)
				der, err := x509.CreateCertificate(rand.Reader, cert, cert, cert.PublicKey, pair.PrivateKey)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
					t.Fatal(err)
				}
			case "missing-key":
				if err := os.Remove(keyFile); err != nil {
					t.Fatal(err)
				}
			case "invalid-cert":
				if err := os.WriteFile(certFile, []byte("invalid"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, _, created, err := EnsureCert(dir, nil)
			if err != nil || !created {
				t.Fatalf("replace %s: created=%v, err=%v", scenario, created, err)
			}
			loadTLSUtilTestCert(t, certFile, keyFile)
			if fingerprint, err := Fingerprint(certFile); err != nil || fingerprint == original {
				t.Fatalf("replacement fingerprint: %q, %v", fingerprint, err)
			}
		})
	}
}

func TestFingerprintRejectsInvalidCertificate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cert.pem")
	if _, err := Fingerprint(path); err == nil {
		t.Fatal("missing certificate accepted")
	}
	for _, data := range [][]byte{[]byte("invalid"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid DER")})} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Fingerprint(path); err == nil {
			t.Fatal("invalid certificate accepted")
		}
	}
}
