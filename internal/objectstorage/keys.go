package objectstorage

import (
	"fmt"
	"regexp"
)

// This file holds pure, side-effect-free helpers for building the canonical
// object keys the PDF ingestion pipeline needs for immutable evidence
// storage (see PutObjectIfAbsent in client.go). They do not talk to Object
// Storage or any other package -- callers combine them with
// PutObjectIfAbsent themselves.

var (
	sha256HexPattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	parserIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// SourceObjectKey returns the canonical object key for a source PDF,
// identified by its FULL lowercase-hex SHA-256 digest -- never a truncated
// prefix. A prefix (e.g. 12 hex chars) is not a safe identity: it shrinks
// the collision space and gives no cryptographic assurance that two
// different source files can't land on the same key.
func SourceObjectKey(sourceSHA256Hex string) (string, error) {
	if err := validateSHA256Hex("source", sourceSHA256Hex); err != nil {
		return "", err
	}
	return fmt.Sprintf("raw/%s.pdf", sourceSHA256Hex), nil
}

// ParserRunManifestKey returns the canonical, stable key for the
// parser-run manifest that anchors one (source_sha256, parser_name,
// parser_version) identity. This manifest is meant to be the single
// authority for that identity's page_number -> (page object ref, page
// media SHA-256) mapping: create it exactly once via PutObjectIfAbsent: if
// PutObjectIfAbsent reports PutOutcomeReused, the caller must fetch and
// reuse the pre-existing manifest's page artifacts rather than re-running
// the parser and trusting a fresh pdfseparate output, since pdfseparate is
// not guaranteed byte-for-byte deterministic between runs of the same
// input.
//
// Changing parser_version changes this key, which is intentional: a
// different parser version is a different parser-run identity and requires
// its own manifest and provenance, never an in-place update of the old one.
func ParserRunManifestKey(sourceSHA256Hex, parserName, parserVersion string) (string, error) {
	if err := validateSHA256Hex("source", sourceSHA256Hex); err != nil {
		return "", err
	}
	if err := validateParserIdentity("parser name", parserName); err != nil {
		return "", err
	}
	if err := validateParserIdentity("parser version", parserVersion); err != nil {
		return "", err
	}
	return fmt.Sprintf("manifests/parser-runs/%s/%s/%s/manifest.json", sourceSHA256Hex, parserName, parserVersion), nil
}

// PageObjectKey returns the canonical, content-addressed object key for one
// parsed page's binary artifact. Identity is the full tuple demanded by
// the hardening spec -- source SHA-256, parser name, parser version, page
// number, AND the page media's own SHA-256 -- never the page number alone
// (".../page-1.pdf" is exactly the mutable-key shape this hardening removes):
// a second pdfseparate run of the same source+parser+version can
// legitimately produce a different media SHA-256 for the same page number,
// and that must resolve to a distinct key, not an overwrite of the first
// run's artifact.
func PageObjectKey(sourceSHA256Hex, parserName, parserVersion string, pageNumber int, mediaSHA256Hex string) (string, error) {
	if err := validateSHA256Hex("source", sourceSHA256Hex); err != nil {
		return "", err
	}
	if err := validateParserIdentity("parser name", parserName); err != nil {
		return "", err
	}
	if err := validateParserIdentity("parser version", parserVersion); err != nil {
		return "", err
	}
	if pageNumber < 1 {
		return "", fmt.Errorf("object storage: page number must be >= 1, got %d", pageNumber)
	}
	if err := validateSHA256Hex("media", mediaSHA256Hex); err != nil {
		return "", err
	}
	return fmt.Sprintf("pages/%s/%s/%s/page-%04d-%s.pdf", sourceSHA256Hex, parserName, parserVersion, pageNumber, mediaSHA256Hex), nil
}

func validateSHA256Hex(label, value string) error {
	if !sha256HexPattern.MatchString(value) {
		return fmt.Errorf("object storage: %s SHA-256 must be 64 lowercase hex characters, got %q", label, value)
	}
	return nil
}

func validateParserIdentity(label, value string) error {
	if value == "" || !parserIdentityPattern.MatchString(value) {
		return fmt.Errorf("object storage: %s must be non-empty and match %s, got %q", label, parserIdentityPattern.String(), value)
	}
	return nil
}
