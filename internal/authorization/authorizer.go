package authorization

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"gopkg.in/yaml.v3"
)

type Matrix struct {
	SchemaVersion  string              `yaml:"schema_version"`
	DocumentStatus string              `yaml:"document_status"`
	DefaultPolicy  string              `yaml:"default_policy"`
	Capabilities   []Capability        `yaml:"capabilities"`
	Grants         map[string][]string `yaml:"grants"`
	HardDenies     map[string][]string `yaml:"hard_denies"`
	SkillLifecycle map[string]any      `yaml:"skill_lifecycle,omitempty"`
	ImportedSkills []map[string]any    `yaml:"imported_skills,omitempty"`
}

type Capability struct {
	ID       string `yaml:"id"`
	Risk     string `yaml:"risk"`
	Approval string `yaml:"approval,omitempty"`
}

type registryPolicyReader struct{ reader registry.Reader }

func (r registryPolicyReader) GetOrganization(ctx context.Context, id string) (registry.Organization, error) {
	return r.reader.GetOrganization(ctx, id)
}
func (r registryPolicyReader) GetCurrentRevision(ctx context.Context, id string) (*registry.Revision, error) {
	return r.reader.GetCurrentRevision(ctx, id)
}
func (r registryPolicyReader) GetAuthorizationRole(ctx context.Context, organizationID, roleID string) (registry.Role, error) {
	return r.reader.GetRole(ctx, organizationID, roleID)
}

type Authorizer struct {
	reader       PolicyReader
	organization string
	matrix       Matrix
	matrixHash   string
	capabilities map[string]Capability
	authorities  map[string]struct{}
}

func New(reader registry.Reader, organizationID, canonicalDir string) (*Authorizer, error) {
	if reader == nil {
		return nil, errors.New("capability authorizer requires registry reader")
	}
	return NewWithPolicyReader(registryPolicyReader{reader: reader}, organizationID, canonicalDir)
}

func NewWithPolicyReader(reader PolicyReader, organizationID, canonicalDir string) (*Authorizer, error) {
	if reader == nil {
		return nil, errors.New("capability authorizer requires policy reader")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("capability authorizer requires organization ID")
	}
	body, err := os.ReadFile(strings.TrimRight(canonicalDir, "/") + "/capability-matrix.yaml")
	if err != nil {
		return nil, fmt.Errorf("read capability matrix: %w", err)
	}
	var matrix Matrix
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&matrix); err != nil {
		return nil, fmt.Errorf("parse capability matrix: %w", err)
	}
	loader, err := registry.NewLoader(canonicalDir)
	if err != nil {
		return nil, fmt.Errorf("create canonical loader: %w", err)
	}
	snapshot, _, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("load canonical documents: %w", err)
	}
	var matrixHash string
	for _, document := range snapshot.Documents {
		if document.Path == "capability-matrix.yaml" {
			matrixHash = document.SemanticHash
			break
		}
	}
	if matrixHash == "" {
		return nil, errors.New("canonical capability matrix hash is missing")
	}
	return newAuthorizer(reader, organizationID, matrix, matrixHash)
}

