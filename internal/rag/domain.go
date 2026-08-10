package rag

import "time"

type DataClass string

const (
	DataPublic         DataClass = "public"
	DataOrganizational DataClass = "organizational"
	DataSanitized      DataClass = "sanitized"
	DataSecret         DataClass = "secret"
	DataClinical       DataClass = "clinical"
)

func (c DataClass) Valid() bool {
	switch c {
	case DataPublic, DataOrganizational, DataSanitized, DataSecret, DataClinical:
		return true
	default:
		return false
	}
}

func (c DataClass) AllowedInApprovedKnowledge() bool {
	switch c {
	case DataPublic, DataOrganizational, DataSanitized:
		return true
	default:
		return false
	}
}

type SourceKind string

const (
	SourceResearch    SourceKind = "research"
	SourceOperational SourceKind = "operational"
	SourceHuman       SourceKind = "human"
)

func (k SourceKind) Valid() bool {
	switch k {
	case SourceResearch, SourceOperational, SourceHuman:
		return true
	default:
		return false
	}
}

type NamespaceKind string

const (
	NamespaceDepartment NamespaceKind = "department"
	NamespaceOwn        NamespaceKind = "own"
)

func (k NamespaceKind) Valid() bool { return k == NamespaceDepartment || k == NamespaceOwn }

type Lifecycle string

const (
	LifecycleCandidate  Lifecycle = "candidate"
	LifecycleApproved   Lifecycle = "approved"
	LifecycleRejected   Lifecycle = "rejected"
	LifecycleDeprecated Lifecycle = "deprecated"
	LifecycleArchived   Lifecycle = "archived"
)

func (l Lifecycle) Valid() bool {
	switch l {
	case LifecycleCandidate, LifecycleApproved, LifecycleRejected, LifecycleDeprecated, LifecycleArchived:
		return true
	default:
		return false
	}
}

type EvidenceRef struct {
	Reference string `json:"reference"`
	Digest    string `json:"digest"`
}

type AdmissionAttestation struct {
	DataClass               DataClass `json:"data_class"`
	AttestedBy              string    `json:"attested_by"`
	SourceBoundary          string    `json:"source_boundary"`
	EvidenceRef             string    `json:"evidence_ref"`
	SanitizationEvidenceRef string    `json:"sanitization_evidence_ref,omitempty"`
	AttestedAt              time.Time `json:"attested_at"`
}

type KnowledgeDocument struct {
	ID             string        `json:"id"`
	OrganizationID string        `json:"organization_id"`
	NamespaceKind  NamespaceKind `json:"namespace_kind"`
	NamespaceID    string        `json:"namespace_id"`
	CreatedByRole  string        `json:"created_by_role"`
	CreatedAt      time.Time     `json:"created_at"`
}

type KnowledgeVersion struct {
	ID                  string               `json:"id"`
	DocumentID          string               `json:"document_id"`
	OrganizationID      string               `json:"organization_id"`
	NamespaceKind       NamespaceKind        `json:"namespace_kind"`
	NamespaceID         string               `json:"namespace_id"`
	Version             int64                `json:"version"`
	Title               string               `json:"title"`
	Body                string               `json:"body"`
	SourceKind          SourceKind           `json:"source_kind"`
	SourceReference     string               `json:"source_reference"`
	SourceRunRef        string               `json:"source_run_ref,omitempty"`
	EvidenceRefs        []EvidenceRef        `json:"evidence_refs"`
	ProposedBy          string               `json:"proposed_by"`
	Admission           AdmissionAttestation `json:"admission"`
	ContentHash         string               `json:"content_hash"`
	CanonicalHash       string               `json:"canonical_hash"`
	SupersedesVersionID string               `json:"supersedes_version_id,omitempty"`
	Lifecycle           Lifecycle            `json:"lifecycle"`
	ReviewerID          string               `json:"reviewer_id,omitempty"`
	ReviewedAt          *time.Time           `json:"reviewed_at,omitempty"`
	Revision            int64                `json:"revision"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
}

// MediaSourceRef/MediaMimeType are both empty for a normal text chunk
// (its embedding comes from Content, as always) or both set together for
// a media-backed chunk (e.g. one PDF page): MediaSourceRef is an Object
// Storage key pointing at the exact bytes to embed — always a single
// self-contained item (one PDF page, one image, ...), never a multi-page
// container, so embedding a media chunk never needs its own
// page-splitting logic, only a fetch + Embed call. Content still holds
// this item's extracted text (FTS, citations, snippets, debugging,
// provenance) even when it is not what gets embedded.
type Chunk struct {
	ID                    string `json:"id"`
	VersionID             string `json:"version_id"`
	GenerationID          string `json:"generation_id,omitempty"`
	ChunkerID             string `json:"chunker_id"`
	ChunkerVersion        string `json:"chunker_version"`
	Ordinal               int    `json:"ordinal"`
	StartOffset           int    `json:"start_offset"`
	EndOffset             int    `json:"end_offset"`
	Content               string `json:"content"`
	ContentHash           string `json:"content_hash"`
	EmbeddingModelID      string `json:"embedding_model_id,omitempty"`
	EmbeddingModelVersion string `json:"embedding_model_version,omitempty"`
	EmbeddingDimension    int    `json:"embedding_dimension,omitempty"`
	MediaSourceRef        string `json:"media_source_ref,omitempty"`
	MediaMimeType         string `json:"media_mime_type,omitempty"`
}

// IsMedia reports whether this chunk's embedding should come from
// fetching MediaSourceRef's bytes rather than embedding Content directly.
func (c Chunk) IsMedia() bool { return c.MediaSourceRef != "" }

type GenerationStatus string

const (
	GenerationBuilding   GenerationStatus = "building"
	GenerationActive     GenerationStatus = "active"
	GenerationSuperseded GenerationStatus = "superseded"
)

func (s GenerationStatus) Valid() bool {
	switch s {
	case GenerationBuilding, GenerationActive, GenerationSuperseded:
		return true
	default:
		return false
	}
}

type IndexGeneration struct {
	ID             string           `json:"id"`
	OrganizationID string           `json:"organization_id"`
	NamespaceKind  NamespaceKind    `json:"namespace_kind"`
	NamespaceID    string           `json:"namespace_id"`
	Generation     int64            `json:"generation"`
	Status         GenerationStatus `json:"status"`
	ChunkerID      string           `json:"chunker_id"`
	ChunkerVersion string           `json:"chunker_version"`
	CreatedAt      time.Time        `json:"created_at"`
	ActivatedAt    *time.Time       `json:"activated_at,omitempty"`
}

type QueryResult struct {
	Chunk           Chunk         `json:"chunk"`
	DocumentID      string        `json:"document_id"`
	Title           string        `json:"title"`
	NamespaceKind   NamespaceKind `json:"namespace_kind"`
	NamespaceID     string        `json:"namespace_id"`
	SourceReference string        `json:"source_reference"`
	EvidenceRefs    []EvidenceRef `json:"evidence_refs"`
	CanonicalHash   string        `json:"canonical_hash"`
	DataClass       DataClass     `json:"data_class"`
	GenerationID    string        `json:"generation_id"`
	Score           float64       `json:"score"`
}
