package webevidence

import (
	"fmt"
	"regexp"
	"strings"
)

// injectionPatterns are phrases commonly used to try to hijack an agent
// reading untrusted content into treating it as instructions. Detection
// here is audit-only — it never blocks ingestion and never changes how a
// chunk is classified: every chunk this package produces is
// InstructionData/TrustUntrusted unconditionally (see the contextengine-
// shaped rendering a caller applies on top of this package's output),
// whether or not it matches one of these patterns. A pattern list can
// never be a complete defense against prompt injection; it is a detection
// and audit signal, not the security boundary itself — the boundary is
// that this package's output can never be treated as InstructionClass
// other than "data", by construction.
var injectionPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"ignore_prior_instructions", regexp.MustCompile(`(?i)ignore\s+(all\s+|any\s+)?(previous|prior|above|earlier)\s+instructions?`)},
	{"disregard_prior", regexp.MustCompile(`(?i)disregard\s+(all\s+|any\s+)?(previous|prior|above|earlier)`)},
	{"role_override", regexp.MustCompile(`(?i)you\s+are\s+now\s+(a|an)\b`)},
	{"system_prompt_reference", regexp.MustCompile(`(?i)system\s+prompt`)},
	{"new_instructions_marker", regexp.MustCompile(`(?i)new\s+instructions?\s*:`)},
	{"reveal_instructions", regexp.MustCompile(`(?i)reveal\s+(your|the)\s+(system\s+prompt|instructions)`)},
	{"instruction_delimiter", regexp.MustCompile(`(?i)###\s*(instruction|system)`)},
	{"assistant_impersonation", regexp.MustCompile(`(?i)\bassistant\s*:\s*i\s+will\b`)},
}

const maxFindingExcerptBytes = 200

// maxChunkBytes bounds a single ephemeral chunk — deliberately small: web
// evidence chunks feed a per-task, per-query ranking (see rank.go), not a
// permanent index, so there is no benefit to large chunks the way there
// might be for approved-knowledge retrieval.
const maxChunkBytes = 1200

// Sanitize splits page's body into bounded, ephemeral chunks and scans
// each for prompt-injection-shaped patterns. It never truncates silently
// mid-rune and never returns a chunk larger than maxChunkBytes.
func Sanitize(page RawPage) ([]Chunk, []SanitizationFinding, error) {
	text := strings.TrimSpace(string(page.Body))
	if text == "" {
		return nil, nil, fmt.Errorf("%w: page body is empty after trimming", ErrInvalidEvidence)
	}
	chunks := chunkText(text, maxChunkBytes)
	var findings []SanitizationFinding
	for _, chunk := range chunks {
		findings = append(findings, detectInjection(chunk)...)
	}
	return chunks, findings, nil
}

func chunkText(text string, maxBytes int) []Chunk {
	runes := []rune(text)
	var chunks []Chunk
	ordinal := 0
	start := 0
	for start < len(runes) {
		end := start
		size := 0
		for end < len(runes) {
			runeLen := len(string(runes[end]))
			if size+runeLen > maxBytes && end > start {
				break
			}
			size += runeLen
			end++
		}
		chunkText := strings.TrimSpace(string(runes[start:end]))
		if chunkText != "" {
			chunks = append(chunks, Chunk{Ordinal: ordinal, Text: chunkText})
			ordinal++
		}
		start = end
	}
	return chunks
}

func detectInjection(chunk Chunk) []SanitizationFinding {
	var findings []SanitizationFinding
	for _, candidate := range injectionPatterns {
		loc := candidate.pattern.FindStringIndex(chunk.Text)
		if loc == nil {
			continue
		}
		excerptStart := loc[0]
		excerptEnd := loc[1]
		if excerptEnd-excerptStart > maxFindingExcerptBytes {
			excerptEnd = excerptStart + maxFindingExcerptBytes
		}
		findings = append(findings, SanitizationFinding{
			ChunkOrdinal: chunk.Ordinal, Pattern: candidate.name, Excerpt: chunk.Text[excerptStart:excerptEnd],
		})
	}
	return findings
}
