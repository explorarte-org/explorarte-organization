package need

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	needIDPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	roleIDPattern = regexp.MustCompile(`^[a-z0-9_]+/[a-z0-9_]+$`)
)

type ProcedureNeedStatus string

const (
	StatusOpen       ProcedureNeedStatus = "open"
	StatusAccepted   ProcedureNeedStatus = "accepted"
	StatusAuthored   ProcedureNeedStatus = "authored"
	StatusRejected   ProcedureNeedStatus = "rejected"
	StatusSuperseded ProcedureNeedStatus = "superseded"
)

func (s ProcedureNeedStatus) Valid() bool {
	switch s {
	case StatusOpen, StatusAccepted, StatusAuthored, StatusRejected, StatusSuperseded:
		return true
	default:
		return false
	}
}

type EvidenceRef struct {
	EvidenceID string `json:"evidence_id"`
	Kind       string `json:"kind"`
	Digest     string `json:"digest"`
}

type Acceptance struct {
	DecisionRef string    `json:"decision_ref"`
	AcceptedBy  string    `json:"accepted_by"`
	AcceptedAt  time.Time `json:"accepted_at"`
	ProjectRef  string    `json:"project_ref,omitempty"`
	TaskRef     string    `json:"task_ref,omitempty"`
}

func (a *Acceptance) Validate() error {
	if a == nil {
		return errors.New("acceptance evidence is required")
	}
	if strings.TrimSpace(a.DecisionRef) == "" {
		return errors.New("decision_ref is required for acceptance")
	}
	if strings.TrimSpace(a.AcceptedBy) == "" || !roleIDPattern.MatchString(a.AcceptedBy) {
		return errors.New("accepted_by role ID is invalid or empty")
	}
	if a.AcceptedAt.IsZero() {
		return errors.New("accepted_at timestamp is required")
	}
	return nil
}

type ProcedureNeed struct {
	ID                 string              `json:"id"`
	OrganizationID     string              `json:"organization_id"`
	RoleID             string              `json:"role_id"`
	TaskClass          string              `json:"task_class,omitempty"`
	ExecutionProfileID string              `json:"execution_profile_id,omitempty"`
	ProblemStatement   string              `json:"problem_statement"`
	EpisodeRefs        []string            `json:"episode_refs,omitempty"`
	ClusterRefs        []string            `json:"cluster_refs,omitempty"`
	EvidenceRefs       []EvidenceRef       `json:"evidence_refs,omitempty"`
	SuggestedSkillIDs  []string            `json:"suggested_skill_ids,omitempty"`
	Status             ProcedureNeedStatus `json:"status"`
	Acceptance         *Acceptance         `json:"acceptance,omitempty"`
	CanonicalDigest    string              `json:"canonical_digest"`
	Revision           int64               `json:"revision"`
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
}

func (n *ProcedureNeed) Validate() error {
	if !needIDPattern.MatchString(n.ID) {
		return fmt.Errorf("invalid procedure need ID: %q", n.ID)
	}
	if strings.TrimSpace(n.OrganizationID) == "" {
		return errors.New("organization_id is required")
	}
	if !roleIDPattern.MatchString(n.RoleID) {
		return fmt.Errorf("invalid role ID: %q", n.RoleID)
	}
	if strings.TrimSpace(n.ProblemStatement) == "" || len(n.ProblemStatement) < 10 {
		return errors.New("problem_statement must be at least 10 characters")
	}
	if !n.Status.Valid() {
		return fmt.Errorf("invalid status: %q", n.Status)
	}
	if n.Revision <= 0 {
		return errors.New("revision must be greater than zero")
	}
	if n.CreatedAt.IsZero() || n.UpdatedAt.IsZero() || n.UpdatedAt.Before(n.CreatedAt) {
		return errors.New("invalid timestamps")
	}

	switch n.Status {
	case StatusOpen:
		if n.Acceptance != nil {
			return errors.New("open need cannot have acceptance evidence")
		}
	case StatusAccepted, StatusAuthored:
		if err := n.Acceptance.Validate(); err != nil {
			return fmt.Errorf("accepted need requires valid acceptance: %w", err)
		}
	case StatusRejected:
		// Rejected needs can be rejected without acceptance
	case StatusSuperseded:
	}

	computedDigest, err := HashNeedIdentity(*n)
	if err != nil {
		return fmt.Errorf("compute canonical digest: %w", err)
	}
	if n.CanonicalDigest != "" && n.CanonicalDigest != computedDigest {
		return fmt.Errorf("canonical digest mismatch: expected %s, got %s", computedDigest, n.CanonicalDigest)
	}
	n.CanonicalDigest = computedDigest

	return nil
}

func HashNeedIdentity(n ProcedureNeed) (string, error) {
	identity := struct {
		SchemaVersion    string   `json:"schema_version"`
		ID               string   `json:"id"`
		OrganizationID   string   `json:"organization_id"`
		RoleID           string   `json:"role_id"`
		ProblemStatement string   `json:"problem_statement"`
		EpisodeRefs      []string `json:"episode_refs,omitempty"`
		ClusterRefs      []string `json:"cluster_refs,omitempty"`
	}{
		SchemaVersion:    "skillforge-need/v1",
		ID:               n.ID,
		OrganizationID:   n.OrganizationID,
		RoleID:           n.RoleID,
		ProblemStatement: strings.TrimSpace(n.ProblemStatement),
		EpisodeRefs:      n.EpisodeRefs,
		ClusterRefs:      n.ClusterRefs,
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// NeedProposer is an extensibility port for future automated discovery
// (such as an operator-reviewed MemoryOS miner). It is NOT connected to MemoryOS in Phase 1.
type NeedProposer interface {
	Propose(ctx context.Context, need ProcedureNeed) (ProcedureNeed, error)
}
