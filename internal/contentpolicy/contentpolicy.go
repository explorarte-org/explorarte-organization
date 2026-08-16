// Package contentpolicy is the single deterministic content-safety engine.
//
// It detects credential material but does not decide one global disposition.
// Callers own the boundary policy: task ingress rejects credentials, governed
// knowledge verifies declarations, provider egress skips calls, and
// observability may redact. Detection is deliberately narrower than general
// data classification.
package contentpolicy

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Kind is an audit-safe category. It never contains matched content.
type Kind string

const (
	KindAPIToken                 Kind = "api_token"
	KindPassword                 Kind = "password"
	KindPrivateKey               Kind = "private_key"
	KindCloudCredential          Kind = "cloud_credential"
	KindDatabaseURLCredential    Kind = "database_url_credential"
	KindSessionToken             Kind = "session_token"
	KindWebhookSigningSecret     Kind = "webhook_signing_secret"
	KindOAuthClientSecret        Kind = "oauth_client_secret"
	KindServiceAccountCredential Kind = "service_account_credential"
	KindCredentialAssignment     Kind = "credential_assignment"
)

// Finding records only category and byte offsets. The matched value is never
// retained, so findings are safe to render in errors and audit records.
type Finding struct {
	Kind  Kind
	Start int
	End   int
}

func (f Finding) String() string {
	return fmt.Sprintf("credential/%s at bytes %d-%d", f.Kind, f.Start, f.End)
}

// Assessment is the deterministic result for one body.
type Assessment struct {
	Findings []Finding
}

func (a Assessment) HasCredentials() bool { return len(a.Findings) > 0 }

func (a Assessment) First() (Finding, bool) {
	if len(a.Findings) == 0 {
		return Finding{}, false
	}
	return a.Findings[0], true
}

func (a Assessment) Kinds() []string {
	seen := make(map[Kind]struct{})
	var kinds []string
	for _, finding := range a.Findings {
		if _, exists := seen[finding.Kind]; exists {
			continue
		}
		seen[finding.Kind] = struct{}{}
		kinds = append(kinds, string(finding.Kind))
	}
	sort.Strings(kinds)
	return kinds
}

type detector struct {
	kind       Kind
	pattern    *regexp.Regexp
	valueGroup int
}

var detectors = []detector{
	{KindAPIToken, regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{16,}`), 0},
	{KindAPIToken, regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}`), 0},
	{KindAPIToken, regexp.MustCompile(`\b(?:ghp|gho|ghs|ghu)_[A-Za-z0-9]{30,}`), 0},
	{KindAPIToken, regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{30,}`), 0},
	{KindAPIToken, regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{16,}`), 0},
	{KindAPIToken, regexp.MustCompile(`\bxox[abpsr]-[A-Za-z0-9-]{10,}`), 0},
	{KindAPIToken, regexp.MustCompile(`\bAIza[A-Za-z0-9_-]{35}`), 0},
	{KindAPIToken, regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}`), 0},
	{KindAPIToken, regexp.MustCompile(`\bnpm_[A-Za-z0-9]{30,}`), 0},
	{KindAPIToken, regexp.MustCompile(`\bdop_v1_[a-f0-9]{32,}`), 0},
	{KindPrivateKey, regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY(?: BLOCK)?-----`), 0},
	{KindCloudCredential, regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), 0},
	{KindCloudCredential, regexp.MustCompile(`(?i)aws_secret_access_key\s*[:=]\s*["']?([A-Za-z0-9/+=]{40})`), 1},
	{KindCloudCredential, regexp.MustCompile(`(?i)AccountKey\s*=\s*([A-Za-z0-9/+=]{40,})`), 1},
	{KindServiceAccountCredential, regexp.MustCompile(`(?is)"type"\s*:\s*"service_account".{0,900}?"private_key"\s*:`), 0},
	{KindDatabaseURLCredential, regexp.MustCompile(`(?i)\b(?:postgres|postgresql|mysql|mongodb(?:\+srv)?|redis|rediss|amqps?|mssql)://[^\s:/@]+:([^\s@/]{3,})@`), 1},
	{KindSessionToken, regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), 0},
	{KindSessionToken, regexp.MustCompile(`(?i)authorization\s*:\s*bearer\s+([A-Za-z0-9._~+/=-]{16,})`), 1},
	{KindSessionToken, regexp.MustCompile(`(?i)\b(?:session|sessionid|sid|jsessionid|phpsessid)\s*=\s*([A-Za-z0-9._-]{16,})`), 1},
	{KindWebhookSigningSecret, regexp.MustCompile(`\bwhsec_[A-Za-z0-9_-]{16,}`), 0},
	{KindWebhookSigningSecret, regexp.MustCompile(`(?i)\bsigning[_-]?secret\s*["']?\s*[:=]\s*["']?([^\s"',]{8,})`), 1},
	{KindOAuthClientSecret, regexp.MustCompile(`(?i)\bclient[_-]?secret\s*["']?\s*[:=]\s*["']?([^\s"',]{8,})`), 1},
	{KindPassword, regexp.MustCompile(`(?i)\b(?:password|passwd|pwd)\s*["']?\s*[:=]\s*["']?([^\s"',]{6,})`), 1},
	// This preserves the broad admission backstop used by governed knowledge
	// while sharing one detector inventory with task ingress.
	{KindCredentialAssignment, regexp.MustCompile(`(?i)(?:api[_-]?key|secret[_-]?key|access[_-]?token|private[_-]?key)\s*[:=]\s*["']?([A-Za-z0-9/_\-+=]{8,})["']?`), 1},
}

