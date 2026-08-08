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

type Clock interface {
	Now() time.Time
}

type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }
