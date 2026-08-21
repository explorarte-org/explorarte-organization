package executive

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// AcceptancePhase says when a criterion becomes checkable.
//
// This exists because a criterion is not simply a sentence the campaign must
// satisfy. "The design records what will change" can be judged before
// anything is built; "the host gates passed" cannot, because there are no
// gates yet. Handing both to the design reviewer asked it to verify the
// future, and it refused -- correctly, and permanently.
type AcceptancePhase string

const (
	// AcceptanceDesign: judgeable from the design alone, before anything
	// is built. These are the only criteria the design reviewer sees.
	AcceptanceDesign AcceptancePhase = "design"

	// AcceptanceImplementation: what the built change must demonstrate.
	AcceptanceImplementation AcceptancePhase = "implementation"

	// AcceptancePromotion: what must hold once the change has landed.
	AcceptancePromotion AcceptancePhase = "promotion"
)

func (p AcceptancePhase) valid() bool {
	switch p {
	case AcceptanceDesign, AcceptanceImplementation, AcceptancePromotion:
		return true
	}
	return false
}

// AcceptanceCriterion is one owner requirement and the phase that owns it.
type AcceptanceCriterion struct {
	Text  string          `json:"text"`
	Phase AcceptancePhase `json:"phase"`
}

// UnmarshalJSON refuses a bare string.
//
// A string carries no phase, and there is no safe thing to assume about one.
// Defaulting it to design puts implementation criteria back in front of the
// design reviewer, which is the bug this type exists to remove; defaulting it
// to implementation silently drops it from the review it may well have
// belonged to. Guessing from its words would hide the same contradiction one
// layer down, where nothing compares it to anything.
//
// So the owner says. The error names the three phases, because a refusal that
// does not tell you what to write is a refusal you have to go read code to
// answer.
func (c *AcceptanceCriterion) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		return fmt.Errorf(
			"%w: acceptance criterion %q has no phase; declare {\"text\":...,\"phase\":\"design\"|\"implementation\"|\"promotion\"}",
			ErrInvalidInput, truncateForMessage(text))
	}
	var raw struct {
		Text  string          `json:"text"`
		Phase AcceptancePhase `json:"phase"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return fmt.Errorf("%w: acceptance criterion: %v", ErrInvalidInput, err)
	}
	if strings.TrimSpace(raw.Text) == "" {
		return fmt.Errorf("%w: acceptance criterion has no text", ErrInvalidInput)
	}
	if !raw.Phase.valid() {
		return fmt.Errorf(
			"%w: acceptance criterion %q declares phase %q; expected design, implementation or promotion",
			ErrInvalidInput, truncateForMessage(raw.Text), raw.Phase)
	}
	c.Text, c.Phase = strings.TrimSpace(raw.Text), raw.Phase
	return nil
}

func truncateForMessage(text string) string {
	const limit = 80
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

// AcceptanceTexts returns the criterion texts, in order, for the places that
// carry requirements as prose.
func AcceptanceTexts(criteria []AcceptanceCriterion) []string {
	out := make([]string, 0, len(criteria))
	for _, criterion := range criteria {
		out = append(out, criterion.Text)
	}
	return out
}

// AcceptanceForPhase returns the texts of the criteria that phase owns.
func AcceptanceForPhase(criteria []AcceptanceCriterion, phase AcceptancePhase) []string {
	out := make([]string, 0, len(criteria))
	for _, criterion := range criteria {
		if criterion.Phase == phase {
			out = append(out, criterion.Text)
		}
	}
	return out
}

// AcceptanceRecorder stores and recovers the owner's phase assignment.
//
// It is durable rather than recomputed because the assignment is the owner's
// statement, not a derivation. Recomputing it later from the criterion text
// would be the keyword classifier this whole type exists to avoid.
type AcceptanceRecorder interface {
	// RecordAcceptance is idempotent on (root task, ordinal): a resumed
	// submit must not duplicate or contradict what the first one stored.
	RecordAcceptance(ctx context.Context, rootTaskID int64, criteria []AcceptanceCriterion) error
	// Acceptance returns what was recorded, in the owner's order.
	Acceptance(ctx context.Context, rootTaskID int64) ([]AcceptanceCriterion, error)
}
