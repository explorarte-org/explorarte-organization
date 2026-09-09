package search

import (
	"fmt"
	"strings"
	"time"
)

// =============================================================================
// Autonomous Research Agenda V1 — domain types
//
// Mission-independent research: the Investigator keeps a persistent agenda of
// topics per department, researches continuously, and feeds Department
// Intelligence. Missions may CONSUME findings later, but never originate
// research. The concepts below are deliberately separate stores:
//
//	KnowledgeNeed    — what the organization needs to know
//	ResearchTopic    — what we are watching/tracking/deep-diving
//	ResearchCycle    — one concrete execution of the Investigator
//	ResearchFinding  — what NEW knowledge a cycle produced
//	Knowledge RAG    — durable, governed knowledge (NOT here)
// =============================================================================

// SearchIntent.Valid reports whether the intent is one of the canonical V1
// intents. This is a minimal, additive contract extension required by the
// research agenda: topics validate their AllowedIntents at Save time so a
// typo cannot silently produce a topic the Router will always deny. It
// changes no routing behavior; the Router's policy engine remains the sole
// authority at search time.
func (s SearchIntent) Valid() bool {
	switch s {
	case IntentWebGeneral, IntentNews, IntentAcademic, IntentBiomedical,
		IntentPreprint, IntentBookGeneral, IntentBookAcademicOA,
		IntentBookPublicDomain, IntentDOIResolve, IntentOpenAccessResolve:
		return true
	}
	return false
}

// -----------------------------------------------------------------------------
// KnowledgeNeed
// -----------------------------------------------------------------------------

// KnowledgeNeedSource is where a knowledge need came from.
type KnowledgeNeedSource string

const (
	NeedSourceDepartmentRequested  KnowledgeNeedSource = "department_requested"
	NeedSourceSystemDetected       KnowledgeNeedSource = "system_detected"
	NeedSourceKnowledgeGap         KnowledgeNeedSource = "knowledge_gap"
	NeedSourceStaleKnowledge       KnowledgeNeedSource = "stale_knowledge"
	NeedSourceStrategicWatch       KnowledgeNeedSource = "strategic_watch"
	NeedSourceResearcherDiscovered KnowledgeNeedSource = "researcher_discovered"
)

// Valid reports whether the source is canonical.
func (s KnowledgeNeedSource) Valid() bool {
	switch s {
	case NeedSourceDepartmentRequested, NeedSourceSystemDetected, NeedSourceKnowledgeGap,
		NeedSourceStaleKnowledge, NeedSourceStrategicWatch, NeedSourceResearcherDiscovered:
		return true
	}
	return false
}

// NeedStatus is the lifecycle of a KnowledgeNeed.
type NeedStatus string

const (
	NeedStatusActive    NeedStatus = "active"
	NeedStatusSatisfied NeedStatus = "satisfied"
	NeedStatusExpired   NeedStatus = "expired"
	NeedStatusArchived  NeedStatus = "archived"
)

// Valid reports whether the status is canonical.
func (s NeedStatus) Valid() bool {
	switch s {
	case NeedStatusActive, NeedStatusSatisfied, NeedStatusExpired, NeedStatusArchived:
		return true
	}
	return false
}

// KnowledgeNeed is a department-level need for knowledge. It is NOT a query:
// one need may produce many ResearchTopics over time.
type KnowledgeNeed struct {
	ID              string
	OrganizationID  string
	DepartmentID    string // canonical kernel department/unit ID
	Question        string
	Description     string
	Importance      float64 // 0..1 deterministic weight, host-assigned
	Confidence      float64 // 0..1
	Status          NeedStatus
	Source          KnowledgeNeedSource
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastSatisfiedAt *time.Time
	ExpiresAt       *time.Time
}

