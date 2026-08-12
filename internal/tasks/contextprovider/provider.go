package contextprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type Provider struct{ reader tasks.TaskReader }

func New(reader tasks.TaskReader) (*Provider, error) {
	if reader == nil {
		return nil, errors.New("task context provider requires task reader")
	}
	return &Provider{reader: reader}, nil
}

func (p *Provider) GetTaskContext(ctx context.Context, request contextengine.BuildRequest) (*contextengine.SourceRecord, error) {
	if strings.TrimSpace(request.TaskRef) == "" {
		return nil, nil
	}
	id, err := parseRef(request.TaskRef)
	if err != nil {
		return nil, err
	}
	detail, err := p.reader.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if detail.Task.OrganizationID != request.OrganizationID || detail.Task.OrganizationRevisionID != request.OrganizationRevisionID {
		return nil, fmt.Errorf("task context scope mismatch for task %d", id)
	}
	record, err := sourceRecord(detail)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// The actor role is part of the SourceValidator contract but is not
// consulted here: a task context record is scoped by task reference, and
// the caller has already bound the task to the actor.
func (p *Provider) ValidateVersion(ctx context.Context, _ string, expected contextengine.SourceRecord) error {
	if expected.Kind != contextengine.SourceTaskContext {
		return fmt.Errorf("task version validation received source kind %s", expected.Kind)
	}
	id, err := parseRef(expected.Reference)
	if err != nil {
		return err
	}
	detail, err := p.reader.GetTask(ctx, id)
	if err != nil {
		return err
	}
	current, err := sourceRecord(detail)
	if err != nil {
		return err
	}
	if current.Version != expected.Version || current.ContentHash != expected.ContentHash {
		return fmt.Errorf("task context %d version drift", id)
	}
	return nil
}

func parseRef(ref string) (int64, error) {
	const prefix = "task:"
	if !strings.HasPrefix(ref, prefix) {
		return 0, fmt.Errorf("task context reference must use task:<id>")
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(ref, prefix), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid task context reference %q", ref)
	}
	return id, nil
}

type renderedRequirement struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Status      string `json:"status"`
}

type renderedEvidence struct {
	Type      string         `json:"type"`
	Reference string         `json:"reference"`
	Digest    string         `json:"digest,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type renderedAttempt struct {
	Ordinal       int    `json:"ordinal"`
	State         string `json:"state"`
	ResultSummary string `json:"result_summary,omitempty"`
	FailureCode   string `json:"failure_code,omitempty"`
}

type renderedTask struct {
	SchemaVersion      string                `json:"schema_version"`
	TaskID             int64                 `json:"task_id"`
	AssignedRoleID     string                `json:"assigned_role_id"`
	AssignedUnitID     string                `json:"assigned_unit_id"`
	Title              string                `json:"title"`
	Instructions       string                `json:"instructions"`
	AcceptanceCriteria []string              `json:"acceptance_criteria"`
	Status             string                `json:"status"`
	Requirements       []renderedRequirement `json:"requirements"`
	Evidence           []renderedEvidence    `json:"evidence"`
	Attempts           []renderedAttempt     `json:"attempts"`
}

func sourceRecord(detail tasks.TaskDetail) (contextengine.SourceRecord, error) {
	requirements := make([]renderedRequirement, 0, len(detail.Requirements))
	for _, r := range detail.Requirements {
		requirements = append(requirements, renderedRequirement{Key: r.Key, Type: string(r.Type), Description: r.Description, Required: r.Required, Status: string(r.Status)})
	}
	evidence := make([]renderedEvidence, 0, len(detail.Evidence))
	for _, e := range detail.Evidence {
		digest := ""
		if e.Digest != nil {
			digest = *e.Digest
		}
		metadata := map[string]any{}
		if strings.HasPrefix(e.Reference, "executive-evidence:") && e.Metadata != nil {
			metadata = e.Metadata
		}
		evidence = append(evidence, renderedEvidence{Type: string(e.Type), Reference: e.Reference, Digest: digest, Metadata: metadata})
	}
	attempts := make([]renderedAttempt, 0, len(detail.Attempts))
	for _, a := range detail.Attempts {
		item := renderedAttempt{Ordinal: a.Ordinal, State: string(a.State)}
		if a.ResultSummary != nil {
			item.ResultSummary = *a.ResultSummary
		}
		if a.FailureCode != nil {
			item.FailureCode = *a.FailureCode
		}
		attempts = append(attempts, item)
	}
	payload, err := json.Marshal(renderedTask{SchemaVersion: "task-context.v1", TaskID: detail.Task.ID, AssignedRoleID: detail.Task.AssignedRoleID, AssignedUnitID: detail.Task.AssignedUnitID, Title: detail.Task.Title, Instructions: detail.Task.Instructions, AcceptanceCriteria: append([]string(nil), detail.Task.AcceptanceCriteria...), Status: string(detail.Task.Status), Requirements: requirements, Evidence: evidence, Attempts: attempts})
	if err != nil {
		return contextengine.SourceRecord{}, err
	}
	priority, ok := contextengine.AuthorityPriority(contextengine.TierTask)
	if !ok {
		return contextengine.SourceRecord{}, errors.New("task authority tier is not registered")
	}
	record := contextengine.SourceRecord{Kind: contextengine.SourceTaskContext, Reference: fmt.Sprintf("task:%d", detail.Task.ID), Version: fmt.Sprintf("task.v1:%d:%s", detail.Task.Version, detail.Task.RequestHash), AuthorityTier: contextengine.TierTask, AuthorityPriority: priority, InstructionClass: contextengine.InstructionScoped, TrustClass: contextengine.TrustUntrusted, DataClass: contextengine.DataOrganizational, MayGrantCapabilities: false, Content: payload, ContentHash: contextengine.DigestCanonicalBytes(payload), Included: true, Relevance: 1, ProviderPriority: 1}
	if err := contextengine.ValidateSourceMetadata(record); err != nil {
		return contextengine.SourceRecord{}, err
	}
	return record, nil
}
