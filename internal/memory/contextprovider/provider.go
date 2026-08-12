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
	manager        *memory.Manager
	organizationID string
	maxEntries     int
}

func New(manager *memory.Manager, organizationID string, maxEntries int) (*Provider, error) {
	if manager == nil {
		return nil, errors.New("memory context provider requires manager")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("memory context provider requires organization ID")
	}
	if maxEntries <= 0 {
		return nil, errors.New("memory context provider requires positive max entries")
	}
	return &Provider{manager: manager, organizationID: organizationID, maxEntries: maxEntries}, nil
}

// ListApproved uses Manager.Search (exact-identifier + vector channels,
// see internal/memory/semantic.go and postgres/search.go) when the build
// request carries a usable query signal (request.Purpose); Search itself
// falls back to plain recency when neither channel finds anything, so this
// path is strictly additive over the pure-recency behavior this provider
// had before R29 — never worse, sometimes better (a relevant older lesson
// can now outrank an irrelevant recent one). Search enforces
// ActorRoleID == RoleID, which already matches what this provider always
// scoped to (the requesting role's own memory), so no behavior changes for
// who can see what — only what gets prioritized among what they could
// already see.
func (p *Provider) ListApproved(ctx context.Context, request contextengine.BuildRequest) ([]contextengine.SourceRecord, error) {
	if request.OrganizationID != p.organizationID {
		return nil, fmt.Errorf("memory provider organization mismatch: request=%s configured=%s", request.OrganizationID, p.organizationID)
	}
	var entries []memory.Entry
	var err error
	if queryText := strings.TrimSpace(request.Purpose); queryText != "" {
		// Search already returns its results ordered by relevance (RRF
		// score, or its own recency fallback when no channel matched) — a
		// second recency sort here would silently throw that ranking away
		// and defeat the entire point of using Search instead of
		// ListApproved. Only the no-query branch below needs an explicit
		// recency sort, because ListApproved makes no ordering promise of
		// its own.
		entries, err = p.manager.Search(ctx, memory.SearchRequest{
			OrganizationID: p.organizationID, ActorRoleID: request.ActorRoleID, RoleID: request.ActorRoleID,
			QueryText: queryText, Limit: p.maxEntries,
		})
	} else {
		entries, err = p.manager.ListApproved(ctx, p.organizationID, request.ActorRoleID, p.maxEntries)
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
				return entries[i].ID < entries[j].ID
			}
			return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
		})
	}
	if err != nil {
		return nil, err
	}
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

func (p *Provider) ValidateVersion(ctx context.Context, actorRoleID string, expected contextengine.SourceRecord) error {
	if expected.Kind != contextengine.SourceApprovedMemory {
		return fmt.Errorf("memory version validation received source kind %s", expected.Kind)
	}
	actorRoleID = strings.TrimSpace(actorRoleID)
	if actorRoleID == "" {
		return errors.New("memory version validation requires the actor role the snapshot was built for")
	}
	// Revalidate through the narrow API that re-enforces org/role/status/data
	// class. Passing the real actor role is what gives that enforcement any
	// meaning: it is the same role ListApproved scoped the entry to, so an
	// entry belonging to a different role can never satisfy revalidation.
	// This argument replaces a literal "placeholder-for-role", which made
	// every real revalidation fail while the unit tests still passed.
	entry, err := p.manager.GetForRevalidation(ctx, p.organizationID, actorRoleID, expected.Reference)
	if err != nil {
		return err
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
	SourceKind    memory.SourceKind    `json:"source_kind"`
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
	payload, err := json.Marshal(renderedMemory{SchemaVersion: "approved-memory.context.v1", Category: entry.Category, Problem: entry.Problem, Correction: entry.Correction, SourceKind: entry.SourceKind, SourceRunID: entry.SourceRunID, EvidenceRefs: append([]memory.EvidenceRef(nil), entry.EvidenceRefs...)})
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