func newAuthorizer(reader PolicyReader, organizationID string, matrix Matrix, matrixHash string) (*Authorizer, error) {
	if matrix.DefaultPolicy != "deny" {
		return nil, errors.New("capability matrix must use default deny")
	}
	if !sha256Pattern.MatchString(matrixHash) {
		return nil, errors.New("capability matrix semantic hash must be lowercase SHA-256")
	}
	capabilities := make(map[string]Capability, len(matrix.Capabilities))
	for _, capability := range matrix.Capabilities {
		capability.ID = strings.TrimSpace(capability.ID)
		capability.Risk = strings.TrimSpace(capability.Risk)
		capability.Approval = strings.TrimSpace(capability.Approval)
		if capability.ID == "" || capability.Risk == "" {
			return nil, errors.New("capability matrix contains incomplete capability")
		}
		if _, exists := capabilities[capability.ID]; exists {
			return nil, fmt.Errorf("duplicate capability %q", capability.ID)
		}
		switch capability.Approval {
		case "", "owner", "policy_or_human", "owner_or_cell_policy":
		default:
			return nil, fmt.Errorf("capability %q uses unsupported approval mode %q", capability.ID, capability.Approval)
		}
		capabilities[capability.ID] = capability
	}
	authorities := make(map[string]struct{}, len(matrix.Grants))
	for authority, grants := range matrix.Grants {
		authority = strings.TrimSpace(authority)
		if authority == "" {
			return nil, errors.New("capability matrix contains empty authority class")
		}
		authorities[authority] = struct{}{}
		for _, grant := range grants {
			if grant != "*" {
				if _, ok := capabilities[grant]; !ok {
					return nil, fmt.Errorf("authority %q grants unknown capability %q", authority, grant)
				}
			}
		}
	}
	for authority, denied := range matrix.HardDenies {
		if authority != "*" {
			if _, ok := authorities[authority]; !ok {
				return nil, fmt.Errorf("hard deny references unknown authority %q", authority)
			}
		}
		for _, capability := range denied {
			if _, ok := capabilities[capability]; !ok {
				return nil, fmt.Errorf("hard deny references unknown capability %q", capability)
			}
		}
	}
	return &Authorizer{reader: reader, organization: organizationID, matrix: matrix, matrixHash: matrixHash, capabilities: capabilities, authorities: authorities}, nil
}

func (a *Authorizer) Evaluate(ctx context.Context, request EvaluationRequest) (Evaluation, error) {
	if a == nil || a.reader == nil {
		return Evaluation{}, errors.New("authorization evaluator is unavailable")
	}
	if err := validateEvaluationRequest(request); err != nil {
		return Evaluation{}, err
	}
	return a.evaluate(ctx, request)
}

func (a *Authorizer) evaluate(ctx context.Context, request EvaluationRequest) (Evaluation, error) {
	result := Evaluation{CapabilityID: request.CapabilityID, MatrixHash: a.matrixHash, ApprovalRequestID: request.ApprovalRequestID}
	if request.OrganizationID != a.organization {
		result.Effect, result.ReasonCode = EffectDeny, ReasonOrganizationMismatch
		return result, nil
	}
	organization, err := a.reader.GetOrganization(ctx, request.OrganizationID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) || errors.Is(err, ErrRequestNotFound) {
			result.Effect, result.ReasonCode = EffectDeny, ReasonOrganizationMismatch
			return result, nil
		}
		return Evaluation{}, fmt.Errorf("read organization: %w", err)
	}
	if organization.ID != request.OrganizationID || organization.RetiredAt != nil {
		result.Effect, result.ReasonCode = EffectDeny, ReasonOrganizationMismatch
		return result, nil
	}
	revision, err := a.reader.GetCurrentRevision(ctx, request.OrganizationID)
	if err != nil {
		return Evaluation{}, fmt.Errorf("read organization revision: %w", err)
	}
	if revision == nil || organization.CurrentRevision != request.OrganizationRevisionID || revision.ID != request.OrganizationRevisionID {
		result.Effect, result.ReasonCode = EffectDeny, ReasonRevisionMismatch
		return result, nil
	}
	if revision.DocumentHashes["capability-matrix.yaml"] != a.matrixHash {
		result.Effect, result.ReasonCode = EffectDeny, ReasonMatrixHashMismatch
		return result, nil
	}
	capability, ok := a.capabilities[request.CapabilityID]
	if !ok {
		result.Effect, result.ReasonCode = EffectDeny, ReasonUnknownCapability
		return result, nil
	}
	result.Risk, result.ApprovalMode = capability.Risk, capability.Approval
	role, err := a.reader.GetAuthorizationRole(ctx, request.OrganizationID, request.ActorRoleID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) || errors.Is(err, ErrRoleNotFound) {
			result.Effect, result.ReasonCode = EffectDeny, ReasonRoleNotFound
			return result, nil
		}
		return Evaluation{}, fmt.Errorf("read authorization role: %w", err)
	}
	if role.OrganizationID != "" && role.OrganizationID != request.OrganizationID {
		result.Effect, result.ReasonCode = EffectDeny, ReasonOrganizationMismatch
		return result, nil
	}
	if role.RetiredAt != nil {
		result.Effect, result.ReasonCode = EffectDeny, ReasonRoleRetired
		return result, nil
	}
	if !role.Enabled {
		result.Effect, result.ReasonCode = EffectDeny, ReasonRoleDisabled
		return result, nil
	}
	if !role.Executable && role.ID != organization.OwnerRoleID {
		result.Effect, result.ReasonCode = EffectDeny, ReasonRoleNotExecutable
		return result, nil
	}
	authority := strings.TrimSpace(role.AuthorityClass)
	result.AuthorityClass = authority
	if _, ok := a.authorities[authority]; !ok {
		result.Effect, result.ReasonCode = EffectDeny, ReasonUnknownAuthorityClass
		return result, nil
	}
	if a.globalHardDenied(request.CapabilityID) {
		result.Effect, result.ReasonCode = EffectDeny, ReasonHardDeny
		return result, nil
	}
	if a.authorityHardDenied(authority, request.CapabilityID) {
		result.Effect, result.ReasonCode = EffectDeny, ReasonHardDeny
		return result, nil
	}
	granted := contains(a.matrix.Grants[authority], "*") || contains(a.matrix.Grants[authority], request.CapabilityID)
	if capability.Approval != "" {
		result.Effect, result.ReasonCode = EffectApprovalRequired, ReasonApprovalMissing
		return result, nil
	}
	if granted {
		result.Effect, result.ReasonCode = EffectAllow, ReasonAllowedByGrant
		return result, nil
	}
	result.Effect, result.ReasonCode = EffectDeny, ReasonGrantMissing
	return result, nil
}