// Validate enforces the structural contract of a need.
func (n KnowledgeNeed) Validate() error {
	if strings.TrimSpace(n.ID) == "" {
		return fmt.Errorf("%w: knowledge need id is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(n.DepartmentID) == "" {
		return fmt.Errorf("%w: knowledge need department is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(n.Question) == "" {
		return fmt.Errorf("%w: knowledge need question is required", ErrInvalidRequest)
	}
	if n.Status != "" && !n.Status.Valid() {
		return fmt.Errorf("%w: unknown knowledge need status %q", ErrInvalidRequest, n.Status)
	}
	if !n.Source.Valid() {
		return fmt.Errorf("%w: unknown knowledge need source %q", ErrInvalidRequest, n.Source)
	}
	return nil
}

// -----------------------------------------------------------------------------
// ResearchTopic
// -----------------------------------------------------------------------------

// ResearchClass is the vigilance semantics of a topic. It drives cadence.
type ResearchClass string

const (
	// ResearchWatch: fast-changing info (APIs, models, pricing, outages,
	// critical releases, relevant CVEs). Cadence: minutes/hours.
	ResearchWatch ResearchClass = "watch"
	// ResearchTrack: moderate evolution (frameworks, trends, competitors,
	// platform behavior, tools). Cadence: daily/weekly.
	ResearchTrack ResearchClass = "track"
	// ResearchDeep: cumulative research (papers, books, meta-analyses,
	// state of the art). Never auto-runs every tick; runs only when due
	// and budget/capacity allow.
	ResearchDeep ResearchClass = "deep"
)

// Valid reports whether the class is canonical.
func (c ResearchClass) Valid() bool {
	switch c {
	case ResearchWatch, ResearchTrack, ResearchDeep:
		return true
	}
	return false
}

// TopicStatus is the lifecycle of a ResearchTopic.
type TopicStatus string

const (
	TopicStatusActive    TopicStatus = "active"
	TopicStatusPaused    TopicStatus = "paused"
	TopicStatusCompleted TopicStatus = "completed"
	TopicStatusArchived  TopicStatus = "archived"
)

// Valid reports whether the status is canonical.
func (s TopicStatus) Valid() bool {
	switch s {
	case TopicStatusActive, TopicStatusPaused, TopicStatusCompleted, TopicStatusArchived:
		return true
	}
	return false
}

// ResearchTopic is a persistent vigilance/research topic. It must be able to
// live WITHOUT a MissionID — mission linkage, if any, is optional metadata.
type ResearchTopic struct {
	ID             string
	OrganizationID string
	DepartmentID   string

	Title       string
	Description string

	Priority      float64 // 0..1 base priority, host-assigned
	ResearchClass ResearchClass
	Status        TopicStatus

	// AllowedIntents constrains which SearchRouter intents this topic may
	// use. The Router still enforces role policy on top of this.
	AllowedIntents []SearchIntent

	// Cadence is how often this topic should be checked. The scheduler
	// evaluates NextCheckAt against now; the global tick never overrides
	// the topic's own cadence.
	Cadence time.Duration

	LastCheckedAt *time.Time
	NextCheckAt   time.Time

	// NoveltyWindow bounds how far back evidence is compared for novelty.
	NoveltyWindow time.Duration

	CreatedBy string // canonical role/principal ID, host-managed
	Reason    string

	// ParentKnowledgeNeedID links the topic to the need that originated it.
	// Optional: strategic-watch topics may exist without a parent need.
	ParentKnowledgeNeedID string

	// MissionID is OPTIONAL metadata. It is never part of the identity and
	// the scheduler never queries it.
	MissionID string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate enforces the structural contract of a topic.
func (t ResearchTopic) Validate() error {
	if strings.TrimSpace(t.ID) == "" && strings.TrimSpace(t.Title) == "" {
		return fmt.Errorf("%w: research topic requires id or title", ErrInvalidRequest)
	}
	if strings.TrimSpace(t.DepartmentID) == "" {
		return fmt.Errorf("%w: research topic department is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(t.Title) == "" {
		return fmt.Errorf("%w: research topic title is required", ErrInvalidRequest)
	}
	if !t.ResearchClass.Valid() {
		return fmt.Errorf("%w: unknown research class %q", ErrInvalidRequest, t.ResearchClass)
	}
	if t.Status != "" && !t.Status.Valid() {
		return fmt.Errorf("%w: unknown topic status %q", ErrInvalidRequest, t.Status)
	}
	if t.Cadence < time.Minute {
		return fmt.Errorf("%w: research topic cadence must be at least 1 minute", ErrInvalidRequest)
	}
	if len(t.AllowedIntents) == 0 {
		return fmt.Errorf("%w: research topic must allow at least one intent", ErrInvalidRequest)
	}
	for _, intent := range t.AllowedIntents {
		if !intent.Valid() {
			return fmt.Errorf("%w: unknown intent %q in topic", ErrInvalidRequest, intent)
		}
	}
	return nil
}

// Due reports whether the topic should run at the given time. Only ACTIVE
// topics can ever be due — paused/completed/archived never run.
func (t ResearchTopic) Due(now time.Time) bool {
	if t.Status != TopicStatusActive && t.Status != "" {
		return false
	}
	return !now.Before(t.NextCheckAt)
}

// -----------------------------------------------------------------------------
// ResearchCycle
// -----------------------------------------------------------------------------

// ResearchTrigger records what caused a cycle to start.
type ResearchTrigger string

const (
	TriggerScheduler ResearchTrigger = "scheduler"
	TriggerManual    ResearchTrigger = "manual"
	TriggerMission   ResearchTrigger = "mission" // optional metadata only
)

// CycleOutcome is the terminal state of a cycle.
type CycleOutcome string

const (
	CycleOutcomeCompleted      CycleOutcome = "completed"
	CycleOutcomeNoNewInfo      CycleOutcome = "no_new_information"
	CycleOutcomePostponed      CycleOutcome = "postponed"
	CycleOutcomeFailed         CycleOutcome = "failed"
	CycleOutcomeAbortedLoop    CycleOutcome = "aborted_loop_limit"
	CycleOutcomeLLMUnavailable CycleOutcome = "llm_unavailable"
)

// ResearchCycle is one concrete execution of the Investigator for a topic.
// It never requires a MissionID.
type ResearchCycle struct {
	ID                   string
	OrganizationID       string
	TopicID              string
	DepartmentID         string
	Trigger              ResearchTrigger
	StartedAt            time.Time
	CompletedAt          *time.Time
	QueriesAttempted     int
	ProvidersUsed        []string
	ResultCount          int
	NewEvidenceCount     int
	DuplicateCount       int
	FindingsCreated      int
	RAGCandidatesCreated int
	Outcome              CycleOutcome
	ErrorClass           string
	// MissionID is optional metadata only.
	MissionID string
}

// -----------------------------------------------------------------------------
// ResearchFinding
// -----------------------------------------------------------------------------

// FindingClassification drives propagation semantics.
type FindingClassification string

const (
	// FindingIrrelevant: do not propagate.
	FindingIrrelevant FindingClassification = "irrelevant"
	// FindingInformative: available in the intelligence feed.
	FindingInformative FindingClassification = "informative"
	// FindingImportant: highlight to the department (event). NOT automatically
	// durable — important != durable.
	FindingImportant FindingClassification = "important"
	// FindingCritical: priority notification/event.
	FindingCritical FindingClassification = "critical"
	// FindingDurableCandidate: may generate a RAG candidate (governance then
	// decides promotion; the Researcher can NEVER promote directly).
	FindingDurableCandidate FindingClassification = "durable_candidate"
)

// Valid reports whether the classification is canonical.
func (c FindingClassification) Valid() bool {
	switch c {
	case FindingIrrelevant, FindingInformative, FindingImportant,
		FindingCritical, FindingDurableCandidate:
		return true
	}
	return false
}

// Propagates reports whether the finding enters the department feed.
func (c FindingClassification) Propagates() bool {
	return c != FindingIrrelevant
}

// EvidenceRef is a safe provenance pointer for a finding.
type EvidenceRef struct {
	Provider string
	URL      string
	DOI      string
	ArxivID  string
	ISBN     string
	Title    string
	FoundAt  time.Time
}

// ResearchFinding is NEW knowledge detected in a cycle, with provenance.
type ResearchFinding struct {
	ID              string
	OrganizationID  string
	TopicID         string
	DepartmentID    string
	ResearchCycleID string

	Title   string
	Summary string

	Importance float64 // 0..1
	Novelty    float64 // 0..1
	Confidence float64 // 0..1

	// EvidenceRefs carry provenance: canonical URLs, DOIs, arXiv IDs,
	// provider names. Never raw provider payloads.
	EvidenceRefs []EvidenceRef

	Classification FindingClassification

	// MissionID is optional metadata only.
	MissionID string

	CreatedAt time.Time
}

// Validate enforces the structural contract of a finding.
func (f ResearchFinding) Validate() error {
	if strings.TrimSpace(f.ID) == "" {
		return fmt.Errorf("%w: finding id is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(f.TopicID) == "" {
		return fmt.Errorf("%w: finding topic is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(f.DepartmentID) == "" {
		return fmt.Errorf("%w: finding department is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(f.ResearchCycleID) == "" {
		return fmt.Errorf("%w: finding cycle is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(f.Title) == "" {
		return fmt.Errorf("%w: finding title is required", ErrInvalidRequest)
	}
	if !f.Classification.Valid() {
		return fmt.Errorf("%w: unknown finding classification %q", ErrInvalidRequest, f.Classification)
	}
	return nil
}

// -----------------------------------------------------------------------------
// ResearchTopicProposal
// -----------------------------------------------------------------------------

// ProposalDecision is the host-side deterministic outcome of a topic proposal.
type ProposalDecision string

const (
	ProposalAccepted ProposalDecision = "autoaccepted"
	ProposalPending  ProposalDecision = "pending_review"
	ProposalRejected ProposalDecision = "rejected"
)

// ProposalRejectReason explains why a proposal was not autoaccepted.
type ProposalRejectReason string

const (
	RejectDuplicate         ProposalRejectReason = "duplicate"
	RejectUnknownDepartment ProposalRejectReason = "unknown_department"
	RejectInvalidParent     ProposalRejectReason = "invalid_parent_need"
	RejectCadenceTooFast    ProposalRejectReason = "cadence_below_floor"
	RejectHighFrequency     ProposalRejectReason = "high_frequency_review"
	RejectCriticalPriority  ProposalRejectReason = "critical_priority_review"
	RejectInvalidTopic      ProposalRejectReason = "invalid_topic"
)

// ResearchTopicProposal is what the LLM may PRODUCE. The LLM can only propose;
// insertion into the productive agenda happens host-side via deterministic
// policy (TopicProposalPolicy).
type ResearchTopicProposal struct {
	ID                    string
	OrganizationID        string
	DepartmentID          string
	Title                 string
	Description           string
	ResearchClass         ResearchClass
	Cadence               time.Duration
	Priority              float64
	AllowedIntents        []SearchIntent
	ParentKnowledgeNeedID string
	ParentTopicID         string
	OriginCycleID         string
	Reason                string
	Decision              ProposalDecision
	RejectReason          ProposalRejectReason
	ReviewedBy            string
	CreatedAt             time.Time
	ReviewedAt            *time.Time
}

// -----------------------------------------------------------------------------
// Scheduler configuration (centralized, no scattered magic numbers)
// -----------------------------------------------------------------------------

// SchedulerConfig centralizes every autonomous-research budget knob.
type SchedulerConfig struct {
	// Enabled gates the whole scheduler (default false in tests; the
	// productive default is chosen at wiring time).
	Enabled bool

	// TickInterval is the global tick (default 10m). The tick does NOT mean
	// "search all topics every 10 minutes" — it means "evaluate due topics
	// every 10 minutes".
	TickInterval time.Duration

	// MaxTopicsPerTick bounds how many due topics run per tick.
	MaxTopicsPerTick int

	// MaxQueriesPerTopic bounds SearchRouter requests per topic cycle.
	MaxQueriesPerTopic int

	// MaxSearchRequestsPerTick bounds SearchRouter requests per tick overall.
	MaxSearchRequestsPerTick int

	// MaxLLMTurnsPerCycle bounds the researcher's LLM turns per cycle
	// (loop protection).
	MaxLLMTurnsPerCycle int

	// MaxTopicProposalsPerCycle bounds how many topics the researcher may
	// propose in a single cycle.
	MaxTopicProposalsPerCycle int

	// CadenceFloors is the minimum cadence per research class. A proposal
	// faster than its class floor is rejected or sent to review.
	CadenceFloors map[ResearchClass]time.Duration

	// AllowPaidFallbackForCritical gates paid LLM/provider fallback for
	// critical topics. V1 default: false (never paid automatically).
	AllowPaidFallbackForCritical bool

	// RecentCyclePenalty is applied per recent cycle when scoring due topics,
	// damping topics that have just run.
	RecentCyclePenalty float64

	// BudgetPressureWeight damps scoring as the tick budget is consumed.
	BudgetPressureWeight float64
}

// DefaultSchedulerConfig returns the conservative V1 defaults.
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		Enabled:                   false,
		TickInterval:              10 * time.Minute,
		MaxTopicsPerTick:          2,
		MaxQueriesPerTopic:        3,
		MaxSearchRequestsPerTick:  5,
		MaxLLMTurnsPerCycle:       4,
		MaxTopicProposalsPerCycle: 2,
		CadenceFloors: map[ResearchClass]time.Duration{
			ResearchWatch: 10 * time.Minute,
			ResearchTrack: 6 * time.Hour,
			ResearchDeep:  24 * time.Hour,
		},
		AllowPaidFallbackForCritical: false,
		RecentCyclePenalty:           0.25,
		BudgetPressureWeight:         0.1,
	}
}

// schedulerMinTickFloor is the production floor for the tick interval. It
// exists so a misconfigured deployment cannot hammer providers; tests lower
// it (never production code) to run ticks at test speed.
var schedulerMinTickFloor = time.Minute

// Validate enforces sane scheduler bounds.
func (c SchedulerConfig) Validate() error {
	if c.TickInterval < schedulerMinTickFloor {
		return fmt.Errorf("%w: scheduler tick below the minimum floor", ErrInvalidRequest)
	}
	if c.MaxTopicsPerTick < 1 || c.MaxTopicsPerTick > 50 {
		return fmt.Errorf("%w: max topics per tick outside 1..50", ErrInvalidRequest)
	}
	if c.MaxQueriesPerTopic < 1 || c.MaxQueriesPerTopic > 20 {
		return fmt.Errorf("%w: max queries per topic outside 1..20", ErrInvalidRequest)
	}
	if c.MaxSearchRequestsPerTick < c.MaxTopicsPerTick {
		return fmt.Errorf("%w: tick search budget below topic budget", ErrInvalidRequest)
	}
	if c.MaxLLMTurnsPerCycle < 1 || c.MaxLLMTurnsPerCycle > 50 {
		return fmt.Errorf("%w: max LLM turns per cycle outside 1..50", ErrInvalidRequest)
	}
	if c.MaxTopicProposalsPerCycle < 0 || c.MaxTopicProposalsPerCycle > 20 {
		return fmt.Errorf("%w: max topic proposals per cycle outside 0..20", ErrInvalidRequest)
	}
	for _, class := range []ResearchClass{ResearchWatch, ResearchTrack, ResearchDeep} {
		floor, ok := c.CadenceFloors[class]
		if !ok || floor < time.Minute {
			return fmt.Errorf("%w: cadence floor missing for class %q", ErrInvalidRequest, class)
		}
	}
	return nil
}
