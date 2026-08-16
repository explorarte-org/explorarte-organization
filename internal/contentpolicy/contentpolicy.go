// Package contentpolicy is the single deterministic content-safety engine.
//
// It detects typed risk signals but does not decide one global disposition.
// Callers own the boundary policy: task ingress rejects credentials, governed
// knowledge verifies declared data classes, and observability may redact
// credentials. Keeping detection and disposition separate prevents clinical
// vocabulary from being treated as a credential while ensuring every surface
// uses the same detectors.
package contentpolicy

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Risk is the policy dimension a finding belongs to.
type Risk string

const (
	RiskCredential Risk = "credential"
	RiskClinical   Risk = "clinical"
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
	KindClinicalTerminology      Kind = "clinical_terminology"
)

// Finding records only category and byte offsets. The matched value is never
// retained, so findings are safe to render in errors and audit records.
type Finding struct {
	Risk  Risk
	Kind  Kind
	Start int
	End   int
}

func (f Finding) String() string {
	return fmt.Sprintf("%s/%s at bytes %d-%d", f.Risk, f.Kind, f.Start, f.End)
}

// Assessment is the deterministic result for one body.
type Assessment struct {
	Findings []Finding
}

func (a Assessment) Has(risk Risk) bool {
	_, ok := a.First(risk)
	return ok
}

func (a Assessment) First(risk Risk) (Finding, bool) {
	for _, finding := range a.Findings {
		if finding.Risk == risk {
			return finding, true
		}
	}
	return Finding{}, false
}

func (a Assessment) For(risk Risk) []Finding {
	var findings []Finding
	for _, finding := range a.Findings {
		if finding.Risk == risk {
			findings = append(findings, finding)
		}
	}
	return findings
}

func (a Assessment) Kinds(risk Risk) []string {
	seen := make(map[Kind]struct{})
	var kinds []string
	for _, finding := range a.Findings {
		if finding.Risk != risk {
			continue
		}
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
	risk       Risk
	kind       Kind
	pattern    *regexp.Regexp
	valueGroup int
}

var detectors = []detector{
	{RiskCredential, KindAPIToken, regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{16,}`), 0},
	{RiskCredential, KindAPIToken, regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}`), 0},
	{RiskCredential, KindAPIToken, regexp.MustCompile(`\b(?:ghp|gho|ghs|ghu)_[A-Za-z0-9]{30,}`), 0},
	{RiskCredential, KindAPIToken, regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{30,}`), 0},
	{RiskCredential, KindAPIToken, regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{16,}`), 0},
	{RiskCredential, KindAPIToken, regexp.MustCompile(`\bxox[abpsr]-[A-Za-z0-9-]{10,}`), 0},
	{RiskCredential, KindAPIToken, regexp.MustCompile(`\bAIza[A-Za-z0-9_-]{35}`), 0},
	{RiskCredential, KindAPIToken, regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}`), 0},
	{RiskCredential, KindAPIToken, regexp.MustCompile(`\bnpm_[A-Za-z0-9]{30,}`), 0},
	{RiskCredential, KindAPIToken, regexp.MustCompile(`\bdop_v1_[a-f0-9]{32,}`), 0},
	{RiskCredential, KindPrivateKey, regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY(?: BLOCK)?-----`), 0},
	{RiskCredential, KindCloudCredential, regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), 0},
	{RiskCredential, KindCloudCredential, regexp.MustCompile(`(?i)aws_secret_access_key\s*[:=]\s*["']?([A-Za-z0-9/+=]{40})`), 1},
	{RiskCredential, KindCloudCredential, regexp.MustCompile(`(?i)AccountKey\s*=\s*([A-Za-z0-9/+=]{40,})`), 1},
	{RiskCredential, KindServiceAccountCredential, regexp.MustCompile(`(?is)"type"\s*:\s*"service_account".{0,900}?"private_key"\s*:`), 0},
	{RiskCredential, KindDatabaseURLCredential, regexp.MustCompile(`(?i)\b(?:postgres|postgresql|mysql|mongodb(?:\+srv)?|redis|rediss|amqps?|mssql)://[^\s:/@]+:([^\s@/]{3,})@`), 1},
	{RiskCredential, KindSessionToken, regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), 0},
	{RiskCredential, KindSessionToken, regexp.MustCompile(`(?i)authorization\s*:\s*bearer\s+([A-Za-z0-9._~+/=-]{16,})`), 1},
	{RiskCredential, KindSessionToken, regexp.MustCompile(`(?i)\b(?:session|sessionid|sid|jsessionid|phpsessid)\s*=\s*([A-Za-z0-9._-]{16,})`), 1},
	{RiskCredential, KindWebhookSigningSecret, regexp.MustCompile(`\bwhsec_[A-Za-z0-9_-]{16,}`), 0},
	{RiskCredential, KindWebhookSigningSecret, regexp.MustCompile(`(?i)\bsigning[_-]?secret\s*["']?\s*[:=]\s*["']?([^\s"',]{8,})`), 1},
	{RiskCredential, KindOAuthClientSecret, regexp.MustCompile(`(?i)\bclient[_-]?secret\s*["']?\s*[:=]\s*["']?([^\s"',]{8,})`), 1},
	{RiskCredential, KindPassword, regexp.MustCompile(`(?i)\b(?:password|passwd|pwd)\s*["']?\s*[:=]\s*["']?([^\s"',]{6,})`), 1},
	// This preserves the broad admission backstop used by governed knowledge
	// while sharing one detector inventory with task ingress.
	{RiskCredential, KindCredentialAssignment, regexp.MustCompile(`(?i)(?:api[_-]?key|secret[_-]?key|access[_-]?token|private[_-]?key)\s*[:=]\s*["']?([A-Za-z0-9/_\-+=]{8,})["']?`), 1},
}

