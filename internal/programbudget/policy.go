package programbudget

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

const SchemaVersion = "program-model-budget/v1"

type Family struct {
	Key         string                `json:"key"`
	ProviderIDs []string              `json:"provider_ids"`
	ModelIDs    []string              `json:"model_ids"`
	MaxUSD      modelpricing.USDNanos `json:"max_usd_nanos"`
	Unavailable bool                  `json:"unavailable,omitempty"`
}

// UnmarshalJSON accepts the operator-facing dollar form (max_usd: 7) while
// retaining exact nanodollar arithmetic internally. The nanos form is also
// accepted for durable backwards-compatible tooling.
func (f *Family) UnmarshalJSON(data []byte) error {
	var raw struct {
		Key         string                `json:"key"`
		ProviderIDs []string              `json:"provider_ids"`
		ModelIDs    []string              `json:"model_ids"`
		MaxUSD      *json.Number          `json:"max_usd"`
		MaxUSDNanos modelpricing.USDNanos `json:"max_usd_nanos"`
		Unavailable bool                  `json:"unavailable"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	amount := raw.MaxUSDNanos
	if raw.MaxUSD != nil {
		v, err := raw.MaxUSD.Float64()
		if err != nil || v <= 0 || v > math.MaxInt64/1e9 {
			return fmt.Errorf("invalid max_usd")
		}
		amount = modelpricing.USDNanos(v*1e9 + 0.5)
	}
	*f = Family{Key: raw.Key, ProviderIDs: raw.ProviderIDs, ModelIDs: raw.ModelIDs, MaxUSD: amount, Unavailable: raw.Unavailable}
	return nil
}

type Policy struct {
	SchemaVersion     string   `json:"schema_version"`
	ProgramRootTaskID int64    `json:"program_root_task_id"`
	Families          []Family `json:"families"`
}
type Scope struct {
	ProgramRootTaskID int64
	CorrelationID     string
	Family            Family
}
type Reader interface{ tasks.TaskReader }

func (p Policy) Validate() error {
	if p.SchemaVersion != SchemaVersion || p.ProgramRootTaskID <= 0 || len(p.Families) == 0 {
		return fmt.Errorf("invalid program budget policy")
	}
	seen := map[string]bool{}
	for _, f := range p.Families {
		if strings.TrimSpace(f.Key) == "" || f.MaxUSD <= 0 || seen[f.Key] {
			return fmt.Errorf("invalid program budget family")
		}
		seen[f.Key] = true
		if f.Unavailable {
			continue
		}
		if len(f.ProviderIDs) == 0 || len(f.ModelIDs) == 0 {
			return fmt.Errorf("family %s has no route", f.Key)
		}
		if len(f.ProviderIDs) != 1 {
			return fmt.Errorf("family %s must have exactly one provider", f.Key)
		}
	}
	return nil
}
func Decode(m map[string]any) (Policy, error) {
	b, e := json.Marshal(m)
	if e != nil {
		return Policy{}, e
	}
	var p Policy
	if e = json.Unmarshal(b, &p); e != nil {
		return Policy{}, e
	}
	return p, e
}

type Resolver struct{ Tasks Reader }

// Policy returns the single durable policy attached to a program root.
// Absence is not an error for an ordinary task; callers that have already
// established that the root is a program must treat it as fail-closed.
func (r Resolver) Policy(ctx context.Context, root int64) (Policy, error) {
	if r.Tasks == nil || root <= 0 {
		return Policy{}, fmt.Errorf("program budget task reader required")
	}
	d, err := r.Tasks.GetTask(ctx, root)
	if err != nil {
		return Policy{}, err
	}
	var found *Policy
	for _, ev := range d.Evidence {
		if ev.Reference != "program-model-budget://"+fmt.Sprint(root) {
			continue
		}
		p, err := Decode(ev.Metadata)
		if err != nil || p.Validate() != nil {
			return Policy{}, fmt.Errorf("invalid program budget policy")
		}
		if found != nil {
			return Policy{}, fmt.Errorf("duplicate program budget policy")
		}
		found = &p
	}
	if found == nil {
		return Policy{}, nil
	}
	return *found, nil
}

// Program returns the program root task, the correlation that binds the
// program together, and the budget policy governing taskID.
//
// An empty correlation, or a zero-value Policy, means this task belongs to no
// program: not an error for an ordinary task, but callers admitting autonomous
// spend must treat it as fail-closed. There is no such thing as an unbounded
// budget here, only a missing one.
//
// It is exported and used by Resolve itself so that "which program governs
// this task" has exactly one implementation. A second answer to that question
// would eventually disagree with the ceiling actually enforced at reservation
// time, and the disagreement would show up as spend admitted against a
// program that never authorised it.
func (r Resolver) Program(ctx context.Context, taskID int64) (int64, string, Policy, error) {
	if r.Tasks == nil {
		return 0, "", Policy{}, fmt.Errorf("program budget task reader required")
	}
	d, err := r.Tasks.GetTask(ctx, taskID)
	if err != nil {
		return 0, "", Policy{}, err
	}
	if d.Task.CorrelationID == nil || strings.TrimSpace(*d.Task.CorrelationID) == "" {
		return 0, "", Policy{}, nil
	}
	correlation := *d.Task.CorrelationID
	items, err := r.Tasks.ListTasks(ctx, tasks.TaskFilter{OrganizationID: d.Task.OrganizationID, CorrelationID: correlation, Limit: 1000})
	if err != nil {
		return 0, "", Policy{}, err
	}
	root := d.Task.ID
	for _, t := range items {
		// The executive root is the task in this correlation without a
		// causation edge. Prefer that durable relationship over creation-order
		// heuristics; retain the lowest-id fallback for historical rows.
		if t.CausationID == nil || strings.TrimSpace(*t.CausationID) == "" {
			root = t.ID
			break
		}
		if t.ID < root {
			root = t.ID
		}
	}
	found, err := r.Policy(ctx, root)
	if err != nil {
		return 0, "", Policy{}, err
	}
	if found.SchemaVersion == "" {
		return root, correlation, Policy{}, nil
	}
	if found.ProgramRootTaskID != root {
		return 0, "", Policy{}, fmt.Errorf("program budget policy root mismatch")
	}
	return root, correlation, found, nil
}

func (r Resolver) Resolve(ctx context.Context, taskID int64, provider, model string) (Scope, error) {
	if r.Tasks == nil {
		return Scope{}, fmt.Errorf("program budget task reader required")
	}
	root, correlation, found, e := r.Program(ctx, taskID)
	if e != nil {
		return Scope{}, e
	}
	if correlation == "" || found.SchemaVersion == "" {
		return Scope{}, nil
	}
	for _, f := range found.Families {
		if f.Unavailable {
			continue
		}
		for _, pr := range f.ProviderIDs {
			for _, mo := range f.ModelIDs {
				if pr == provider && mo == model {
					return Scope{ProgramRootTaskID: root, CorrelationID: correlation, Family: f}, nil
				}
			}
		}
	}
	return Scope{ProgramRootTaskID: root, CorrelationID: correlation}, fmt.Errorf("provider/model outside program policy")
}
