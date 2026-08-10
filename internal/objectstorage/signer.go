package objectstorage

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// signer implements OCI's Request Signing Version 1: a fixed, ordered set of
// headers is concatenated into a "signing string", RSA-SHA256/PKCS1v15
// signed with the API key's private key, and the base64 signature plus
// keyId are carried in the Authorization header. There is no OCI SDK
// dependency here -- this is the entire algorithm, hand-rolled, matching how
// every other external provider in this codebase is integrated.
type signer struct {
	keyID      string
	privateKey *rsa.PrivateKey
}

func newSigner(keyID, privateKeyFile string) (*signer, error) {
	raw, err := os.ReadFile(privateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("read object-storage private key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("object-storage private key is not valid PEM")
	}
	key, err := parseRSAPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse object-storage private key: %w", err)
	}
	return &signer{keyID: keyID, privateKey: key}, nil
}

func parseRSAPrivateKey(der []byte) (*rsa.PrivateKey, error) {
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	generic, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	key, ok := generic.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("object-storage private key is not RSA")
	}
	return key, nil
}

// sign attaches Date, (request-target), Host, and -- for requests carrying a
// body -- Content-Length, Content-Type, and X-Content-Sha256 to req's
// Authorization header. req.Header must already carry Date and Content-Type
// (for body requests); Host and (request-target) are derived from req
// itself, never taken from caller-supplied headers.
func (s *signer) sign(req *http.Request, body []byte) error {
	date := req.Header.Get("Date")
	if date == "" {
		return errors.New("object-storage request is missing Date header")
	}
	headers := []string{"date", "(request-target)", "host"}
	values := map[string]string{
		"date":              date,
		"(request-target)":  strings.ToLower(req.Method) + " " + req.URL.RequestURI(),
		"host":              req.URL.Host,
	}
	if body != nil {
		sum := sha256.Sum256(body)
		contentSHA256 := base64.StdEncoding.EncodeToString(sum[:])
		req.Header.Set("Content-Length", strconv.Itoa(len(body)))
		req.Header.Set("X-Content-Sha256", contentSHA256)
		headers = append(headers, "content-length", "content-type", "x-content-sha256")
		values["content-length"] = strconv.Itoa(len(body))
		values["content-type"] = req.Header.Get("Content-Type")
		values["x-content-sha256"] = contentSHA256
	}
	lines := make([]string, 0, len(headers))
	for _, h := range headers {
		lines = append(lines, h+": "+values[h])
	}
	signingString := strings.Join(lines, "\n")
	digest := sha256.Sum256([]byte(signingString))
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return fmt.Errorf("sign object-storage request: %w", err)
	}
	authHeader := fmt.Sprintf(
		`Signature version="1",headers="%s",keyId="%s",algorithm="rsa-sha256",signature="%s"`,
		strings.Join(headers, " "), s.keyID, base64.StdEncoding.EncodeToString(signature),
	)
	req.Header.Set("Authorization", authHeader)
	return nil
}