var placeholderPattern = regexp.MustCompile(`(?i)^(?:[*x•.]+|<[^>]*>|\{\{?[^}]*\}?\}|\$[A-Za-z_][A-Za-z0-9_]*|\$\{[^}]*\}|redacted|removed|hidden|secret|changeme|placeholder|example|dummy|test|none|null|nil|todo|tbd|(?:your|insert)[_-](?:api[_-]?key|access[_-]?token|token|password|client[_-]?secret|credential)(?:[_-]here)?)$`)

func isPlaceholder(value string) bool {
	trimmed := strings.Trim(strings.TrimSpace(value), `"'`)
	return trimmed == "" || placeholderPattern.MatchString(trimmed)
}

// Analyze reports typed risks in stable byte order.
func Analyze(text string) Assessment {
	if text == "" {
		return Assessment{}
	}
	var findings []Finding
	for _, detector := range detectors {
		for _, match := range detector.pattern.FindAllStringSubmatchIndex(text, -1) {
			if detector.valueGroup > 0 {
				start, end := match[2*detector.valueGroup], match[2*detector.valueGroup+1]
				if start < 0 || isPlaceholder(text[start:end]) {
					continue
				}
			}
			findings = append(findings, Finding{Kind: detector.kind, Start: match[0], End: match[1]})
		}
	}
	return Assessment{Findings: collapse(findings)}
}

func collapse(findings []Finding) []Finding {
	if len(findings) < 2 {
		return findings
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Start != findings[j].Start {
			return findings[i].Start < findings[j].Start
		}
		if findings[i].End != findings[j].End {
			return findings[i].End > findings[j].End
		}
		return findings[i].Kind < findings[j].Kind
	})
	kept := make([]Finding, 0, len(findings))
	for _, candidate := range findings {
		if len(kept) == 0 || candidate.Start >= kept[len(kept)-1].End {
			kept = append(kept, candidate)
			continue
		}
		// Overlapping detectors describe one unsafe redaction region. Union
		// partial overlaps so no credential suffix can escape redaction.
		last := &kept[len(kept)-1]
		if candidate.End > last.End {
			last.End = candidate.End
		}
	}
	return kept
}

// RedactCredentials removes credential findings while preserving surrounding
// ordinary text.
func RedactCredentials(text string) (string, []Finding) {
	findings := Analyze(text).Findings
	if len(findings) == 0 {
		return text, nil
	}
	var out strings.Builder
	previous := 0
	for _, finding := range findings {
		out.WriteString(text[previous:finding.Start])
		fmt.Fprintf(&out, "[credential_redacted=true credential_type=%s]", finding.Kind)
		previous = finding.End
	}
	out.WriteString(text[previous:])
	return out.String(), findings
}
