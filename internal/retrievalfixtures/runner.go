package retrievalfixtures

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
	"github.com/Mireuz13/explorarte-organization/internal/evaluationdb"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
	memorypostgres "github.com/Mireuz13/explorarte-organization/internal/memory/postgres"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
	ragpostgres "github.com/Mireuz13/explorarte-organization/internal/rag/postgres"
)

// Runner executes R30 fixtures 03-07 through the production managers and
// PostgreSQL repositories. Embeddings are deterministic test vectors; this
// validates retrieval wiring and invariants, not BGE-M3 hardware health.
type Runner struct{ Store *platformpostgres.Store }

var _ fixtures.Runner = Runner{}

func (Runner) Supports(f fixtures.Fixture) bool {
	_, ok := supportedFixtureIDs[f.ID]
	return ok && f.RunnerKind == "retrieval" && f.Status == fixtures.StatusRunnerReady
}

func (r Runner) Run(ctx context.Context, f fixtures.Fixture, subjectID string) (fixtures.RunOutcome, error) {
	if err := evaluationdb.RequireDisposable(ctx, r.Store); err != nil {
		return fixtures.RunOutcome{}, err
	}
	scenario, ok := f.Scenario.(*Scenario)
	if !ok || scenario.FixtureID != f.ID {
		return fixtures.RunOutcome{}, fmt.Errorf("fixture %s was not activated by retrievalfixtures.Activate", f.ID)
	}
	profile, err := profileForSubject(subjectID)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	ragStore, err := ragpostgres.New(r.Store, fixtureOrganization)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	memoryStore, err := memorypostgres.New(r.Store, fixtureOrganization)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	switch f.ID {
	case fixtureIdentifierHardNegatives:
		return r.runIdentifierHardNegatives(ctx, f, profile, ragStore)
	case fixtureSemanticParaphrase:
		return r.runSemanticParaphrase(ctx, f, profile, ragStore)
	case fixtureMemoryRelevance:
		return r.runMemoryRelevance(ctx, f, profile, memoryStore)
	case fixtureCrossNamespace:
		return r.runCrossNamespace(ctx, f, profile, ragStore, memoryStore)
	case fixtureRejectedAdmission:
		return r.runRejectedAdmission(ctx, f, profile, ragStore, memoryStore)
	default:
		return fixtures.RunOutcome{}, fmt.Errorf("unsupported retrieval fixture %s", f.ID)
	}
}

type recorder struct {
	outcome fixtures.RunOutcome
	passed  bool
}

func newRecorder(f fixtures.Fixture, subjectID string) *recorder {
	return &recorder{passed: true, outcome: fixtures.RunOutcome{
		FixtureID: f.ID, SubjectID: subjectID, InvariantResults: map[string]bool{}, Metrics: map[string]float64{},
		EvidenceRefs: append([]string(nil), f.ExpectedEvidence...),
	}}
}

func (r *recorder) record(name string, passed bool) {
	r.outcome.InvariantResults[name] = passed
	if !passed {
		r.passed = false
		r.outcome.ViolatedInvariants = append(r.outcome.ViolatedInvariants, name)
	}
}

func (r *recorder) finish(notes string) fixtures.RunOutcome {
	r.outcome.Passed = r.passed
	r.outcome.Notes = notes
	return r.outcome
}

