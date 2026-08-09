package webevidencefixtures

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/webevidence"
)

type renderedWebEvidenceChunk struct {
	SchemaVersion string `json:"schema_version"`
	EvidenceID    string `json:"evidence_id"`
	URL           string `json:"url"`
	ChunkOrdinal  int    `json:"chunk_ordinal"`
	Text          string `json:"text"`
}

// sourceRecord renders one chunk of webevidence.Evidence into a real
// contextengine.SourceRecord — the shape this content would take in
// production if/when a WebEvidenceProvider is wired into
// contextengine.Service (not yet built; see
// contextengine.SourceWebEvidence's doc comment). Every field
// DeterministicAssembler.Assemble's hard gate checks (InstructionClass,
// TrustClass, MayGrantCapabilities) is fixed here at the only
// classification web evidence is ever allowed to carry — mirrors
// internal/rag/contextprovider's and internal/memory/contextprovider's own
// sourceRecord() functions exactly, so this package's fixture exercises
// the real Context Engine hard gate instead of asserting a boolean about
// it.
func sourceRecord(evidence webevidence.Evidence, chunk webevidence.Chunk) (contextengine.SourceRecord, error) {
	if err := evidence.Validate(); err != nil {
		return contextengine.SourceRecord{}, err
	}
	payload, err := json.Marshal(renderedWebEvidenceChunk{
		SchemaVersion: "web-evidence.context.v1", EvidenceID: evidence.ID, URL: evidence.URL,
		ChunkOrdinal: chunk.Ordinal, Text: chunk.Text,
	})
	if err != nil {
		return contextengine.SourceRecord{}, fmt.Errorf("render web evidence %s chunk %d: %w", evidence.ID, chunk.Ordinal, err)
	}
	contentSum := sha256.Sum256(payload)
	// Web evidence shares RAG's authority tier — it is at least as
	// untrusted as approved-knowledge retrieval, and registering a
	// dedicated tier (precedence.yaml, RenderRank, hashing.go, ...) is
	// scope this fixture-only fix does not need; a real
	// WebEvidenceProvider can revisit this when it lands.
	priority, ok := contextengine.AuthorityPriority(contextengine.TierRAGEvidence)
	if !ok {
		return contextengine.SourceRecord{}, fmt.Errorf("web evidence authority tier is not registered")
	}
	record := contextengine.SourceRecord{
		Kind: contextengine.SourceWebEvidence, Reference: fmt.Sprintf("%s:%d", evidence.ID, chunk.Ordinal), Version: "sha256:" + evidence.ContentHash,
		AuthorityTier: contextengine.TierRAGEvidence, AuthorityPriority: priority,
		InstructionClass: contextengine.InstructionData, TrustClass: contextengine.TrustUntrusted,
		DataClass: contextengine.DataPublic, MayGrantCapabilities: false,
		Content: payload, ContentHash: hex.EncodeToString(contentSum[:]), Included: true, Relevance: 1, ProviderPriority: 1,
	}
	if err := contextengine.ValidateSourceMetadata(record); err != nil {
		return contextengine.SourceRecord{}, err
	}
	return record, nil
}
