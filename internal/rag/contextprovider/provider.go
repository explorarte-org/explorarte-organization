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

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

type Provider struct {
	manager        *rag.Manager
	organizationID string
	maxResults     int
}

func New(manager *rag.Manager, organizationID string, maxResults int) (*Provider, error) {
	if manager == nil {
		return nil, errors.New("rag context provider requires manager")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("rag context provider requires organization ID")
	}
	if maxResults <= 0 {
		return nil, errors.New("rag context provider requires positive max results")
	}
	return &Provider{manager: manager, organizationID: organizationID, maxResults: maxResults}, nil
}

var _ contextengine.RAGEvidenceProvider = (*Provider)(nil)

func (p *Provider) ListApprovedEvidence(ctx context.Context, request contextengine.BuildRequest) ([]contextengine.SourceRecord, error) {
	if request.OrganizationID != p.organizationID {
		return nil, fmt.Errorf("rag provider organization mismatch: request=%s configured=%s", request.OrganizationID, p.organizationID)
	}
	queryText := strings.TrimSpace(request.Purpose)
	if queryText == "" {
		return []contextengine.SourceRecord{}, nil
	}
	var results []rag.QueryResult
	for _, scope := range []rag.NamespaceKind{rag.NamespaceOwn, rag.NamespaceDepartment} {
		scoped, err := p.manager.Query(ctx, rag.QueryRequest{OrganizationID: p.organizationID, ActorRoleID: request.ActorRoleID, Scope: scope, QueryText: queryText, Limit: p.maxResults})
		if err != nil {
			if errors.Is(err, authorization.ErrCapabilityDenied) || errors.Is(err, authorization.ErrApprovalRequired) || errors.Is(err, rag.ErrInvalidNamespace) {
				continue
			}
			return nil, err
		}
		results = append(results, scoped...)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Chunk.ID < results[j].Chunk.ID
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > p.maxResults {
		results = results[:p.maxResults]
	}
	records := make([]contextengine.SourceRecord, 0, len(results))
	for _, result := range results {
		record, err := sourceRecord(result)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (p *Provider) ValidateVersion(ctx context.Context, expected contextengine.SourceRecord) error {
	if expected.Kind != contextengine.SourceRAGEvidence {
		return fmt.Errorf("rag version validation received source kind %s", expected.Kind)
	}
	meta, err := parseVersion(expected.Version)
	if err != nil {
		return err
	}
	version, err := p.manager.Get(ctx, p.organizationID, meta.versionID)
	if err != nil {
		return err
	}
	if version.OrganizationID != p.organizationID {
		return fmt.Errorf("rag knowledge %s crossed organization boundary", version.ID)
	}
	if version.Lifecycle != rag.LifecycleApproved {
		return fmt.Errorf("rag knowledge %s is no longer approved (lifecycle=%s)", version.ID, version.Lifecycle)
	}
	if version.CanonicalHash != meta.canonicalHash {
		return fmt.Errorf("rag knowledge %s version drift: expected %s got %s", version.ID, meta.canonicalHash, version.CanonicalHash)
	}
	active, ok, err := p.manager.ActiveGeneration(ctx, p.organizationID, meta.namespaceKind, meta.namespaceID)
	if err != nil {
		return err
	}
	if !ok || active.ID != meta.generationID {
		return fmt.Errorf("rag knowledge namespace %s/%s active index generation changed", meta.namespaceKind, meta.namespaceID)
	}
	return nil
}

type renderedChunk struct {
	SchemaVersion   string            `json:"schema_version"`
	DocumentID      string            `json:"document_id"`
	Title           string            `json:"title"`
	Content         string            `json:"content"`
	SourceReference string            `json:"source_reference"`
	EvidenceRefs    []rag.EvidenceRef `json:"evidence_refs"`
	NamespaceKind   rag.NamespaceKind `json:"namespace_kind"`
	NamespaceID     string            `json:"namespace_id"`
}

type versionMeta struct {
	namespaceKind rag.NamespaceKind
	namespaceID   string
	versionID     string
	canonicalHash string
	generationID  string
	chunkHash     string
}

func encodeVersion(m versionMeta) string {
	return strings.Join([]string{"rag-knowledge-chunk.v1", string(m.namespaceKind), m.namespaceID, m.versionID, m.canonicalHash, m.generationID, m.chunkHash}, ":")
}

func parseVersion(value string) (versionMeta, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 7 || parts[0] != "rag-knowledge-chunk.v1" {
		return versionMeta{}, fmt.Errorf("rag version string is malformed: %q", value)
	}
	return versionMeta{namespaceKind: rag.NamespaceKind(parts[1]), namespaceID: parts[2], versionID: parts[3], canonicalHash: parts[4], generationID: parts[5], chunkHash: parts[6]}, nil
}

func sourceRecord(result rag.QueryResult) (contextengine.SourceRecord, error) {
	dataClass, err := mapDataClass(result.DataClass)
	if err != nil {
		return contextengine.SourceRecord{}, err
	}
	payload, err := json.Marshal(renderedChunk{
		SchemaVersion: "approved-knowledge.context.v1", DocumentID: result.DocumentID, Title: result.Title, Content: result.Chunk.Content,
		SourceReference: result.SourceReference, EvidenceRefs: append([]rag.EvidenceRef(nil), result.EvidenceRefs...),
		NamespaceKind: result.NamespaceKind, NamespaceID: result.NamespaceID,
	})
	if err != nil {
		return contextengine.SourceRecord{}, fmt.Errorf("render rag evidence %s: %w", result.Chunk.ID, err)
	}
	contentSum := sha256.Sum256(payload)
	priority, ok := contextengine.AuthorityPriority(contextengine.TierRAGEvidence)
	if !ok {
		return contextengine.SourceRecord{}, errors.New("rag evidence authority tier is not registered")
	}
	version := encodeVersion(versionMeta{namespaceKind: result.NamespaceKind, namespaceID: result.NamespaceID, versionID: result.Chunk.VersionID, canonicalHash: result.CanonicalHash, generationID: result.GenerationID, chunkHash: result.Chunk.ContentHash})
	record := contextengine.SourceRecord{
		Kind: contextengine.SourceRAGEvidence, Reference: result.Chunk.ID, Version: version,
		AuthorityTier: contextengine.TierRAGEvidence, AuthorityPriority: priority, InstructionClass: contextengine.InstructionData, TrustClass: contextengine.TrustUntrusted,
		DataClass: dataClass, MayGrantCapabilities: false, Content: payload, ContentHash: hex.EncodeToString(contentSum[:]),
		Included: true, Relevance: 1, ProviderPriority: 1,
	}
	if err := contextengine.ValidateSourceMetadata(record); err != nil {
		return contextengine.SourceRecord{}, err
	}
	return record, nil
}

func mapDataClass(value rag.DataClass) (contextengine.DataClass, error) {
	switch value {
	case rag.DataPublic:
		return contextengine.DataPublic, nil
	case rag.DataOrganizational:
		return contextengine.DataOrganizational, nil
	case rag.DataSanitized:
		return contextengine.DataSanitized, nil
	case rag.DataSecret, rag.DataClinical:
		return "", fmt.Errorf("forbidden rag data class %s reached context provider", value)
	default:
		return "", fmt.Errorf("unknown rag data class %s", value)
	}
}