func (r Runner) runIdentifierHardNegatives(ctx context.Context, f fixtures.Fixture, profile retrievalProfile, store *ragpostgres.Store) (fixtures.RunOutcome, error) {
	record := newRecorder(f, profile.name)
	clock := &fixedClock{now: fixtureBaseTime(f.Seed)}
	namespaceID := fixtureNamespace(f.ID, profile.name)
	setupManager, err := newRAGManager(store, clock, namespaceID, ragGate{}, nil)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	prefix := "eval-id-" + fixtureSuffix(f.ID, profile.name)
	positiveID, negativeID := prefix+"-20", prefix+"-2000"
	docs := map[string]string{
		positiveID:     "Se registro un error-20 durante el despliegue nocturno.",
		negativeID:     "Se registro error 2000 durante otra auditoria.",
		prefix + "-d1": "El presupuesto trimestral fue aprobado por finanzas.",
		prefix + "-d2": "La politica de vacaciones cambia el proximo semestre.",
		prefix + "-d3": "El catalogo visual recibio nuevas fotografias.",
		prefix + "-d4": "La reunion operativa quedo para el jueves.",
	}
	corpus, err := prepareRAGCorpus(ctx, r.Store, store, setupManager, clock, namespaceID, docs)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	kinds := make(map[string]string, len(docs))
	for id := range docs {
		kinds[id] = "orthogonal"
	}
	kinds[positiveID], kinds[negativeID] = "close", "opposite"
	if err := seedRAGEmbeddings(ctx, store, corpus, kinds, clock.now); err != nil {
		return fixtures.RunOutcome{}, err
	}
	adapter := &deterministicAdapter{dimension: profile.dimension}
	manager, err := newRAGManager(store, clock, namespaceID, ragGate{}, semanticDepsForRAG(profile, adapter))
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	results, err := manager.Query(ctx, rag.QueryRequest{
		OrganizationID: fixtureOrganization, ActorRoleID: fixtureActor, Scope: rag.NamespaceDepartment,
		QueryText: "error 20", Limit: 3,
	})
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	positiveFound := containsDocument(results, positiveID)
	negativeFound := containsDocument(results, negativeID)
	record.record("identifier_20_is_retrieved", positiveFound)
	record.record("identifier_20_never_conflates_with_2000", !negativeFound)
	record.record("exact_channel_remains_active", positiveFound)
	record.outcome.Metrics["recall_at_3"] = boolMetric(positiveFound)
	record.outcome.Metrics["numeric_false_positive_rate_at_3"] = boolMetric(negativeFound)
	for _, result := range results {
		record.outcome.EvidenceRefs = append(record.outcome.EvidenceRefs, "rag-chunk:"+result.Chunk.ID)
	}
	return record.finish("vectores sintéticos deterministas; no prueba el sidecar BGE-M3 real"), nil
}

func (r Runner) runSemanticParaphrase(ctx context.Context, f fixtures.Fixture, profile retrievalProfile, store *ragpostgres.Store) (fixtures.RunOutcome, error) {
	record := newRecorder(f, profile.name)
	clock := &fixedClock{now: fixtureBaseTime(f.Seed)}
	namespaceID := fixtureNamespace(f.ID, profile.name)
	setupManager, err := newRAGManager(store, clock, namespaceID, ragGate{}, nil)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	prefix := "eval-sem-" + fixtureSuffix(f.ID, profile.name)
	semanticID := prefix + "-relevant"
	docs := map[string]string{
		semanticID:     "La vigésima interrupción detuvo la canalización de ingesta.",
		prefix + "-d1": "El equipo revisa el presupuesto cada lunes.",
		prefix + "-d2": "La biblioteca actualizo su inventario.",
		prefix + "-d3": "La sala principal recibio nuevas sillas.",
		prefix + "-d4": "El informe financiero se publica en abril.",
	}
	corpus, err := prepareRAGCorpus(ctx, r.Store, store, setupManager, clock, namespaceID, docs)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	kinds := make(map[string]string, len(docs))
	for id := range docs {
		kinds[id] = "orthogonal"
	}
	kinds[semanticID] = "close"
	if err := seedRAGEmbeddings(ctx, store, corpus, kinds, clock.now); err != nil {
		return fixtures.RunOutcome{}, err
	}
	adapter := &deterministicAdapter{dimension: profile.dimension}
	manager, err := newRAGManager(store, clock, namespaceID, ragGate{}, semanticDepsForRAG(profile, adapter))
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	results, err := manager.Query(ctx, rag.QueryRequest{
		OrganizationID: fixtureOrganization, ActorRoleID: fixtureActor, Scope: rag.NamespaceDepartment,
		QueryText: "fallo número veinte", Limit: 3,
	})
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	found := containsDocument(results, semanticID)
	if profile.name == "lexical" {
		record.record("lexical_baseline_does_not_claim_semantic_recall", !found)
	} else {
		record.record("hybrid_profile_recovers_semantic_paraphrase", found)
	}
	record.outcome.Metrics["recall_at_3"] = boolMetric(found)
	record.outcome.Metrics["embedding_calls"] = float64(adapter.calls.Load())
	return record.finish("comparación por perfil; embeddings del runner son deterministas"), nil
}

