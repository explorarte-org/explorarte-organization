package corpuscuration

// KnowledgeGapCandidate is the clean, extensible contract (owner
// decision, section 20/22: "definir contrato limpio/extensible" -- not
// every signal source needs to exist yet, and Cell Steward specifically
// does not exist in this branch). A future producer (RAG retrieval
// misses, QA failures, CEO Observer findings, a not-yet-built Cell
// Steward) creates one of these; Investigación consumes it. Nothing in
// this package assumes any particular producer exists today.
type KnowledgeGapCandidate struct {
	Department  string `json:"department"`
	Topic       string `json:"topic"`
	Description string `json:"description"`
	// SignalSource names where this gap came from (e.g.
	// "rag_retrieval_miss", "qa_failure", "manual_curation_review",
	// "ceo_observer_finding") -- an open string, not a closed enum,
	// deliberately, since new signal sources will appear over time and
	// this contract must not need a code change for each one.
	SignalSource string   `json:"signal_source"`
	Priority     string   `json:"priority"` // "low" | "medium" | "high" -- advisory, never auto-escalates authority
	Evidence     []string `json:"evidence,omitempty"`
}

// TopicCoverageAssessment is one department's coverage of one topic --
// the unit a DepartmentKnowledgeProfile is built from. Tier mirrors
// internal/corpuscensus.TopicCoverage's own heuristic (strong/adequate/
// weak/missing) so the two stay comparable, but this package does not
// import corpuscensus to avoid a dependency cycle risk -- callers adapt
// at the boundary, same pattern as internal/corpuscluster.WorkInput.
type TopicCoverageAssessment struct {
	Topic           string `json:"topic"`
	WorkCount       int    `json:"work_count"`
	DistinctSources int    `json:"distinct_sources"`
	Tier            string `json:"tier"`
}

// DepartmentKnowledgeProfile is owner decision section 18: NOT limited
// to Engineering. Domains/Coverage here is computed from the corpus
// census's own TopicCoverage output (real numbers), not asserted by this
// package. PriorityGaps is populated by the caller from whichever
// TopicCoverageAssessment entries are "weak" or "missing" AND appear in
// OwnedTopics (a department only has a "priority gap" for topics its own
// roles are actually responsible for -- a weak observability corpus is
// not ingenieria_ia's gap if no role there owns observability).
type DepartmentKnowledgeProfile struct {
	Department   string                    `json:"department"`
	OwnedTopics  []string                  `json:"owned_topics"` // derived from role rag_topics_source_text, not hardcoded
	Coverage     []TopicCoverageAssessment `json:"coverage"`
	PriorityGaps []string                  `json:"priority_gaps"`
}

// BuildDepartmentKnowledgeProfile computes a profile from real inputs:
// the department's owned topics (derived elsewhere, from the canonical
// role catalog -- never hardcoded here) and the corpus's own topic
// coverage assessment. A topic in ownedTopics with no matching entry in
// coverage is treated as "missing" (work_count=0), not silently skipped
// -- an owned topic with zero corpus coverage is exactly the gap this
// function exists to surface.
func BuildDepartmentKnowledgeProfile(department string, ownedTopics []string, coverage []TopicCoverageAssessment) DepartmentKnowledgeProfile {
	byTopic := make(map[string]TopicCoverageAssessment, len(coverage))
	for _, c := range coverage {
		byTopic[c.Topic] = c
	}
	profile := DepartmentKnowledgeProfile{Department: department, OwnedTopics: ownedTopics}
	for _, topic := range ownedTopics {
		assessment, ok := byTopic[topic]
		if !ok {
			assessment = TopicCoverageAssessment{Topic: topic, Tier: "missing"}
		}
		profile.Coverage = append(profile.Coverage, assessment)
		if assessment.Tier == "weak" || assessment.Tier == "missing" {
			profile.PriorityGaps = append(profile.PriorityGaps, topic)
		}
	}
	return profile
}
