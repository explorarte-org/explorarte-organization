package postrun

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/completion"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
)

// correctionPending is the honest placeholder every proposed candidate
// carries: this job observes and proposes, it does not author the
// corrective insight. docs/canonical/memory-policy.yaml's own
// canonical_write_flow puts that judgment on the human reviewer before an
// entry can reach StatusApproved.
const correctionPending = "Pendiente de revisión humana: evaluar las obligaciones incumplidas listadas en el problema y decidir la corrección."

const proposedByCategory = "completion_verification"

type Service struct {
	traces  TraceReader
	verify  Verifier
	roles   RoleResolver
	lessons LessonProposer
}

func NewService(traces TraceReader, verify Verifier, roles RoleResolver, lessons LessonProposer) (*Service, error) {
	if traces == nil || verify == nil || roles == nil || lessons == nil {
		return nil, errors.New("postrun service dependencies are incomplete")
	}
	return &Service{traces: traces, verify: verify, roles: roles, lessons: lessons}, nil
}

// ProcessRun reads the terminal decisiongraph run identified by runID,
// independently re-verifies its completion obligations, and proposes a
// memory candidate when — and only when — that re-verification finds a
// real, non-pass problem and the task's own role is authorized to propose
// one. It is safe to call more than once for the same run.
func (s *Service) ProcessRun(ctx context.Context, organizationID string, runID int64) (Outcome, error) {
	if runID <= 0 {
		return Outcome{}, fmt.Errorf("postrun: invalid run id %d", runID)
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return Outcome{}, errors.New("postrun: organization id is required")
	}

	summary, err := s.traces.RunSummary(ctx, runID)
	if err != nil {
		return Outcome{}, fmt.Errorf("postrun: load run summary: %w", err)
	}

	result, err := s.verify.Verify(ctx, completion.VerificationRequest{TaskID: summary.TaskID, AttemptID: summary.AttemptID})
	if err != nil {
		return Outcome{}, fmt.Errorf("postrun: re-verify completion: %w", err)
	}
	if result.Verdict == completion.VerdictPass {
		return Outcome{Kind: KindSkippedPass}, nil
	}

	roleID, err := s.roles.AssignedRoleID(ctx, summary.TaskID)
	if err != nil {
		return Outcome{}, fmt.Errorf("postrun: resolve assigned role: %w", err)
	}

	problem := problemFromObligations(result)
	entry, reused, err := s.lessons.Propose(ctx, memory.ProposeRequest{
		Command: memory.ProposeCommand{
			ID:             fmt.Sprintf("postrun-run-%d", runID),
			OrganizationID: organizationID,
			RoleID:         roleID,
			Category:       proposedByCategory,
			Problem:        problem,
			Correction:     correctionPending,
			SourceKind:     memory.SourceOperational,
			SourceRunID:    runID,
			EvidenceRefs: []memory.EvidenceRef{{
				Reference: fmt.Sprintf("decisiongraph:run:%d", runID),
				Digest:    summary.TraceHash,
			}},
			ProposedBy: roleID,
			Admission: memory.AdmissionAttestation{
				DataClass:      memory.DataOrganizational,
				AttestedBy:     roleID,
				SourceBoundary: "internal/executive/postrun",
				EvidenceRef:    fmt.Sprintf("decisiongraph:run:%d", runID),
				// Deterministic and stable across repeated ProcessRun calls
				// for the same run — required for the idempotency guarantee
				// this method documents (memory.Entry.CanonicalHash includes
				// the whole Admission struct, so a wall-clock "now" here
				// would make every call look like a content conflict).
				AttestedAt: summary.TerminalAt,
			},
		},
		IdempotencyKey: fmt.Sprintf("postrun:decision-run:%d", runID),
	})
	if err != nil {
		if errors.Is(err, authorization.ErrCapabilityDenied) {
			return Outcome{Kind: KindSkippedRoleNotEligible}, nil
		}
		return Outcome{}, fmt.Errorf("postrun: propose memory candidate: %w", err)
	}
	if reused {
		return Outcome{Kind: KindReused, Entry: &entry}, nil
	}
	return Outcome{Kind: KindProposed, Entry: &entry}, nil
}

// problemFromObligations builds a deterministic, evidence-grounded Problem
// string from every obligation completion did not verify or infer. It
// never invents text completion itself did not produce.
func problemFromObligations(result completion.VerificationResult) string {
	unresolved := make([]completion.ObligationResult, 0, len(result.Obligations))
	for _, o := range result.Obligations {
		if o.Label != completion.LabelVerified && o.Label != completion.LabelInferred {
			unresolved = append(unresolved, o)
		}
	}
	sort.Slice(unresolved, func(i, j int) bool { return unresolved[i].Obligation < unresolved[j].Obligation })

	var b strings.Builder
	fmt.Fprintf(&b, "Verificación de completitud: %s.", result.Verdict)
	for _, o := range unresolved {
		fmt.Fprintf(&b, " [%s: %s] %s", o.Obligation, o.Label, o.Detail)
	}
	return b.String()
}