func (r Runner) runMemoryRelevance(ctx context.Context, f fixtures.Fixture, profile retrievalProfile, store *memorypostgres.Store) (fixtures.RunOutcome, error) {
	record := newRecorder(f, profile.name)
	profile = scopedMemoryProfile(profile, f.ID, profile.name)
	clock := &fixedClock{now: fixtureBaseTime(f.Seed).Add(-30 * 24 * time.Hour)}
	setupManager, err := newMemoryManager(store, clock, nil)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	prefix := "eval-memory-" + fixtureSuffix(f.ID, profile.name)
	oldID, recentID, candidateID := prefix+"-old", prefix+"-recent", prefix+"-candidate"
	roleID := fixtureActor
	old, err := proposeMemory(ctx, setupManager, clock, oldID, roleID,
		"Las goroutines quedaron en espera circular por bloqueo mutuo.", "Ordenar la adquisición de locks evita el deadlock.", true)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	clock.now = fixtureBaseTime(f.Seed)
	recent, err := proposeMemory(ctx, setupManager, clock, recentID, roleID,
		"La paleta visual del panel necesita revisión.", "Usar el nuevo color institucional.", true)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	clock.now = fixtureBaseTime(f.Seed).Add(10 * time.Second)
	if _, err := proposeMemory(ctx, setupManager, clock, candidateID, roleID,
		"Un deadlock candidato aun no fue revisado.", "No debe recuperarse antes de aprobación.", false); err != nil {
		return fixtures.RunOutcome{}, err
	}
	if err := seedMemoryEmbeddingSpace(ctx, store, profile, map[string]string{old.ID: "close", recent.ID: "orthogonal"}, clock.now); err != nil {
		return fixtures.RunOutcome{}, err
	}
	adapter := &deterministicAdapter{dimension: profile.dimension}
	manager, err := newMemoryManager(store, clock, semanticDepsForMemory(profile, adapter, store))
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	results, err := manager.Search(ctx, memory.SearchRequest{
		OrganizationID: fixtureOrganization, ActorRoleID: roleID, RoleID: roleID,
		QueryText: "como resolver un deadlock", Limit: 3,
	})
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	oldFirst := len(results) > 0 && results[0].ID == oldID
	record.record("old_relevant_memory_ranks_before_recent_irrelevant_memory", oldFirst)
	record.record("candidate_memory_is_never_returned", !containsEntry(results, candidateID))
	record.record("search_stays_inside_actor_role", allEntriesBelongTo(results, roleID))
	record.outcome.Metrics["old_relevant_rank_1"] = boolMetric(oldFirst)
	record.outcome.Metrics["embedding_calls"] = float64(adapter.calls.Load())
	return record.finish("el perfil lexical conserva la línea base por recencia y puede fallar este fixture por diseño"), nil
}

