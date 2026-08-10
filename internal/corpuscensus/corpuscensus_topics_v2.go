package corpuscensus

import (
	"regexp"
)

// titleTopicPatterns is Topic Classification V2 (owner decision, re-run
// section 8): Work-level classification from title (this package has no
// abstract field -- the harvester's `papers` schema does not carry one),
// not merely "collection=rag -> 4 fixed topics." A title matching more
// than one pattern gets more than one topic (owner decision: never force
// a single topic). This is still a deterministic keyword heuristic, not
// a model call -- it is a CENSUS SIGNAL, not a Knowledge approval (owner
// decision: explicitly reiterated in this re-run's own instructions).
// Keys are the organization's topic vocabulary; order does not matter,
// all patterns that match contribute.
var titleTopicPatterns = map[string]*regexp.Regexp{
	"software-architecture":    regexp.MustCompile(`(?i)\b(software architecture|microservice|bounded context|system design)\b`),
	"distributed-systems":      regexp.MustCompile(`(?i)\b(distributed system|consensus|replication|sharding|distributed training)\b`),
	"agentic-ai":               regexp.MustCompile(`(?i)\b(agentic|multi-agent|autonomous agent|llm agent|ai agent)\b`),
	"self-improvement":         regexp.MustCompile(`(?i)\b(self-improv|self-evolv|self-refin|self-correct)\b`),
	"agent-memory":             regexp.MustCompile(`(?i)\b(agent memory|memory (bank|module|augmented)|episodic memory|long-term memory)\b`),
	"memory-os":                regexp.MustCompile(`(?i)\b(memory os|memory system|working memory)\b`),
	"rag":                      regexp.MustCompile(`(?i)\b(retrieval[- ]augmented|\brag\b)\b`),
	"graph-rag":                regexp.MustCompile(`(?i)\bgraph[- ]?rag\b`),
	"hierarchical-rag":         regexp.MustCompile(`(?i)\bhierarchical (retrieval|rag)\b`),
	"multimodal-rag":           regexp.MustCompile(`(?i)\bmultimodal (retrieval|rag)\b`),
	"information-retrieval":    regexp.MustCompile(`(?i)\b(information retrieval|passage retrieval|document retrieval)\b`),
	"semantic-search":          regexp.MustCompile(`(?i)\bsemantic search\b`),
	"embeddings":               regexp.MustCompile(`(?i)\bembedding(s)?\b`),
	"context-engineering":      regexp.MustCompile(`(?i)\bcontext engineering\b`),
	"long-context":             regexp.MustCompile(`(?i)\blong[- ]context\b`),
	"context-compression":      regexp.MustCompile(`(?i)\b(context|prompt) compress`),
	"token-efficiency":         regexp.MustCompile(`(?i)\btoken (efficien|optimiz|reduc|budget)`),
	"reinforcement-learning":   regexp.MustCompile(`(?i)\b(reinforcement learning|\brl\b|policy gradient|reward model)\b`),
	"symbolic-ai":              regexp.MustCompile(`(?i)\bsymbolic (ai|reasoning|logic)\b`),
	"prolog":                   regexp.MustCompile(`(?i)\bprolog\b|\bdatalog\b`),
	"formal-reasoning":         regexp.MustCompile(`(?i)\bformal (reasoning|verification|logic|method)\b`),
	"knowledge-representation": regexp.MustCompile(`(?i)\bknowledge (representation|graph)\b`),
	"ontologies":               regexp.MustCompile(`(?i)\bontolog`),
	"ml-systems":               regexp.MustCompile(`(?i)\b(ml system|machine learning system|inference (serving|system)|model serving)\b`),
	"deep-learning":            regexp.MustCompile(`(?i)\bdeep learning\b|\bneural network`),
	"asr":                      regexp.MustCompile(`(?i)\b(automatic speech recognition|\basr\b|speech recognition)\b`),
	"speaker-diarization":      regexp.MustCompile(`(?i)\bdiarization\b|\bspeaker (identification|verification)\b`),
	"continual-learning":       regexp.MustCompile(`(?i)\bcontinual learning\b|\blifelong learning\b|\bcatastrophic forgetting\b`),
	"observability":            regexp.MustCompile(`(?i)\bobservability\b`),
	"llm-observability":        regexp.MustCompile(`(?i)\bllm observability\b`),
	"agent-observability":      regexp.MustCompile(`(?i)\bagent observability\b`),
	"sociotechnical-systems":   regexp.MustCompile(`(?i)\bsociotechnical\b|\bsocio-technical\b`),
}

// TopicsForTitle applies every pattern in titleTopicPatterns against a
// Work's title and returns every topic that matched, sorted for
// determinism. Empty result is valid and expected for many titles --
// this package never forces a fallback topic here (TopicsForCollection
// still supplies the collection-derived signal as a floor).
func TopicsForTitle(title string) []string {
	var matched []string
	for topic, pattern := range titleTopicPatterns {
		if pattern.MatchString(title) {
			matched = append(matched, topic)
		}
	}
	return matched
}

// CombineTopics merges the collection-derived signal (TopicsForCollection
// -- a real, direct fact: which catalog search found this Work) with the
// title-derived signal (TopicsForTitle -- a heuristic, richer, Work-level
// signal), deduplicated and sorted, per owner decision that a Work may
// carry multiple topics and losing either signal would be a regression
// from the first census.
func CombineTopics(collection, title string) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(topics []string) {
		for _, t := range topics {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	add(TopicsForCollection(collection))
	add(TopicsForTitle(title))
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// AllTopics is the organization's full target topic vocabulary (owner
// decision, re-run section 8), including the two -- agent-memory,
// memory-os -- that only TopicsForCollection can ever assign (no title
// keyword pattern reliably distinguishes them, so they are intentionally
// absent from titleTopicPatterns and rely on the collection=memory
// signal instead) plus one collection-only entry retained for the same
// reason. Used by coverage-gap analysis to report "missing" (0 Works)
// honestly instead of only listing topics that happened to get a hit.
var AllTopics = []string{
	"software-architecture", "distributed-systems", "agentic-ai", "self-improvement",
	"agent-memory", "memory-os", "rag", "graph-rag", "hierarchical-rag", "multimodal-rag",
	"information-retrieval", "semantic-search", "embeddings", "context-engineering",
	"long-context", "context-compression", "token-efficiency", "reinforcement-learning",
	"symbolic-ai", "prolog", "formal-reasoning", "knowledge-representation", "ontologies",
	"ml-systems", "deep-learning", "asr", "speaker-diarization", "continual-learning",
	"observability", "llm-observability", "agent-observability", "sociotechnical-systems",
}
