// Package logicir defines the versioned intermediate representation (IR)
// that ADR-0006 (resolving D-007) decided Go will compile to for an
// eventual Prolog/Datalog shadow solver: "una representación intermedia
// tipada se compila hacia Prolog/Datalog aislado en shadow." Go remains
// the sole authoritative decision-maker; nothing in this package is wired
// to block or alter a production decision.
//
// R30's scope for this package is deliberately narrow — it is the
// "contrato" fixed by ADR-0006, not the solver itself (that is R34+):
// a versioned program/fact schema, the comparison-result and divergence
// shapes a future shadow run will produce, hard limits on that run, and a
// structural (not policy-based) guarantee that free-form text — in
// particular a model's private chain-of-thought — can never be
// represented by these types and therefore can never reach a solver or
// its storage through this contract.
package logicir

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// CurrentSchemaVersion is the only IR schema version this package
// produces or accepts. It is bumped, never silently reinterpreted, if the
// shape of Program changes.
const CurrentSchemaVersion = "logic-ir.v1"

// identifierPattern is deliberately the same shape as a logic
// predicate/atom, not free text: lowercase, digits, and a small set of
// structural separators. A model's reasoning trace cannot satisfy this
// pattern except by coincidence of a single short token, which is the
// point — this is a structural guarantee, not a content filter.
var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_./:-]{0,127}$`)

// deniedPredicates lists Prolog/Datalog builtins with side effects
// (process control, file I/O, network, dynamic code mutation). ADR-0006
// requires "prohibición de red, escritura de archivos, predicados
// peligrosos" at the contract level: a Program containing any of these as
// a predicate name is invalid IR, full stop, regardless of what a future
// compiler or solver would otherwise allow.
var deniedPredicates = map[string]struct{}{
	"shell": {}, "exec": {}, "system": {}, "popen": {}, "spawn": {}, "halt": {},
	"assert": {}, "asserta": {}, "assertz": {}, "retract": {}, "retractall": {},
	"open": {}, "close": {}, "read": {}, "write": {}, "read_term": {}, "write_term": {},
	"consult": {}, "see": {}, "seen": {}, "tell": {}, "told": {}, "load_files": {},
	"http": {}, "http_open": {}, "socket": {}, "tcp_connect": {}, "file": {},
}

var (
	ErrInvalidProgram = errors.New("logicir: invalid program")
	ErrInvalidLimits  = errors.New("logicir: invalid limits")
	ErrInvalidEvent   = errors.New("logicir: invalid event")
)

// Fact is one ground atom or rule head/body element: predicate(args...).
// Both Predicate and every element of Args must match identifierPattern —
// there is no string field wide enough to carry a sentence, a prompt, or
// a chain-of-thought trace.
type Fact struct {
	Predicate string   `json:"predicate"`
	Args      []string `json:"args,omitempty"`
}

func (f Fact) validate() error {
	if !identifierPattern.MatchString(f.Predicate) {
		return fmt.Errorf("%w: predicate %q is not a valid identifier", ErrInvalidProgram, f.Predicate)
	}
	if _, denied := deniedPredicates[f.Predicate]; denied {
		return fmt.Errorf("%w: predicate %q is a denied builtin", ErrInvalidProgram, f.Predicate)
	}
	if len(f.Args) > 32 {
		return fmt.Errorf("%w: predicate %q has too many arguments", ErrInvalidProgram, f.Predicate)
	}
	for _, arg := range f.Args {
		if !identifierPattern.MatchString(arg) {
			return fmt.Errorf("%w: argument %q of %q is not a valid identifier", ErrInvalidProgram, arg, f.Predicate)
		}
	}
	return nil
}

// Rule is Head :- Body (conjunction). An empty Body makes Rule equivalent
// to a Fact declared as a rule; Program keeps them separate so a future
// compiler can treat ground facts and derivation rules differently
// without re-deriving which is which.
type Rule struct {
	Head Fact   `json:"head"`
	Body []Fact `json:"body,omitempty"`
}

func (r Rule) validate() error {
	if err := r.Head.validate(); err != nil {
		return err
	}
	if len(r.Body) > 64 {
		return fmt.Errorf("%w: rule head %q has too large a body", ErrInvalidProgram, r.Head.Predicate)
	}
	for _, fact := range r.Body {
		if err := fact.validate(); err != nil {
			return err
		}
	}
	return nil
}

// Program is one versioned, self-contained IR unit compiled from a Go
// snapshot of decision-relevant facts (the same kind of ground truth
// internal/shadowverifier already re-derives for its fixed fact set —
// this type generalizes the wire shape, it does not replace that
// package). SourceHash lets a divergence or comparison event be traced
// back to exactly which Go-side snapshot produced this program, without
// the program itself needing to embed anything beyond identifiers.
type Program struct {
	SchemaVersion string `json:"schema_version"`
	ProgramID     string `json:"program_id"`
	SourceHash    string `json:"source_hash"`
	Facts         []Fact `json:"facts,omitempty"`
	Rules         []Rule `json:"rules,omitempty"`
}

func (p Program) Validate() error {
	if p.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("%w: schema version %q, want %q", ErrInvalidProgram, p.SchemaVersion, CurrentSchemaVersion)
	}
	if !identifierPattern.MatchString(p.ProgramID) {
		return fmt.Errorf("%w: program_id %q is not a valid identifier", ErrInvalidProgram, p.ProgramID)
	}
	if len(p.SourceHash) != 64 {
		return fmt.Errorf("%w: source_hash must be a 64-character hex digest", ErrInvalidProgram)
	}
	if len(p.Facts) == 0 && len(p.Rules) == 0 {
		return fmt.Errorf("%w: program has no facts or rules", ErrInvalidProgram)
	}
	if len(p.Facts) > 100_000 || len(p.Rules) > 100_000 {
		return fmt.Errorf("%w: program exceeds size ceiling", ErrInvalidProgram)
	}
	for _, fact := range p.Facts {
		if err := fact.validate(); err != nil {
			return err
		}
	}
	for _, rule := range p.Rules {
		if err := rule.validate(); err != nil {
			return err
		}
	}
	return nil
}

// Limits are hard ceilings ADR-0006 requires on every shadow evaluation:
// "límites de tiempo/profundidad/soluciones". A solver adapter built
// against this contract must reject or truncate — never silently run
// past — any of these.
type Limits struct {
	MaxWallTime  time.Duration `json:"max_wall_time"`
	MaxDepth     int           `json:"max_depth"`
	MaxSolutions int           `json:"max_solutions"`
}

// DefaultLimits are conservative enough to run inside the shadow path
// without competing with the productive Go decision for CPU/wall time.
func DefaultLimits() Limits {
	return Limits{MaxWallTime: 2 * time.Second, MaxDepth: 32, MaxSolutions: 100}
}

func (l Limits) Validate() error {
	if l.MaxWallTime <= 0 || l.MaxWallTime > 30*time.Second {
		return fmt.Errorf("%w: max_wall_time %s out of bounds (0, 30s]", ErrInvalidLimits, l.MaxWallTime)
	}
	if l.MaxDepth <= 0 || l.MaxDepth > 256 {
		return fmt.Errorf("%w: max_depth %d out of bounds (0, 256]", ErrInvalidLimits, l.MaxDepth)
	}
	if l.MaxSolutions <= 0 || l.MaxSolutions > 10_000 {
		return fmt.Errorf("%w: max_solutions %d out of bounds (0, 10000]", ErrInvalidLimits, l.MaxSolutions)
	}
	return nil
}

// ComparisonEvent is the event internal/shadowverifier's eventual
// solver-comparison mode records for every evaluated program: what Go
// concluded, what the solver concluded, and whether they agreed. It is
// intentionally separate from internal/decisiongraph's NodeVerification/
// NodeEvidence events, which already cover organizational-fact evidence —
// this event exists only for the Go-vs-solver comparison ADR-0006 adds,
// and never carries free text (Outcome values are identifiers, e.g. a
// Fact.Predicate or a fixed verdict token, not prose).
type ComparisonEvent struct {
	RunID         string    `json:"run_id"`
	ProgramID     string    `json:"program_id"`
	SourceHash    string    `json:"source_hash"`
	GoOutcome     string    `json:"go_outcome"`
	SolverOutcome string    `json:"solver_outcome"`
	Diverged      bool      `json:"diverged"`
	OccurredAt    time.Time `json:"occurred_at"`
}

func (e ComparisonEvent) Validate() error {
	if !identifierPattern.MatchString(e.RunID) {
		return fmt.Errorf("%w: run_id %q is not a valid identifier", ErrInvalidEvent, e.RunID)
	}
	if !identifierPattern.MatchString(e.ProgramID) {
		return fmt.Errorf("%w: program_id %q is not a valid identifier", ErrInvalidEvent, e.ProgramID)
	}
	if len(e.SourceHash) != 64 {
		return fmt.Errorf("%w: source_hash must be a 64-character hex digest", ErrInvalidEvent)
	}
	if !identifierPattern.MatchString(e.GoOutcome) || !identifierPattern.MatchString(e.SolverOutcome) {
		return fmt.Errorf("%w: outcomes must be valid identifiers, not free text", ErrInvalidEvent)
	}
	if e.Diverged == (e.GoOutcome == e.SolverOutcome) {
		return fmt.Errorf("%w: diverged=%v inconsistent with go_outcome==solver_outcome", ErrInvalidEvent, e.Diverged)
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("%w: occurred_at is required", ErrInvalidEvent)
	}
	return nil
}

// Divergence is the durable record ADR-0006 requires ("almacenamiento de
// divergencias") whenever a ComparisonEvent disagrees. Storage of this
// type is future work (R34+ persistence); this shape is the contract a
// store adapter must satisfy.
type Divergence struct {
	RunID         string    `json:"run_id"`
	ProgramID     string    `json:"program_id"`
	Predicate     string    `json:"predicate"`
	GoOutcome     string    `json:"go_outcome"`
	SolverOutcome string    `json:"solver_outcome"`
	RecordedAt    time.Time `json:"recorded_at"`
}

func NewDivergence(event ComparisonEvent, predicate string) (Divergence, error) {
	if err := event.Validate(); err != nil {
		return Divergence{}, err
	}
	if !event.Diverged {
		return Divergence{}, fmt.Errorf("%w: comparison event did not diverge", ErrInvalidEvent)
	}
	if !identifierPattern.MatchString(predicate) {
		return Divergence{}, fmt.Errorf("%w: predicate %q is not a valid identifier", ErrInvalidEvent, predicate)
	}
	return Divergence{
		RunID: event.RunID, ProgramID: event.ProgramID, Predicate: predicate,
		GoOutcome: event.GoOutcome, SolverOutcome: event.SolverOutcome, RecordedAt: event.OccurredAt,
	}, nil
}