func (r Runner) runCrossNamespace(ctx context.Context, f fixtures.Fixture, profile retrievalProfile, ragStore *ragpostgres.Store, memoryStore *memorypostgres.Store) (fixtures.RunOutcome, error) {
	record := newRecorder(f, profile.name)
	clock := &fixedClock{now: fixtureBaseTime(f.Seed)}
	targetNamespace := fixtureNamespace(f.ID, profile.name) + "_target"
	targetActor := "marketing/estratega_crecimiento"
	deniedActor := fixtureActor
	setupManager, err := newRAGManager(ragStore, clock, targetNamespace, ragGate{}, nil)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	documentID := fixtureID("eval-private", f.ID, profile.name)
	corpus, err := prepareRAGCorpus(ctx, r.Store, ragStore, setupManager, clock, targetNamespace, map[string]string{
		documentID: "El proyecto alfa usa el identificador controlado 884422.",
	})
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	if err := seedRAGEmbeddings(ctx, ragStore, corpus, map[string]string{documentID: "close"}, clock.now); err != nil {
		return fixtures.RunOutcome{}, err
	}
	controlManager, err := newRAGManager(ragStore, clock, targetNamespace, ragGate{}, nil)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	control, err := controlManager.Query(ctx, rag.QueryRequest{
		OrganizationID: fixtureOrganization, ActorRoleID: targetActor, Scope: rag.NamespaceDepartment,
		QueryText: "884422", Limit: 3,
	})
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	record.record("control_query_proves_foreign_content_exists", containsDocument(control, documentID))

	profiles := []retrievalProfile{{name: "lexical"}, {name: "gemini-hybrid", dimension: geminiDimension, identity: geminiIdentity(), prompt: geminiPrompt}, {name: "bge-m3-hybrid", dimension: bgeM3Dimension, identity: bgeM3Identity(), prompt: bgeM3Prompt}}
	allDeniedBeforeEgress := true
	for _, candidate := range profiles {
		adapter := &deterministicAdapter{dimension: candidate.dimension}
		manager, managerErr := newRAGManager(ragStore, clock, targetNamespace, ragGate{deniedActor: deniedActor}, semanticDepsForRAG(candidate, adapter))
		if managerErr != nil {
			return fixtures.RunOutcome{}, managerErr
		}
		results, queryErr := manager.Query(ctx, rag.QueryRequest{
			OrganizationID: fixtureOrganization, ActorRoleID: deniedActor, Scope: rag.NamespaceDepartment,
			QueryText: "884422", Limit: 3,
		})
		if !errors.Is(queryErr, errFixtureAuthorizationDenied) || len(results) != 0 || adapter.calls.Load() != 0 {
			allDeniedBeforeEgress = false
		}
	}
	record.record("rag_cross_namespace_is_denied_in_all_profiles", allDeniedBeforeEgress)

	memorySetup, err := newMemoryManager(memoryStore, clock, nil)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	memoryID := fixtureID("eval-private-memory", f.ID, profile.name)
	clock.now = fixtureBaseTime(f.Seed).Add(5 * time.Minute)
	if _, err := proposeMemory(ctx, memorySetup, clock, memoryID, targetActor,
		"El identificador controlado es 884422.", "Mantenerlo dentro del rol propietario.", true); err != nil {
		return fixtures.RunOutcome{}, err
	}
	adapter := &deterministicAdapter{dimension: bgeM3Dimension}
	memoryManager, err := newMemoryManager(memoryStore, clock, semanticDepsForMemory(retrievalProfile{name: "bge-m3-hybrid", dimension: bgeM3Dimension, identity: bgeM3Identity(), prompt: bgeM3Prompt}, adapter, memoryStore))
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	memoryResults, memoryErr := memoryManager.Search(ctx, memory.SearchRequest{
		OrganizationID: fixtureOrganization, ActorRoleID: deniedActor, RoleID: targetActor,
		QueryText: "884422", Limit: 3,
	})
	record.record("memory_cross_role_is_denied_before_embedding", memoryErr != nil && len(memoryResults) == 0 && adapter.calls.Load() == 0)
	return record.finish("la autorización se evalúa antes del adapter y del repositorio de búsqueda"), nil
}

