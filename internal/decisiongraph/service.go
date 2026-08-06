package decisiongraph

import (
	"context"
	"errors"
	"fmt"
)

type Service struct {
	ledger Ledger
	clock  Clock
}

func NewService(ledger Ledger, clock Clock) (*Service, error) {
	if ledger == nil {
		return nil, errors.New("decisiongraph service requires a ledger")
	}
	if clock == nil {
		clock = SystemClock{}
	}
	return &Service{ledger: ledger, clock: clock}, nil
}

func (s *Service) CreateRun(ctx context.Context, request CreateRunRequest) (Run, error) {
	now := s.clock.Now().UTC()
	if err := request.Validate(now); err != nil {
		return Run{}, err
	}
	run, err := s.ledger.CreateRun(ctx, request, now)
	if err != nil {
		return Run{}, fmt.Errorf("create decision graph run: %w", err)
	}
	return run, nil
}

func (s *Service) AppendGraph(ctx context.Context, request AppendGraphRequest) (GraphVersion, error) {
	if _, _, _, err := request.Validate(); err != nil {
		return GraphVersion{}, err
	}
	version, err := s.ledger.AppendGraph(ctx, request)
	if err != nil {
		return GraphVersion{}, fmt.Errorf("append decision graph: %w", err)
	}
	return version, nil
}

func (s *Service) StartRun(ctx context.Context, runID int64) error {
	if runID <= 0 {
		return fmt.Errorf("%w: invalid run id", ErrInvalidRun)
	}
	if err := s.ledger.StartRun(ctx, runID, s.clock.Now().UTC()); err != nil {
		return fmt.Errorf("start decision graph run: %w", err)
	}
	return nil
}

func (s *Service) ClaimReadyNode(ctx context.Context, request ClaimNodeRequest) (NodeClaim, error) {
	if err := request.Validate(); err != nil {
		return NodeClaim{}, err
	}
	claim, err := s.ledger.ClaimReadyNode(ctx, request, s.clock.Now().UTC())
	if err != nil {
		return NodeClaim{}, fmt.Errorf("claim decision node: %w", err)
	}
	return claim, nil
}

func (s *Service) FinishExecution(ctx context.Context, request FinishExecutionRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := s.ledger.FinishExecution(ctx, request, s.clock.Now().UTC()); err != nil {
		return fmt.Errorf("finish decision execution: %w", err)
	}
	return nil
}

func (s *Service) RecordObservation(ctx context.Context, record ObservationRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if err := s.ledger.RecordObservation(ctx, record, s.clock.Now().UTC()); err != nil {
		return fmt.Errorf("record decision observation: %w", err)
	}
	return nil
}

func (s *Service) RecordVerification(ctx context.Context, record VerificationRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if err := s.ledger.RecordVerification(ctx, record, s.clock.Now().UTC()); err != nil {
		return fmt.Errorf("record decision verification: %w", err)
	}
	return nil
}

func (s *Service) RecordTerminalDecision(ctx context.Context, request TerminalDecisionRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if request.VerificationLabel != VerificationVerified && request.VerificationLabel != VerificationInferred {
		return fmt.Errorf("%w: terminal selection requires verified or inferred label", ErrInvalidDecision)
	}
	if err := s.ledger.RecordTerminalDecision(ctx, request, s.clock.Now().UTC()); err != nil {
		return fmt.Errorf("record terminal decision: %w", err)
	}
	return nil
}

func (s *Service) RecoverExpiredExecutions(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 256 {
		return 0, fmt.Errorf("%w: recovery limit must be between 1 and 256", ErrInvalidExecution)
	}
	recovered, err := s.ledger.RecoverExpiredExecutions(ctx, limit, s.clock.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("recover expired decision executions: %w", err)
	}
	return recovered, nil
}

func (s *Service) TraceRef(ctx context.Context, runID int64) (TraceRef, error) {
	if runID <= 0 {
		return TraceRef{}, fmt.Errorf("%w: invalid run id", ErrInvalidRun)
	}
	ref, err := s.ledger.TraceRef(ctx, runID)
	if err != nil {
		return TraceRef{}, fmt.Errorf("load decision trace reference: %w", err)
	}
	return ref, nil
}
