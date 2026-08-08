package sleep

import (
	"context"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

type ExperienceReader interface {
	ListEligible(ctx context.Context, organizationID string, from, to time.Time, limit int) ([]Experience, error)
}

type CandidateProposer interface {
	Propose(ctx context.Context, request rag.ProposeRequest) (rag.KnowledgeVersion, bool, error)
}

// EvidenceLedger reports which decision-graph runs have already been
// consolidated into knowledge, so ListEligible never needs direct SQL access
// to rag's own tables. Satisfied by *rag.Manager.
type EvidenceLedger interface {
	ExistingEvidenceReferences(ctx context.Context, organizationID, referencePrefix string) (map[string]bool, error)
}

type Clock interface {
	Now() time.Time
}

type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }
