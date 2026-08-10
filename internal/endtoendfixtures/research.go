package endtoendfixtures

import (
	"context"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/memory"
	memorypostgres "github.com/Mireuz13/explorarte-organization/internal/memory/postgres"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
	ragpostgres "github.com/Mireuz13/explorarte-organization/internal/rag/postgres"
)

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

// researchEvidence is what the "research.worker -> research.audit" stage
// actually approves before the executive orchestration phase begins — a
// real approved RAG document and a real approved memory entry, sourced
// through the same rag.Manager/memory.Manager production code every other
// R30 retrieval fixture uses. The executive worker's evidence_refs cite
// these exact identifiers (see support.go's fakeModelRuntime.output), so
// this is the fixture's "todos los gates duros... juntos" link between
// retrieval and executive closure.
type researchEvidence struct {
	ragDocumentID string
	ragChunkID    string
	memoryEntryID string
}

func (e researchEvidence) ragEvidenceRef() string    { return "rag-chunk:" + e.ragChunkID }
func (e researchEvidence) memoryEvidenceRef() string { return "memory-entry:" + e.memoryEntryID }

// seedResearchEvidence proposes and approves one RAG document (approved by
// investigacion/auditor_cerebro_empresa, proposed by
// investigacion/research_worker_hourly — the fixture's "research.worker ->
// research.audit" stage) and one organizational memory entry, then
// reindexes the RAG namespace so the approved document has a real chunk
// row to cite. investigacion is a leaderless unit (organization.yaml) —
// its work is never routed through internal/executive's department
// dispatch, which requires a led unit; this is why the research stage
// runs here, before Submit, rather than as an executive-orchestrated
// department task.
func seedResearchEvidence(ctx context.Context, platform *platformpostgres.Store, ragStore *ragpostgres.Store, memoryStore *memorypostgres.Store, clock *fixedClock, namespaceID, suffix string) (researchEvidence, error) {
	// Every step below sets clock.now to a FIXED offset from base, never
	// an accumulating "+1 more from wherever the clock ended up" — on a
	// replay, a document/entry that's already approved skips Review and
	// therefore consumes fewer Clock.Now calls than a fresh run did. A
	// cumulative clock would shift every later step's timestamp and turn
	// an otherwise identical replay into a canonical-hash mismatch against
	// the same idempotency key (mirrors internal/retrievalfixtures'
	// documented fix for the exact same class of bug).
	base := clock.now
	ragManager, err := rag.NewManager(rag.NewService(clock), ragStore, allowRAGGate{}, namespaceResolver{namespaceID: namespaceID}, nil, nil)
	if err != nil {
		return researchEvidence{}, err
	}
	docID := "e2e-research-" + suffix
	clock.now = base.Add(time.Second)
	version, _, err := ragManager.Propose(ctx, rag.ProposeRequest{
		IdempotencyKey: "idem-" + docID,
		Command: rag.ProposeCommand{
			ID: docID, DocumentID: docID, OrganizationID: fixtureOrganization,
			NamespaceKind: rag.NamespaceDepartment, NamespaceID: namespaceID, Version: 1,
			Title: "R30.1 end-to-end research finding", Body: "El subsistema de despacho ejecutivo procesa correctamente el caso sintetico " + suffix + ".",
			SourceKind: rag.SourceOperational, SourceReference: "evaluation:" + docID, SourceRunRef: "r30-14-fixture",
			EvidenceRefs: []rag.EvidenceRef{{Reference: "evidence:" + docID, Digest: rag.ContentHash(docID)}},
			ProposedBy:   researchWorkerRole,
			Admission: rag.AdmissionAttestation{
				DataClass: rag.DataOrganizational, AttestedBy: researchWorkerRole, SourceBoundary: "evaluation_fixture",
				EvidenceRef: "admission:" + docID, AttestedAt: base,
			},
		},
	})
	if err != nil {
		return researchEvidence{}, fmt.Errorf("propose research rag document: %w", err)
	}
	if version.Lifecycle == rag.LifecycleCandidate {
		clock.now = base.Add(2 * time.Second)
		version, err = ragManager.Review(ctx, rag.ReviewRequest{Mutation: rag.MutationRequest{
			OrganizationID: fixtureOrganization, VersionID: version.ID, ExpectedRevision: version.Revision,
			ActorRoleID: researchAuditRole, Reason: "audit de investigacion aprueba el hallazgo",
		}, Outcome: rag.ReviewApprove})
		if err != nil {
			return researchEvidence{}, fmt.Errorf("approve research rag document: %w", err)
		}
	}
	if version.Lifecycle != rag.LifecycleApproved {
		return researchEvidence{}, fmt.Errorf("research rag document %s has lifecycle %s, want approved", docID, version.Lifecycle)
	}
	generation, err := ragManager.Reindex(ctx, rag.ReindexRequest{
		OrganizationID: fixtureOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: namespaceID, ActorRoleID: researchAuditRole,
	})
	if err != nil {
		return researchEvidence{}, fmt.Errorf("reindex research namespace: %w", err)
	}
	var chunkID string
	if err := platform.Pool().QueryRow(ctx, `
SELECT c.chunk_id FROM rag_knowledge_chunks c
JOIN rag_knowledge_versions v ON v.organization_id=c.organization_id AND v.version_id=c.version_id
WHERE c.organization_id=$1 AND c.generation_id=$2 AND v.document_id=$3 LIMIT 1`,
		fixtureOrganization, generation.ID, docID).Scan(&chunkID); err != nil {
		return researchEvidence{}, fmt.Errorf("read approved research chunk: %w", err)
	}

	memoryManager, err := memory.NewManager(memory.NewService(clock), memoryStore, allowMemoryGate{}, nil)
	if err != nil {
		return researchEvidence{}, err
	}
	entryID := "e2e-research-memory-" + suffix
	clock.now = base.Add(3 * time.Second)
	entry, _, err := memoryManager.Propose(ctx, memory.ProposeRequest{
		IdempotencyKey: "idem-" + entryID,
		Command: memory.ProposeCommand{
			ID: entryID, OrganizationID: fixtureOrganization, RoleID: researchWorkerRole, Category: "evaluation_fixture",
			Problem:    "El caso sintetico " + suffix + " necesitaba una leccion aprobada para citar como evidencia.",
			Correction: "Aprobar la leccion vía research.audit antes de que el ejecutivo la cite.", SourceKind: memory.SourceSyntheticTest, SourceRunID: 30,
			EvidenceRefs: []memory.EvidenceRef{{Reference: "evidence:" + entryID, Digest: rag.ContentHash(entryID)}},
			ProposedBy:   researchWorkerRole,
			Admission: memory.AdmissionAttestation{
				DataClass: memory.DataOrganizational, AttestedBy: researchWorkerRole, SourceBoundary: "evaluation_fixture",
				EvidenceRef: "admission:" + entryID, AttestedAt: base.Add(2 * time.Second),
			},
		},
	})
	if err != nil {
		return researchEvidence{}, fmt.Errorf("propose research memory entry: %w", err)
	}
	if entry.Status == memory.StatusCandidate {
		clock.now = base.Add(4 * time.Second)
		entry, err = memoryManager.Review(ctx, memory.ReviewRequest{Mutation: memory.MutationRequest{
			OrganizationID: fixtureOrganization, EntryID: entry.ID, ExpectedRevision: entry.Revision,
			ActorRoleID: researchAuditRole, Reason: "audit de investigacion aprueba la leccion",
		}, Outcome: memory.ReviewApprove})
		if err != nil {
			return researchEvidence{}, fmt.Errorf("approve research memory entry: %w", err)
		}
	}
	if entry.Status != memory.StatusApproved {
		return researchEvidence{}, fmt.Errorf("research memory entry %s has status %s, want approved", entryID, entry.Status)
	}

	return researchEvidence{ragDocumentID: docID, ragChunkID: chunkID, memoryEntryID: entry.ID}, nil
}
