// Package objectstorage is a hand-rolled client for OCI Object Storage's
// native REST API (no vendored OCI/AWS SDK, consistent with the rest of this
// project -- every external HTTP integration here is stdlib net/http plus a
// from-scratch implementation of that provider's own request-signing
// scheme). Object Storage is the source-of-truth bucket for the knowledge
// ingestion pipeline (raw/normalized/manifests/logs) landed at
// explorarte-org-knowledge-source, namespace axkhdnwe6r1c, region
// sa-santiago-1.
package objectstorage

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRequestTimeout   = 2 * time.Minute
	defaultMaxResponseBytes = 8 << 20
)

var fingerprintPattern = regexp.MustCompile(`^([0-9a-f]{2}:){15}[0-9a-f]{2}$`)

type LookupEnv func(string) (string, bool)

type Config struct {
	Enabled          bool
	TenancyOCID      string
	UserOCID         string
	Fingerprint      string
	Region           string
	Namespace        string
	Bucket           string
	PrivateKeyFile   string
	RequestTimeout   time.Duration
	MaxResponseBytes int
}

func LoadConfig(lookup LookupEnv) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("object-storage environment lookup is nil")
	}
	cfg := Config{RequestTimeout: defaultRequestTimeout, MaxResponseBytes: defaultMaxResponseBytes}
	var err error
	if cfg.Enabled, err = envBool(lookup, "ORG_OBJECT_STORAGE_OCI_ENABLED", false); err != nil {
		return Config{}, err
	}
	cfg.TenancyOCID = envString(lookup, "ORG_OBJECT_STORAGE_OCI_TENANCY_OCID")
	cfg.UserOCID = envString(lookup, "ORG_OBJECT_STORAGE_OCI_USER_OCID")
	cfg.Fingerprint = envString(lookup, "ORG_OBJECT_STORAGE_OCI_FINGERPRINT")
	cfg.Region = envString(lookup, "ORG_OBJECT_STORAGE_OCI_REGION")
	cfg.Namespace = envString(lookup, "ORG_OBJECT_STORAGE_OCI_NAMESPACE")
	cfg.Bucket = envString(lookup, "ORG_OBJECT_STORAGE_OCI_BUCKET")
	cfg.PrivateKeyFile = envString(lookup, "ORG_OBJECT_STORAGE_OCI_PRIVATE_KEY_FILE")
	if cfg.RequestTimeout, err = envDuration(lookup, "ORG_OBJECT_STORAGE_OCI_REQUEST_TIMEOUT", defaultRequestTimeout); err != nil {
		return Config{}, err
	}
	if err = cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}


func (c Config) Validate() error {
	if c.RequestTimeout < time.Second || c.RequestTimeout > 10*time.Minute {
		return fmt.Errorf("object-storage request timeout outside allowed range")
	}
	if !c.Enabled {
		return nil
	}
	if !strings.HasPrefix(c.TenancyOCID, "ocid1.tenancy.") {
		return fmt.Errorf("object-storage tenancy OCID is invalid")
	}
	if !strings.HasPrefix(c.UserOCID, "ocid1.user.") {
		return fmt.Errorf("object-storage user OCID is invalid")
	}
	if !fingerprintPattern.MatchString(c.Fingerprint) {
		return fmt.Errorf("object-storage API key fingerprint is invalid")
	}
	if strings.TrimSpace(c.Region) == "" {
		return fmt.Errorf("object-storage region is required")
	}
	if strings.TrimSpace(c.Namespace) == "" {
		return fmt.Errorf("object-storage namespace is required")
	}
	if strings.TrimSpace(c.Bucket) == "" {
		return fmt.Errorf("object-storage bucket is required")
	}
	if strings.TrimSpace(c.PrivateKeyFile) == "" || !filepath.IsAbs(filepath.Clean(c.PrivateKeyFile)) {
		return fmt.Errorf("object-storage private key file must be an absolute path")
	}
	return nil
}

func (c Config) Host() string {
	return fmt.Sprintf("objectstorage.%s.oraclecloud.com", c.Region)
}

func (c Config) BaseURL() *url.URL {
	return &url.URL{Scheme: "https", Host: c.Host()}
}

func (c Config) KeyID() string {
	return fmt.Sprintf("%s/%s/%s", c.TenancyOCID, c.UserOCID, c.Fingerprint)
}

func defaultHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 90 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("object-storage redirects are forbidden")
		},
	}
}

func envString(lookup LookupEnv, key string) string {
	raw, ok := lookup(key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(raw)
}

func envBool(lookup LookupEnv, key string, fallback bool) (bool, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

func envDuration(lookup LookupEnv, key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}