func (a *Authorizer) Authorize(ctx context.Context, organizationID string, revisionID int64, roleID, capability string) error {
	resourceType := "legacy"
	if capability == "model.invoke" {
		resourceType = "model_invocation"
	}

	canonical := []byte(strings.Join([]string{resourceType, organizationID, fmt.Sprint(revisionID), roleID, capability}, "\x00"))
	result, err := a.Evaluate(ctx, EvaluationRequest{
		OrganizationID: organizationID, OrganizationRevisionID: revisionID, ActorRoleID: roleID,
		CapabilityID: capability, ResourceType: resourceType, ResourceID: roleID + ":" + capability,
		ActionDigest: DigestAction(canonical),
	})
	if err != nil {
		return err
	}
	return legacyEvaluationError(result)
}

func legacyEvaluationError(result Evaluation) error {
	switch result.Effect {
	case EffectAllow:
		return nil
	case EffectApprovalRequired:
		return fmt.Errorf("%w: %s", ErrApprovalRequired, result.ReasonCode)
	case EffectDeny:
		switch result.ReasonCode {
		case ReasonUnknownCapability:
			return fmt.Errorf("%w: %s", ErrUnknownCapability, result.CapabilityID)
		case ReasonUnknownAuthorityClass:
			return fmt.Errorf("%w: %s", ErrUnknownAuthorityClass, result.AuthorityClass)
		case ReasonRevisionMismatch, ReasonMatrixHashMismatch:
			return fmt.Errorf("%w: %s", ErrPolicyRevisionMismatch, result.ReasonCode)
		default:
			return fmt.Errorf("%w: %s", ErrCapabilityDenied, result.ReasonCode)
		}
	default:
		return errors.New("authorization returned an invalid effect")
	}
}

func (a *Authorizer) MatrixHash() string { return a.matrixHash }

func (a *Authorizer) KnownCapabilities() []string {
	result := make([]string, 0, len(a.capabilities))
	for capability := range a.capabilities {
		result = append(result, capability)
	}
	sort.Strings(result)
	return result
}

func (a *Authorizer) Capability(id string) (Capability, bool) {
	value, ok := a.capabilities[id]
	return value, ok
}

func (a *Authorizer) globalHardDenied(capability string) bool {
	return contains(a.matrix.HardDenies["*"], capability)
}

func (a *Authorizer) authorityHardDenied(authority, capability string) bool {
	return contains(a.matrix.HardDenies[authority], capability)
}

func (a *Authorizer) authorityKnown(authority string) bool {
	_, ok := a.authorities[authority]
	return ok
}

func (a *Authorizer) hardDenied(authority, capability string) bool {
	return a.globalHardDenied(capability) || a.authorityHardDenied(authority, capability)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
