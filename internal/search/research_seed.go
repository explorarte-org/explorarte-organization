package search

import "time"

// InvestigacionSeedTopics is the research worker's starting agenda: one paper watch per operational
// department plus two for Investigación's own audit mandate. Topics are Track class every 6 hours,
// staggered an hour apart so a run at the top of every hour has a topic due. Their IDs are stable,
// so seeding is idempotent when the caller inserts only missing IDs.
func InvestigacionSeedTopics(now time.Time) []ResearchTopic {
	seeds := []struct {
		id, department, title, description string
		intent                             SearchIntent
		priority                           float64
	}{
		{"seed-servicios-digital-mental-health", "servicios",
			"Digital mental health interventions effectiveness and engagement",
			"Evidence on internet-delivered and app-based psychological interventions: outcomes, adherence, dropout and user engagement.",
			IntentAcademic, 0.7},
		{"seed-negocio-psychotherapy-retention", "negocio",
			"Psychotherapy patient retention dropout and access",
			"Research on outpatient psychotherapy dropout, retention, waiting lists and barriers to access to mental health care.",
			IntentAcademic, 0.6},
		{"seed-ingenieria-llm-agent-reliability", "ingenieria_ia",
			"LLM agent reliability verification and code generation evaluation",
			"Preprints on verifying LLM agent outputs, evaluating generated code, and failure modes of autonomous coding agents.",
			IntentPreprint, 0.7},
		{"seed-recursos-multi-agent-organizations", "recursos_agenticos",
			"Multi-agent LLM organizations role design delegation",
			"Preprints on organizing LLM agents into roles and hierarchies, delegation protocols, and agent self-improvement.",
			IntentPreprint, 0.6},
		{"seed-investigacion-metacognition-rumination", "investigacion",
			"Metacognition rumination inner speech and mental regulation",
			"Papers on metacognition, rumination, inner speech and self-regulation of mental states.",
			IntentAcademic, 0.6},
		{"seed-investigacion-rag-knowledge-quality", "investigacion",
			"Retrieval augmented generation knowledge quality and auditing",
			"Preprints on evaluating and auditing retrieval-augmented generation: knowledge base quality, staleness and hallucination.",
			IntentPreprint, 0.5},
	}
	topics := make([]ResearchTopic, 0, len(seeds))
	for i, seed := range seeds {
		topics = append(topics, ResearchTopic{
			ID: seed.id, OrganizationID: "explorarte", DepartmentID: seed.department,
			Title: seed.title, Description: seed.description,
			ResearchClass: ResearchTrack, Status: TopicStatusActive, Priority: seed.priority,
			AllowedIntents: []SearchIntent{seed.intent},
			Cadence:        6 * time.Hour, NoveltyWindow: 30 * 24 * time.Hour,
			NextCheckAt: now.Add(time.Duration(i) * time.Hour),
			CreatedBy:   "investigacion/research_worker_hourly", Reason: "seed agenda (owner activation 2026-09-28)",
		})
	}
	return topics
}
