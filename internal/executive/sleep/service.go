package sleep

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Service struct {
	reader   ExperienceReader
	proposer CandidateProposer
	clock    Clock
	config   Config
}

func NewService(reader ExperienceReader, proposer CandidateProposer, clock Clock, config Config) (*Service, error) {
	if reader == nil || proposer == nil {
		return nil, errors.New("sleep: reader and proposer are required")
	}
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Service{reader: reader, proposer: proposer, clock: clock, config: config}, nil
}

// RunCycle performs one bounded offline consolidation pass. It reads only
// already-durable execution evidence, proposes only RAG candidates, and never
// reviews, approves, reindexes, or publishes knowledge.
func (s *Service) RunCycle(ctx context.Context, organizationID string, window time.Duration) (CycleResult, error) {
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return CycleResult{}, errors.New("sleep: organization id is required")
	}
	if window <= 0 || window > s.config.MaxWindow {
		return CycleResult{}, fmt.Errorf("sleep: window must be between 0 and %s", s.config.MaxWindow)
	}
	if err := ctx.Err(); err != nil {
		return CycleResult{}, err
	}

	end := s.clock.Now().UTC()
	start := end.Add(-window)
	result := CycleResult{WindowStart: start, WindowEnd: end, Proposals: []ProposalResult{}, Failures: []ProposalFailure{}}

	experiences, err := s.reader.ListEligible(ctx, organizationID, start, end, s.config.MaxExperiences)
	if err != nil {
		return result, fmt.Errorf("sleep: list eligible experiences: %w", err)
	}
	result.EligibleExperiences = len(experiences)
	groups, err := GroupExperiences(experiences)
	if err != nil {
		return result, fmt.Errorf("sleep: group experiences: %w", err)
	}
	result.GroupsObserved = len(groups)
	recurring := RecurringGroups(groups, s.config.MinGroupSize)
	result.RecurringGroups = len(recurring)

	for _, group := range groups {
		if len(group.Experiences) < s.config.MinGroupSize {
			result.SkippedInsufficientRuns++
			continue
		}
		analysis := AnalyzeGroup(group, s.config.MinGroupSize)
		if analysis.PassRate <= mixedPassLowerBound {
			result.SkippedLowPassRate++
			continue
		}
		if analysis.Contradiction {
			result.MixedContradictionGroups++
		}

		candidate, err := BuildCandidate(group, recurring, analysis, s.config)
		if err != nil {
			result.Failures = append(result.Failures, ProposalFailure{Group: group.Key, Error: err.Error()})
			continue
		}
		candidate.Request.Command.OrganizationID = organizationID
		version, reused, err := s.proposer.Propose(ctx, candidate.Request)
		if err != nil {
			result.Failures = append(result.Failures, ProposalFailure{Group: group.Key, Error: err.Error()})
			continue
		}
		proposal := ProposalResult{
			Group: group.Key, VersionID: version.ID, DocumentID: version.DocumentID,
			Reused: reused, Confidence: candidate.Confidence,
			EvidenceRunIDs: append([]int64(nil), candidate.EvidenceRunIDs...),
		}
		result.Proposals = append(result.Proposals, proposal)
		if reused {
			result.CandidatesReused++
		} else {
			result.CandidatesProposed++
		}
	}
	return result, nil
}