var placeholderPattern = regexp.MustCompile(`(?i)^(?:[*x•.]+|<[^>]*>|\{\{?[^}]*\}?\}|\$[A-Za-z_][A-Za-z0-9_]*|\$\{[^}]*\}|redacted|removed|hidden|secret|changeme|placeholder|example|dummy|test|none|null|nil|todo|tbd|(?:your|my|insert)(?:[_-][a-z0-9]+)*)$`)

var clinicalPattern = regexp.MustCompile(`(?i)\b(` + joinAlternatives([]string{
	"diagnóstico", "diagnostico", "diagnosis", "paciente", "patient",
	"historia clínica", "historia clinica", "medical history", "medical record", "clinical record",
	"receta médica", "receta medica", "prescription", "expediente médico", "expediente medico",
	"tratamiento médico", "tratamiento medico", "medicación", "medicacion", "medication",
	"síntoma", "sintoma", "symptom", "patología", "patologia", "pathology",
}) + `)\b`)

func joinAlternatives(words []string) string {
	quoted := make([]string, len(words))
	for i, word := range words {
		quoted[i] = regexp.QuoteMeta(word)
	}
	return strings.Join(quoted, "|")
}

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
			findings = append(findings, Finding{Risk: detector.risk, Kind: detector.kind, Start: match[0], End: match[1]})
		}
	}
	for _, match := range clinicalPattern.FindAllStringIndex(text, -1) {
		findings = append(findings, Finding{Risk: RiskClinical, Kind: KindClinicalTerminology, Start: match[0], End: match[1]})
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
		if findings[i].Risk != findings[j].Risk {
			return findings[i].Risk < findings[j].Risk
		}
		if findings[i].End != findings[j].End {
			return findings[i].End > findings[j].End
		}
		return findings[i].Kind < findings[j].Kind
	})
	kept := make([]Finding, 0, len(findings))
	for _, candidate := range findings {
		overlaps := false
		for i := len(kept) - 1; i >= 0; i-- {
			prior := kept[i]
			if prior.End <= candidate.Start {
				break
			}
			if prior.Risk == candidate.Risk && candidate.Start < prior.End {
				overlaps = true
				break
			}
		}
		if !overlaps {
			kept = append(kept, candidate)
		}
	}
	return kept
}

// RedactCredentials removes only credential findings. Clinical content is
// intentionally preserved; its access is governed by classification.
func RedactCredentials(text string) (string, []Finding) {
	findings := Analyze(text).For(RiskCredential)
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