func (r Runner) runRejectedAdmission(ctx context.Context, f fixtures.Fixture, profile retrievalProfile, ragStore *ragpostgres.Store, memoryStore *memorypostgres.Store) (fixtures.RunOutcome, error) {
	record := newRecorder(f, profile.name)
	clock := &fixedClock{now: fixtureBaseTime(f.Seed)}
	namespaceID := fixtureNamespace(f.ID, profile.name)
	ragManager, err := newRAGManager(ragStore, clock, namespaceID, ragGate{}, nil)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	ragID := fixtureID("eval-rejected-rag", f.ID, profile.name)
	clock.now = clock.now.Add(time.Second)
	version, _, err := ragManager.Propose(ctx, rag.ProposeRequest{IdempotencyKey: "idem-" + ragID, Command: rag.ProposeCommand{
		ID: ragID, DocumentID: ragID, OrganizationID: fixtureOrganization, NamespaceKind: rag.NamespaceDepartment,
		NamespaceID: namespaceID, Version: 1, Title: "Rejected fixture", Body: "Marcador rechazado 771144.",
		SourceKind: rag.SourceOperational, SourceReference: "evaluation:" + ragID, SourceRunRef: "r30-fixture",
		EvidenceRefs: []rag.EvidenceRef{{Reference: "evidence:" + ragID, Digest: rag.ContentHash(ragID)}}, ProposedBy: fixtureActor,
		Admission: rag.AdmissionAttestation{DataClass: rag.DataOrganizational, AttestedBy: fixtureActor, SourceBoundary: "evaluation_fixture", EvidenceRef: "admission:" + ragID, AttestedAt: clock.now.Add(-time.Second)},
	}})
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	if version.Lifecycle == rag.LifecycleCandidate {
		version, err = ragManager.Review(ctx, rag.ReviewRequest{Mutation: rag.MutationRequest{
			OrganizationID: fixtureOrganization, VersionID: version.ID, ExpectedRevision: version.Revision,
			ActorRoleID: fixtureReviewer, Reason: "fixture rejection",
		}, Outcome: rag.ReviewReject})
		if err != nil {
			return fixtures.RunOutcome{}, err
		}
	}
	record.record("rag_candidate_finishes_rejected", version.Lifecycle == rag.LifecycleRejected)
	_, retryErr := ragManager.Review(ctx, rag.ReviewRequest{Mutation: rag.MutationRequest{
		OrganizationID: fixtureOrganization, VersionID: version.ID, ExpectedRevision: version.Revision,
		ActorRoleID: fixtureReviewer, Reason: "replayed rejection",
	}, Outcome: rag.ReviewReject})
	record.record("rag_replayed_rejection_cannot_change_terminal_state", retryErr != nil)
	if _, err := ragManager.Reindex(ctx, rag.ReindexRequest{
		OrganizationID: fixtureOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: namespaceID, ActorRoleID: fixtureReviewer,
	}); err != nil {
		return fixtures.RunOutcome{}, err
	}
	ragResults, err := ragManager.Query(ctx, rag.QueryRequest{
		OrganizationID: fixtureOrganization, ActorRoleID: fixtureActor, Scope: rag.NamespaceDepartment,
		QueryText: "771144", Limit: 3,
	})
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	record.record("rejected_rag_content_is_not_retrievable", !containsDocument(ragResults, ragID))

	memoryProfile := profile
	if memoryProfile.dimension == 0 {
		memoryProfile = retrievalProfile{name: "bge-m3-hybrid", dimension: bgeM3Dimension, identity: bgeM3Identity(), prompt: bgeM3Prompt}
	}
	memoryProfile = scopedMemoryProfile(memoryProfile, f.ID, profile.name)
	memoryAdapter := &deterministicAdapter{dimension: memoryProfile.dimension}
	memoryManager, err := newMemoryManager(memoryStore, clock, semanticDepsForMemory(memoryProfile, memoryAdapter, memoryStore))
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	memoryID := fixtureID("eval-rejected-memory", f.ID, profile.name)
	memoryRoleID := fixtureActor
	clock.now = fixtureBaseTime(f.Seed).Add(5 * time.Minute)
	entry, err := proposeMemory(ctx, memoryManager, clock, memoryID, memoryRoleID,
		"Marcador rechazado 771144.", "Nunca promover sin revisión aprobatoria.", false)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	callsBeforeReview := memoryAdapter.calls.Load()
	if entry.Status == memory.StatusCandidate {
		entry, err = memoryManager.Review(ctx, memory.ReviewRequest{Mutation: memory.MutationRequest{
			OrganizationID: fixtureOrganization, EntryID: entry.ID, ExpectedRevision: entry.Revision,
			ActorRoleID: fixtureReviewer, Reason: "fixture rejection",
		}, Outcome: memory.ReviewReject})
		if err != nil {
			return fixtures.RunOutcome{}, err
		}
	}
	record.record("memory_candidate_finishes_rejected", entry.Status == memory.StatusRejected)
	record.record("rejected_memory_is_never_embedded", memoryAdapter.calls.Load() == callsBeforeReview)
	_, retryMemoryErr := memoryManager.Review(ctx, memory.ReviewRequest{Mutation: memory.MutationRequest{
		OrganizationID: fixtureOrganization, EntryID: entry.ID, ExpectedRevision: entry.Revision,
		ActorRoleID: fixtureReviewer, Reason: "replayed rejection",
	}, Outcome: memory.ReviewReject})
	record.record("memory_replayed_rejection_cannot_change_terminal_state", retryMemoryErr != nil)
	memoryResults, err := memoryManager.Search(ctx, memory.SearchRequest{
		OrganizationID: fixtureOrganization, ActorRoleID: memoryRoleID, RoleID: memoryRoleID,
		QueryText: "771144", Limit: 3,
	})
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	record.record("rejected_memory_content_is_not_retrievable", !containsEntry(memoryResults, memoryID))
	return record.finish("rechazo durable verificado en RAG y Memory; el reintento no lo promueve"), nil
}

func boolMetric(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func allEntriesBelongTo(entries []memory.Entry, roleID string) bool {
	for _, entry := range entries {
		if entry.RoleID != roleID || entry.Status != memory.StatusApproved {
			return false
		}
	}
	return true
}
