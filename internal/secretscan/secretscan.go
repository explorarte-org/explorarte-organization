// Package secretscan detects credential material in free text.
//
// It exists to support one policy, stated once here so that every call site
// inherits the same reading of it:
//
//	Secrets are rejected at ingress; sensitive information is governed by
//	classification; observability is redacted.
//
// The three clauses are deliberately different mechanisms:
//
//   - At ingress (task instructions, and any other free-text surface that
//     reaches an agent) an unambiguous secret is a hard rejection. Nothing is
//     rewritten on the way in. Silently redacting an instruction changes its
//     meaning, and a task that runs to "success" on quietly mutilated
//     instructions is a worse outcome than a task that refuses to start.
//   - Sensitive-but-legitimate information -- personal data, clinical data,
//     commercially confidential material -- is NOT this package's concern.
//     Those are governed by data classification and access control, which
//     decide who may see them, not by refusing to carry them. Folding them in
//     here would turn a credential filter into a censor and would produce
//     exactly the false rejections that make people route around a control.
//   - Observability (logs, traces, error strings, audit renderings) redacts:
//     Redact replaces the matched span while preserving the fact and the kind,
//     so an operator learns that an api_token was present without the token
//     entering a log they cannot rotate.
//
// Findings never carry the matched text. A detector whose own output leaks
// the value it detected has moved the secret rather than contained it.
package secretscan

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Kind names a category of credential material. These are the categories the
// organization has decided are unambiguous secrets; deciding that something
// new belongs here is a policy change, not a tuning exercise.
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
)

// Finding records that credential material was present, and where, but never
// what. Start and End bound the matched span so Redact can replace it and so
// an audit trail can say "bytes 412-455 of the instructions" without
// reproducing them.
type Finding struct {
	Kind  Kind
	Start int
	End   int
}

// String is audit-safe by construction: it can only ever emit the category
// and the offsets.
func (f Finding) String() string {
	return fmt.Sprintf("%s at bytes %d-%d", f.Kind, f.Start, f.End)
}

type detector struct {
	kind    Kind
	pattern *regexp.Regexp
	// valueGroup, when non-zero, names the capture group holding the secret
	// value itself; the detector fires only when that group survives
	// isPlaceholder. Detectors keyed purely on shape (a PEM header, a
	// provider-prefixed token) leave it zero.
	valueGroup int
}

