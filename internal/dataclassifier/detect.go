// Package dataclassifier is a narrow, deterministic backstop against
// admission attestations that declare a data class the content plainly is
// not. It does not replace upstream classification (the boundary that
// produced the content is still the authority on intent and context) and it
// cannot catch everything a human reviewer would — it exists only to reject
// the specific failure mode of a caller-declared DataClass going
// unverified: an obvious secret or clinical marker sailing through under a
// "public" or "organizational" label because nothing ever looked at the
// content itself.
package dataclassifier

import "regexp"

// Finding names the risk category a pattern matched, along with a
// human-readable reason suitable for an error message. It intentionally
// never includes the matched text itself, since that text may be the
// secret or clinical detail being flagged.
type Finding struct {
	Secret         bool
	SecretReason   string
	Clinical       bool
	ClinicalReason string
}

// Any reports whether either risk category was detected.
func (f Finding) Any() bool { return f.Secret || f.Clinical }

var secretPatterns = []struct {
	reason string
	re     *regexp.Regexp
}{
	{"PEM private key block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"AWS access key ID", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"GitHub token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`)},
	{"Slack token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
	{"JSON Web Token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)},
	{"bearer token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9\-_.]{20,}\b`)},
	{"credential assignment", regexp.MustCompile(`(?i)(api[_-]?key|secret[_-]?key|access[_-]?token|private[_-]?key|client[_-]?secret|password)\s*[:=]\s*['"]?[A-Za-z0-9/_\-+=]{8,}['"]?`)},
}

var clinicalKeywords = []string{
	"diagnóstico", "diagnostico", "diagnosis",
	"paciente", "patient",
	"historia clínica", "historia clinica", "medical history", "medical record", "clinical record",
	"receta médica", "receta medica", "prescription",
	"expediente médico", "expediente medico",
	"tratamiento médico", "tratamiento medico",
	"medicación", "medicacion", "medication",
	"síntoma", "sintoma", "symptom",
	"patología", "patologia", "pathology",
}

var clinicalWordPattern = regexp.MustCompile(`(?i)\b(` + joinAlternatives(clinicalKeywords) + `)\b`)

func joinAlternatives(words []string) string {
	out := ""
	for i, w := range words {
		if i > 0 {
			out += "|"
		}
		out += regexp.QuoteMeta(w)
	}
	return out
}

// Detect scans body for deterministic secret-like and clinical-like
// patterns. It is safe to call on arbitrarily large bodies; it does not
// allocate proportionally to the number of matches.
func Detect(body string) Finding {
	var f Finding
	for _, p := range secretPatterns {
		if p.re.MatchString(body) {
			f.Secret = true
			f.SecretReason = p.reason
			break
		}
	}
	if loc := clinicalWordPattern.FindString(body); loc != "" {
		f.Clinical = true
		f.ClinicalReason = "clinical terminology marker"
	}
	return f
}
