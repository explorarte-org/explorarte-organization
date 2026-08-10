package objectstorage

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeTestPrivateKey(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path, key
}

var authHeaderPattern = regexp.MustCompile(`^Signature version="1",headers="([^"]+)",keyId="([^"]+)",algorithm="rsa-sha256",signature="([^"]+)"$`)

func TestSignGetRequestOmitsBodyHeaders(t *testing.T) {
	path, key := writeTestPrivateKey(t)
	s, err := newSigner("tenancy/user/fp", path)
	if err != nil {
		t.Fatalf("newSigner: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, "https://objectstorage.sa-santiago-1.oraclecloud.com/n/ns/b/bucket/o?prefix=raw/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Date", "Mon, 10 Aug 2026 06:00:00 GMT")

	if err := s.sign(req, nil); err != nil {
		t.Fatalf("sign: %v", err)
	}

	match := authHeaderPattern.FindStringSubmatch(req.Header.Get("Authorization"))
	if match == nil {
		t.Fatalf("authorization header does not match expected shape: %q", req.Header.Get("Authorization"))
	}
	headersList, keyID, signatureB64 := match[1], match[2], match[3]
	if headersList != "date (request-target) host" {
		t.Fatalf("unexpected signed header list for GET: %q", headersList)
	}
	if keyID != "tenancy/user/fp" {
		t.Fatalf("unexpected keyId: %q", keyID)
	}
	if req.Header.Get("Content-Length") != "" || req.Header.Get("X-Content-Sha256") != "" {
		t.Fatalf("GET request must not carry body-signing headers")
	}

	signingString := strings.Join([]string{
		"date: Mon, 10 Aug 2026 06:00:00 GMT",
		"(request-target): get " + req.URL.RequestURI(),
		"host: " + req.URL.Host,
	}, "\n")
	verifySignature(t, key, signingString, signatureB64)
}

func TestSignPutRequestIncludesBodyHeaders(t *testing.T) {
	path, key := writeTestPrivateKey(t)
	s, err := newSigner("tenancy/user/fp", path)
	if err != nil {
		t.Fatalf("newSigner: %v", err)
	}
	body := []byte(`{"hello":"world"}`)
	req, err := http.NewRequest(http.MethodPut, "https://objectstorage.sa-santiago-1.oraclecloud.com/n/ns/b/bucket/o/manifests%2Ffoo.json", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Date", "Mon, 10 Aug 2026 06:00:00 GMT")
	req.Header.Set("Content-Type", "application/json")

	if err := s.sign(req, body); err != nil {
		t.Fatalf("sign: %v", err)
	}

	match := authHeaderPattern.FindStringSubmatch(req.Header.Get("Authorization"))
	if match == nil {
		t.Fatalf("authorization header does not match expected shape: %q", req.Header.Get("Authorization"))
	}
	headersList, _, signatureB64 := match[1], match[2], match[3]
	if headersList != "date (request-target) host content-length content-type x-content-sha256" {
		t.Fatalf("unexpected signed header list for PUT: %q", headersList)
	}

	sum := sha256.Sum256(body)
	wantContentSHA256 := base64.StdEncoding.EncodeToString(sum[:])
	if got := req.Header.Get("X-Content-Sha256"); got != wantContentSHA256 {
		t.Fatalf("x-content-sha256 = %q, want %q", got, wantContentSHA256)
	}
	if got := req.Header.Get("Content-Length"); got != "17" {
		t.Fatalf("content-length = %q, want 17", got)
	}

	signingString := strings.Join([]string{
		"date: Mon, 10 Aug 2026 06:00:00 GMT",
		"(request-target): put " + req.URL.RequestURI(),
		"host: " + req.URL.Host,
		"content-length: 17",
		"content-type: application/json",
		"x-content-sha256: " + wantContentSHA256,
	}, "\n")
	verifySignature(t, key, signingString, signatureB64)
}

func TestSignRejectsMissingDateHeader(t *testing.T) {
	path, _ := writeTestPrivateKey(t)
	s, err := newSigner("tenancy/user/fp", path)
	if err != nil {
		t.Fatalf("newSigner: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, "https://objectstorage.sa-santiago-1.oraclecloud.com/n/ns/b/bucket/o", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := s.sign(req, nil); err == nil {
		t.Fatalf("expected error for missing Date header")
	}
}

func TestNewSignerRejectsInvalidPEM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, []byte("not a pem file"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if _, err := newSigner("tenancy/user/fp", path); err == nil {
		t.Fatalf("expected error for invalid PEM")
	}
}

func verifySignature(t *testing.T, key *rsa.PrivateKey, signingString, signatureB64 string) {
	t.Helper()
	signature, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	digest := sha256.Sum256([]byte(signingString))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("signature does not verify against expected signing string: %v", err)
	}
}

var _ = url.Values{}