// detectors are tuned for precision over recall. A false positive rejects a
// legitimate task and teaches people to route around the control, which costs
// more than the recall it buys; breadth belongs in classification and access
// control, not here.
var detectors = []detector{
	// Provider-issued tokens carry distinctive prefixes. Matching the prefix
	// plus a plausible body is far more precise than entropy heuristics,
	// which flag hashes, UUIDs and base64 payloads indiscriminately.
	{kind: KindAPIToken, pattern: regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{16,}`)},
	{kind: KindAPIToken, pattern: regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}`)},
	{kind: KindAPIToken, pattern: regexp.MustCompile(`\b(?:ghp|gho|ghs|ghu)_[A-Za-z0-9]{30,}`)},
	{kind: KindAPIToken, pattern: regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{30,}`)},
	{kind: KindAPIToken, pattern: regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{16,}`)},
	{kind: KindAPIToken, pattern: regexp.MustCompile(`\bxox[abpsr]-[A-Za-z0-9-]{10,}`)},
	{kind: KindAPIToken, pattern: regexp.MustCompile(`\bAIza[A-Za-z0-9_-]{35}`)},
	{kind: KindAPIToken, pattern: regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}`)},
	{kind: KindAPIToken, pattern: regexp.MustCompile(`\bnpm_[A-Za-z0-9]{30,}`)},
	{kind: KindAPIToken, pattern: regexp.MustCompile(`\bdop_v1_[a-f0-9]{32,}`)},

	{kind: KindPrivateKey, pattern: regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY(?: BLOCK)?-----`)},

	{kind: KindCloudCredential, pattern: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{kind: KindCloudCredential, pattern: regexp.MustCompile(`(?i)aws_secret_access_key\s*[:=]\s*["']?([A-Za-z0-9/+=]{40})`), valueGroup: 1},
	{kind: KindCloudCredential, pattern: regexp.MustCompile(`(?i)AccountKey\s*=\s*([A-Za-z0-9/+=]{40,})`), valueGroup: 1},

	{kind: KindServiceAccountCredential, pattern: regexp.MustCompile(`(?is)"type"\s*:\s*"service_account".{0,900}?"private_key"\s*:`)},

	{kind: KindDatabaseURLCredential, pattern: regexp.MustCompile(`(?i)\b(?:postgres|postgresql|mysql|mongodb(?:\+srv)?|redis|rediss|amqps?|mssql)://[^\s:/@]+:([^\s@/]{3,})@`), valueGroup: 1},

	{kind: KindSessionToken, pattern: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
	{kind: KindSessionToken, pattern: regexp.MustCompile(`(?i)authorization\s*:\s*bearer\s+([A-Za-z0-9._~+/=-]{16,})`), valueGroup: 1},
	{kind: KindSessionToken, pattern: regexp.MustCompile(`(?i)\b(?:session|sessionid|sid|jsessionid|phpsessid)\s*=\s*([A-Za-z0-9._-]{16,})`), valueGroup: 1},

	{kind: KindWebhookSigningSecret, pattern: regexp.MustCompile(`\bwhsec_[A-Za-z0-9_-]{16,}`)},
	{kind: KindWebhookSigningSecret, pattern: regexp.MustCompile(`(?i)\bsigning[_-]?secret\s*["']?\s*[:=]\s*["']?([^\s"',]{8,})`), valueGroup: 1},

	{kind: KindOAuthClientSecret, pattern: regexp.MustCompile(`(?i)\bclient[_-]?secret\s*["']?\s*[:=]\s*["']?([^\s"',]{8,})`), valueGroup: 1},

	// Keyed password assignments only. The bare word "password" appears in
	// perfectly ordinary instructions ("ask the user to reset their
	// password"), so a value is required and placeholders are excluded.
	{kind: KindPassword, pattern: regexp.MustCompile(`(?i)\b(?:password|passwd|pwd)\s*["']?\s*[:=]\s*["']?([^\s"',]{6,})`), valueGroup: 1},
}

// placeholderPattern recognises values that are obviously not real secrets:
// masked output, documentation stand-ins, and template variables. Rejecting
// these would make the control fire on the very text people write when they
// are being careful.
var placeholderPattern = regexp.MustCompile(`(?i)^(?:[*x•.]+|<[^>]*>|\{\{?[^}]*\}?\}|\$[A-Za-z_][A-Za-z0-9_]*|\$\{[^}]*\}|redacted|removed|hidden|secret|changeme|placeholder|example|dummy|test|none|null|nil|todo|tbd|your[_-]?\w*|my[_-]?\w*|insert[_-]?\w*)$`)

func isPlaceholder(value string) bool {
	trimmed := strings.Trim(strings.TrimSpace(value), `"'`)
	if trimmed == "" {
		return true
	}
	return placeholderPattern.MatchString(trimmed)
}

// Scan reports every unambiguous secret in text, ordered by position and with
// overlapping matches collapsed. The returned findings carry categories and
// offsets only.
func Scan(text string) []Finding {
	if text == "" {
		return nil
	}
	var findings []Finding
	for _, d := range detectors {
		for _, match := range d.pattern.FindAllStringSubmatchIndex(text, -1) {
			if d.valueGroup > 0 {
				start, end := match[2*d.valueGroup], match[2*d.valueGroup+1]
				if start < 0 || isPlaceholder(text[start:end]) {
					continue
				}
			}
			findings = append(findings, Finding{Kind: d.kind, Start: match[0], End: match[1]})
		}
	}
	return collapse(findings)
}

// collapse sorts findings and drops any wholly contained in an earlier one,
// so a credential matched by two detectors is reported once.
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
	kept := findings[:1]
	for _, candidate := range findings[1:] {
		last := kept[len(kept)-1]
		if candidate.Start < last.End {
			continue
		}
		kept = append(kept, candidate)
	}
	return kept
}

// Kinds returns the distinct categories present, sorted, for an audit record
// or an error message.
func Kinds(findings []Finding) []string {
	seen := make(map[Kind]struct{}, len(findings))
	var kinds []string
	for _, finding := range findings {
		if _, done := seen[finding.Kind]; done {
			continue
		}
		seen[finding.Kind] = struct{}{}
		kinds = append(kinds, string(finding.Kind))
	}
	sort.Strings(kinds)
	return kinds
}

// Redact is the observability half of the policy. It replaces each matched
// span with a marker naming the category, so a log line records that a
// session_token was present without recording the token. It is never used on
// the ingress path: input is rejected there, not rewritten.
func Redact(text string) (string, []Finding) {
	findings := Scan(text)
	if len(findings) == 0 {
		return text, nil
	}
	var out strings.Builder
	previous := 0
	for _, finding := range findings {
		out.WriteString(text[previous:finding.Start])
		fmt.Fprintf(&out, "[secret_redacted=true secret_type=%s]", finding.Kind)
		previous = finding.End
	}
	out.WriteString(text[previous:])
	return out.String(), findings
}
