package contextprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
)

type Provider struct {
	repository     memory.Repository
	organizationID string
	maxEntries     int
}

func New(repository memory.Repository, organizationID string, maxEntries int) (*Provider, error) {
	if repository == nil {
		return nil, errors.New("memory context provider requires repository")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("memory context provider requires organization ID")
	}
	if maxEntries <= 0 {
		return nil, errors.New("memory context provider requires positive max entries")
	}
	return &Provider{repository: repository, organizationID: organizationID, maxEntries: maxEntries}, nil
}

func (p *Provider) ListApproved(ctx context.Context, request contextengine.BuildRequest) ([]contextengine.SourceRecord, error) {
	if request.OrganizationID != p.organizationID {
		return nil, fmt.Errorf("memory provider organization mismatch: request=%s configured=%s", request.OrganizationID, p.organizationID)
	}
	entries, err := p.repository.ListApproved(ctx, memory.ApprovedFilter{OrganizationID: p.organizationID, RoleID: request.ActorRoleID, Limit: p.maxEntries})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})
	if len(entries) > p.maxEntries {
		entries = entries[:p.maxEntries]
	}
	result := make([]contextengine.SourceRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.OrganizationID != p.organizationID || entry.RoleID != request.ActorRoleID {
			return nil, fmt.Errorf("memory provider returned entry outside requested scope: %s", entry.ID)
		}
		if entry.Status != memory.StatusApproved {
			return nil, fmt.Errorf("memory provider returned non-approved entry %s in state %s", entry.ID, entry.Status)
		}
		record, err := sourceRecord(entry)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

func (p *Provider) ValidateVersion(ctx context.Context, expected contextengine.SourceRecord) error {
	if expected.Kind != contextengine.SourceApprovedMemory {
		return fmt.Errorf("memory version validation received source kind %s", expected.Kind)
	}
	entry, err := p.repository.Get(ctx, p.organizationID, expected.Reference)
	if err != nil {
		return err
	}
	if entry.OrganizationID != p.organizationID {
		return fmt.Errorf("memory entry %s crossed organization boundary", entry.ID)
	}
	if entry.Status != memory.StatusApproved {
		return fmt.Errorf("memory entry %s is no longer approved", entry.ID)
	}
	current, err := sourceRecord(entry)
	if err != nil {
		return err
	}
	if current.Version != expected.Version || current.ContentHash != expected.ContentHash {
		return fmt.Errorf("memory entry %s version drift: expected %s/%s got %s/%s", entry.ID, expected.Version, expected.ContentHash, current.Version, current.ContentHash)
	}
	return nil
}

type renderedMemory struct {
	SchemaVersion string               `json:"schema_version"`
	Category      string               `json:"category"`
	Problem       string               `json:"problem"`
	Correction    string               `json:"correction"`
	SourceRunID   int64                `json:"source_run_id"`
	EvidenceRefs  []memory.EvidenceRef `json:"evidence_refs"`
}

func sourceRecord(entry memory.Entry) (contextengine.SourceRecord, error) {
	if err := entry.Validate(); err != nil {
		return contextengine.SourceRecord{}, err
	}
	if entry.Status != memory.StatusApproved {
		return contextengine.SourceRecord{}, fmt.Errorf("memory entry %s is not approved", entry.ID)
	}
	payload, err := json.Marshal(renderedMemory{SchemaVersion: "approved-memory.context.v1", Category: entry.Category, Problem: entry.Problem, Correction: entry.Correction, SourceRunID: entry.SourceRunID, EvidenceRefs: append([]memory.EvidenceRef(nil), entry.EvidenceRefs...)})
	if err != nil {
		return contextengine.SourceRecord{}, fmt.Errorf("render approved memory %s: %w", entry.ID, err)
	}
	contentSum := sha256.Sum256(payload)
	canonicalHash, err := entry.CanonicalHash()
	if err != nil {
		return contextengine.SourceRecord{}, err
	}
	dataClass, err := mapDataClass(entry.Admission.DataClass)
	if err != nil {
		return contextengine.SourceRecord{}, err
	}
	priority, ok := contextengine.AuthorityPriority(contextengine.TierApprovedMemory)
	if !ok {
		return contextengine.SourceRecord{}, errors.New("approved memory authority tier is not registered")
	}
	record := contextengine.SourceRecord{Kind: contextengine.SourceApprovedMemory, Reference: entry.ID, Version: "organizational-memory.v1:" + canonicalHash, AuthorityTier: contextengine.TierApprovedMemory, AuthorityPriority: priority, InstructionClass: contextengine.InstructionData, TrustClass: contextengine.TrustUntrusted, DataClass: dataClass, MayGrantCapabilities: false, Content: payload, ContentHash: hex.EncodeToString(contentSum[:]), Included: true, Relevance: 1, ProviderPriority: 1}
	if err := contextengine.ValidateSourceMetadata(record); err != nil {
		return contextengine.SourceRecord{}, err
	}
	return record, nil
}

func mapDataClass(value memory.DataClass) (contextengine.DataClass, error) {
	switch value {
	case memory.DataPublic:
		return contextengine.DataPublic, nil
	case memory.DataOrganizational:
		return contextengine.DataOrganizational, nil
	case memory.DataSanitized:
		return contextengine.DataSanitized, nil
	case memory.DataSecret, memory.DataClinical:
		return "", fmt.Errorf("forbidden memory data class %s reached context provider", value)
	default:
		return "", fmt.Errorf("unknown memory data class %s", value)
	}
}
