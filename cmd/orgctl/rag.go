package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/dataclassifier"
	"github.com/Mireuz13/explorarte-organization/internal/pdfingest"
	"github.com/Mireuz13/explorarte-organization/internal/pdfingest/poppler"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
	ragbootstrap "github.com/Mireuz13/explorarte-organization/internal/rag/bootstrap"
)

type ragEvidenceInput struct {
	Reference string `json:"reference"`
	Digest    string `json:"digest"`
}
type ragAdmissionInput struct {
	DataClass               rag.DataClass `json:"data_class"`
	AttestedBy              string        `json:"attested_by"`
	SourceBoundary          string        `json:"source_boundary"`
	EvidenceRef             string        `json:"evidence_ref"`
	SanitizationEvidenceRef string        `json:"sanitization_evidence_ref,omitempty"`
	AttestedAt              string        `json:"attested_at"`
}
type ragProposeInput struct {
	ID                  string             `json:"id"`
	DocumentID          string             `json:"document_id"`
	NamespaceKind       rag.NamespaceKind  `json:"namespace_kind"`
	NamespaceID         string             `json:"namespace_id"`
	Version             int64              `json:"version"`
	Title               string             `json:"title"`
	Body                string             `json:"body"`
	SourceKind          rag.SourceKind     `json:"source_kind"`
	SourceReference     string             `json:"source_reference"`
	SourceRunRef        string             `json:"source_run_ref,omitempty"`
	EvidenceRefs        []ragEvidenceInput `json:"evidence_refs"`
	ProposedBy          string             `json:"proposed_by"`
	Admission           ragAdmissionInput  `json:"admission"`
	SupersedesVersionID string             `json:"supersedes_version_id,omitempty"`
	IdempotencyKey      string             `json:"idempotency_key"`
}
type ragReviewInput struct {
	VersionID         string `json:"version_id"`
	ExpectedRevision  int64  `json:"expected_revision"`
	ActorRoleID       string `json:"actor_role_id"`
	Reason            string `json:"reason"`
	Outcome           string `json:"outcome"`
	ApprovalRequestID *int64 `json:"approval_request_id,omitempty"`
}
type ragReindexInput struct {
	NamespaceKind     rag.NamespaceKind `json:"namespace_kind"`
	NamespaceID       string            `json:"namespace_id"`
	ActorRoleID       string            `json:"actor_role_id"`
	ApprovalRequestID *int64            `json:"approval_request_id,omitempty"`
	// PrecomputedChunksManifestRef, if set, is an Object Storage key
	// pointing at a JSON document (see pdfChunkManifest) this command
	// fetches and decodes into rag.ReindexRequest.PrecomputedChunks --
	// written by `orgctl pdf ingest`, consumed here once the document it
	// describes has been reviewed/approved. Bridges the gap between
	// ingestion (which computes chunks once, out of process) and
	// Reindex (which may run much later): Object Storage is the shared
	// durable point between the two commands, not a new database table.
	PrecomputedChunksManifestRef string `json:"precomputed_chunks_manifest_ref,omitempty"`
}

// pdfChunkManifest is the JSON shape `orgctl pdf ingest` writes to Object
// Storage and `orgctl rag reindex --file` (via
// PrecomputedChunksManifestRef) reads back. VersionID is the document-
// level KnowledgeVersion these chunks belong to; Chunks is exactly the
// []rag.Chunk slice Reindex needs for that version (see
// rag.ReindexRequest.PrecomputedChunks).
type pdfChunkManifest struct {
	VersionID string      `json:"version_id"`
	Chunks    []rag.Chunk `json:"chunks"`
}

// ragIngestPDFInput drives `orgctl rag ingest-pdf` -- the owner-approved
// PDF ingestion contract. LocalFile is read from local disk (matching
// `orgctl objectstorage seed`'s pattern for one-off operator-driven
// uploads); ObjectPrefix scopes where this document's pages/source land
// in the bucket (e.g. "papers/self-improving-agents"); ManifestObject is
// where the computed chunk list is written for a later `orgctl rag
// reindex --file` (via PrecomputedChunksManifestRef) to pick up once the
// proposed document has been reviewed and approved -- ingestion never
// calls Reindex itself, the human-approval gate is unchanged.
type ragIngestPDFInput struct {
	LocalFile      string            `json:"local_file"`
	ObjectPrefix   string            `json:"object_prefix"`
	NamespaceKind  rag.NamespaceKind `json:"namespace_kind"`
	NamespaceID    string            `json:"namespace_id"`
	DocumentID     string            `json:"document_id,omitempty"`
	Title          string            `json:"title"`
	SourceKind     rag.SourceKind    `json:"source_kind"`
	ProposedBy     string            `json:"proposed_by"`
	Admission      ragAdmissionInput `json:"admission"`
	IdempotencyKey string            `json:"idempotency_key"`
	ManifestObject string            `json:"manifest_object"`
}

type ragIngestPDFResult struct {
	Version        rag.KnowledgeVersion `json:"version"`
	Reused         bool                 `json:"reused"`
	PageCount      int                  `json:"page_count"`
	EmptyTextPages int                  `json:"empty_text_pages"`
	ParserName     string               `json:"parser_name"`
	ParserVersion  string               `json:"parser_version"`
	SourceSHA256   string               `json:"source_sha256"`
	SourceObject   string               `json:"source_object"`
	ManifestObject string               `json:"manifest_object"`
}

// runRAGIngestPDF is the whole owner-approved ingestion pipeline (steps
// a-h of the contract) except review/approval and embedding, which stay
// separate commands by design: split, extract, hash, upload, propose one
// document-level candidate, write the chunk manifest. A *pdfingest.
// QuarantineError from the processor is fail-closed -- this function
// returns before ever calling Manager.Propose, so a malformed/encrypted/
// unsupported PDF never produces a knowledge candidate.
func runRAGIngestPDF(ctx context.Context, runtime *ragbootstrap.Runtime, input ragIngestPDFInput) (ragIngestPDFResult, error) {
	if strings.TrimSpace(input.LocalFile) == "" || strings.TrimSpace(input.ObjectPrefix) == "" || strings.TrimSpace(input.ManifestObject) == "" {
		return ragIngestPDFResult{}, fmt.Errorf("%w: local_file, object_prefix, and manifest_object are required", rag.ErrInvalidRequest)
	}
	sourceBytes, err := os.ReadFile(input.LocalFile)
	if err != nil {
		return ragIngestPDFResult{}, fmt.Errorf("read local pdf: %w", err)
	}
	sourceSHA := sha256.Sum256(sourceBytes)
	sourceSHAHex := hex.EncodeToString(sourceSHA[:])

	procCfg, err := poppler.DefaultConfig()
	if err != nil {
		return ragIngestPDFResult{}, fmt.Errorf("poppler config: %w", err)
	}
	processor, err := poppler.New(procCfg)
	if err != nil {
		return ragIngestPDFResult{}, fmt.Errorf("poppler processor: %w", err)
	}
	pdfResult, err := processor.Process(ctx, sourceBytes)
	if err != nil {
		var quarantine *pdfingest.QuarantineError
		if errors.As(err, &quarantine) {
			return ragIngestPDFResult{}, fmt.Errorf("pdf quarantined (%s): %s -- no knowledge candidate proposed", quarantine.Reason, quarantine.Detail)
		}
		return ragIngestPDFResult{}, fmt.Errorf("process pdf: %w", err)
	}

	osClient, err := newObjectStorageClient()
	if err != nil {
		return ragIngestPDFResult{}, err
	}

	documentID := strings.TrimSpace(input.DocumentID)
	if documentID == "" {
		documentID = "pdf-" + sourceSHAHex[:16]
	}
	versionID := documentID + "-v1"
	prefix := strings.Trim(input.ObjectPrefix, "/")
	shaPrefix := sourceSHAHex[:12]

	sourceObject := fmt.Sprintf("raw/%s/%s/source.pdf", prefix, shaPrefix)
	if err := osClient.PutObject(ctx, sourceObject, sourceBytes, "application/pdf"); err != nil {
		return ragIngestPDFResult{}, fmt.Errorf("upload source pdf: %w", err)
	}

	var body strings.Builder
	chunks := make([]rag.Chunk, 0, len(pdfResult.Pages))
	emptyCount := 0
	for _, page := range pdfResult.Pages {
		pageObject := fmt.Sprintf("raw/%s/%s/page-%d.pdf", prefix, shaPrefix, page.PageNumber)
		if err := osClient.PutObject(ctx, pageObject, page.PDFBytes, "application/pdf"); err != nil {
			return ragIngestPDFResult{}, fmt.Errorf("upload page %d: %w", page.PageNumber, err)
		}
		content := page.ExtractedText
		endOffset := len(content)
		if endOffset == 0 {
			endOffset = 1
			emptyCount++
		} else {
			if body.Len() > 0 {
				body.WriteString("\n\n")
			}
			body.WriteString(content)
		}
		chunks = append(chunks, rag.Chunk{
			VersionID: versionID, ChunkerID: "pdf-page-v1", ChunkerVersion: "v1",
			Ordinal: page.PageNumber, StartOffset: 0, EndOffset: endOffset,
			Content: content, ContentHash: rag.ContentHash(content),
			MediaSourceRef: pageObject, MediaMimeType: "application/pdf",
			SourcePageNumber: page.PageNumber, MediaSHA256: page.SHA256,
			MediaParser: pdfResult.ParserName, MediaParserVersion: pdfResult.ParserVersion,
			TextExtractionStatus: rag.TextExtractionStatus(page.TextExtractionStatus),
		})
	}
	bodyText := body.String()
	if bodyText == "" {
		bodyText = fmt.Sprintf("[PDF sin texto extraible: %d paginas, ver chunks media-backed para el contenido visual]", len(pdfResult.Pages))
	}
	if finding := dataclassifier.Detect(bodyText); finding.Any() {
		return ragIngestPDFResult{}, fmt.Errorf("%w: extracted text matched a forbidden data pattern, refusing to propose", rag.ErrInvalidRequest)
	}

	attestedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.Admission.AttestedAt))
	if err != nil {
		attestedAt = time.Now().UTC()
	}
	version, reused, err := runtime.Manager.Propose(ctx, rag.ProposeRequest{
		Command: rag.ProposeCommand{
			ID: versionID, DocumentID: documentID, OrganizationID: runtime.OrganizationID,
			NamespaceKind: input.NamespaceKind, NamespaceID: input.NamespaceID, Version: 1,
			Title: input.Title, Body: bodyText, SourceKind: input.SourceKind, SourceReference: sourceObject,
			EvidenceRefs: []rag.EvidenceRef{{Reference: "objectstorage:" + sourceObject, Digest: sourceSHAHex}},
			ProposedBy:   input.ProposedBy,
			Admission: rag.AdmissionAttestation{
				DataClass: input.Admission.DataClass, AttestedBy: input.Admission.AttestedBy,
				SourceBoundary: input.Admission.SourceBoundary, EvidenceRef: input.Admission.EvidenceRef,
				SanitizationEvidenceRef: input.Admission.SanitizationEvidenceRef, AttestedAt: attestedAt,
			},
		},
		IdempotencyKey: input.IdempotencyKey,
	})
	if err != nil {
		return ragIngestPDFResult{}, err
	}

	manifestBody, err := json.Marshal(pdfChunkManifest{VersionID: versionID, Chunks: chunks})
	if err != nil {
		return ragIngestPDFResult{}, fmt.Errorf("encode chunk manifest: %w", err)
	}
	if err := osClient.PutObject(ctx, input.ManifestObject, manifestBody, "application/json"); err != nil {
		return ragIngestPDFResult{}, fmt.Errorf("upload chunk manifest: %w", err)
	}

	return ragIngestPDFResult{
		Version: version, Reused: reused, PageCount: len(pdfResult.Pages), EmptyTextPages: emptyCount,
		ParserName: pdfResult.ParserName, ParserVersion: pdfResult.ParserVersion,
		SourceSHA256: sourceSHAHex, SourceObject: sourceObject, ManifestObject: input.ManifestObject,
	}, nil
}

type ragQueryInput struct {
	ActorRoleID string            `json:"actor_role_id"`
	Scope       rag.NamespaceKind `json:"scope"`
	QueryText   string            `json:"query_text"`
	Limit       int               `json:"limit,omitempty"`
}

func runRAG(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printRAGUsage(stderr)
		return exitUsage
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "rag")
	if code != exitOK {
		return code
	}
	defer store.Close()
	status, err := runner.Status(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "migration status: %v\n", err)
		return exitInternal
	}
	if !status.Ready {
		fmt.Fprintf(stderr, "database schema has %d pending migrations\n", status.Pending)
		return exitDrift
	}
	runtime, err := ragbootstrap.Open(cfg, store)
	if err != nil {
		fmt.Fprintf(stderr, "create rag runtime: %v\n", err)
		return exitInternal
	}
	switch args[0] {
	case "ingest-pdf":
		var input ragIngestPDFInput
		jsonOutput, code := parseRAGFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		result, err := runRAGIngestPDF(ctx, runtime, input)
		if err != nil {
			return ragCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, result)
		return exitOK
	case "propose":
		var input ragProposeInput
		jsonOutput, code := parseRAGFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		attestedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.Admission.AttestedAt))
		if err != nil {
			fmt.Fprintf(stderr, "parse admission.attested_at: %v\n", err)
			return exitUsage
		}
		refs := make([]rag.EvidenceRef, 0, len(input.EvidenceRefs))
		for _, ref := range input.EvidenceRefs {
			refs = append(refs, rag.EvidenceRef{Reference: ref.Reference, Digest: ref.Digest})
		}
		version, reused, err := runtime.Manager.Propose(ctx, rag.ProposeRequest{
			Command: rag.ProposeCommand{
				ID: input.ID, DocumentID: input.DocumentID, OrganizationID: runtime.OrganizationID, NamespaceKind: input.NamespaceKind, NamespaceID: input.NamespaceID,
				Version: input.Version, Title: input.Title, Body: input.Body, SourceKind: input.SourceKind, SourceReference: input.SourceReference, SourceRunRef: input.SourceRunRef,
				EvidenceRefs: refs, ProposedBy: input.ProposedBy,
				Admission:           rag.AdmissionAttestation{DataClass: input.Admission.DataClass, AttestedBy: input.Admission.AttestedBy, SourceBoundary: input.Admission.SourceBoundary, EvidenceRef: input.Admission.EvidenceRef, SanitizationEvidenceRef: input.Admission.SanitizationEvidenceRef, AttestedAt: attestedAt},
				SupersedesVersionID: input.SupersedesVersionID,
			},
			IdempotencyKey: input.IdempotencyKey,
		})
		if err != nil {
			return ragCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, map[string]any{"version": version, "reused": reused})
		return exitOK
	case "review":
		var input ragReviewInput
		jsonOutput, code := parseRAGFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		mutation := rag.MutationRequest{OrganizationID: runtime.OrganizationID, VersionID: input.VersionID, ExpectedRevision: input.ExpectedRevision, ActorRoleID: input.ActorRoleID, Reason: input.Reason, ApprovalRequestID: input.ApprovalRequestID}
		var version rag.KnowledgeVersion
		switch input.Outcome {
		case "approve":
			version, err = runtime.Manager.Review(ctx, rag.ReviewRequest{Mutation: mutation, Outcome: rag.ReviewApprove})
		case "reject":
			version, err = runtime.Manager.Review(ctx, rag.ReviewRequest{Mutation: mutation, Outcome: rag.ReviewReject})
		case "deprecate":
			version, err = runtime.Manager.Deprecate(ctx, mutation)
		case "archive":
			version, err = runtime.Manager.Archive(ctx, mutation)
		default:
			fmt.Fprintf(stderr, "invalid outcome %q: want approve|reject|deprecate|archive\n", input.Outcome)
			return exitUsage
		}
		if err != nil {
			return ragCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, version)
		return exitOK
	case "get":
		flags := flag.NewFlagSet("rag get", flag.ContinueOnError)
		flags.SetOutput(stderr)
		versionID := flags.String("id", "", "knowledge version id")
		actorRoleID := flags.String("actor", "", "actor role id")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || strings.TrimSpace(*versionID) == "" || strings.TrimSpace(*actorRoleID) == "" {
			return exitUsage
		}
		version, err := runtime.Manager.Get(ctx, runtime.OrganizationID, *versionID, *actorRoleID)
		if err != nil {
			return ragCommandError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, version)
		return exitOK
	case "list":
		flags := flag.NewFlagSet("rag list", flag.ContinueOnError)
		flags.SetOutput(stderr)
		namespaceKind := flags.String("namespace-kind", "", "department|own")
		namespaceID := flags.String("namespace-id", "", "namespace id filter")
		actorRoleID := flags.String("actor", "", "actor role id")
		lifecycle := flags.String("lifecycle", "", "candidate|approved|rejected|deprecated|archived")
		limit := flags.Int("limit", 100, "maximum versions")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || strings.TrimSpace(*actorRoleID) == "" || strings.TrimSpace(*namespaceKind) == "" || strings.TrimSpace(*namespaceID) == "" {
			return exitUsage
		}
		versions, err := runtime.Manager.List(ctx, *actorRoleID, rag.ListFilter{OrganizationID: runtime.OrganizationID, NamespaceKind: rag.NamespaceKind(*namespaceKind), NamespaceID: *namespaceID, Lifecycle: rag.Lifecycle(*lifecycle), Limit: *limit})
		if err != nil {
			return ragCommandError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, versions)
		return exitOK
	case "reindex":
		var input ragReindexInput
		jsonOutput, code := parseRAGFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		var precomputed map[string][]rag.Chunk
		if strings.TrimSpace(input.PrecomputedChunksManifestRef) != "" {
			osClient, code := openObjectStorageClient(stderr)
			if code != exitOK {
				return code
			}
			body, err := osClient.GetObject(ctx, input.PrecomputedChunksManifestRef)
			if err != nil {
				fmt.Fprintf(stderr, "fetch precomputed chunks manifest: %v\n", err)
				return exitInternal
			}
			var manifest pdfChunkManifest
			if err := json.Unmarshal(body, &manifest); err != nil {
				fmt.Fprintf(stderr, "decode precomputed chunks manifest: %v\n", err)
				return exitInvalid
			}
			precomputed = map[string][]rag.Chunk{manifest.VersionID: manifest.Chunks}
		}
		generation, err := runtime.Manager.Reindex(ctx, rag.ReindexRequest{
			OrganizationID: runtime.OrganizationID, NamespaceKind: input.NamespaceKind, NamespaceID: input.NamespaceID,
			ActorRoleID: input.ActorRoleID, ApprovalRequestID: input.ApprovalRequestID, PrecomputedChunks: precomputed,
		})
		if err != nil {
			return ragCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, generation)
		return exitOK
	case "backfill-embeddings":
		flags := flag.NewFlagSet("rag backfill-embeddings", flag.ContinueOnError)
		flags.SetOutput(stderr)
		namespaceKind := flags.String("namespace-kind", "", "department|own")
		namespaceID := flags.String("namespace-id", "", "namespace id")
		actorRoleID := flags.String("actor", "", "actor role id")
		batchSize := flags.Int("batch-size", 0, "chunks embedded per call (default 50, max 500)")
		maxBatches := flags.Int("max-batches", 1, "how many batches to run in this invocation before stopping (each batch is one authorized, ledger-attributed call)")
		approvalRequestID := flags.Int64("approval-request-id", 0, "decided orgctl authorization request ID (rag.publish_approved is always approval-required)")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || strings.TrimSpace(*namespaceKind) == "" || strings.TrimSpace(*namespaceID) == "" || strings.TrimSpace(*actorRoleID) == "" || *maxBatches <= 0 {
			return exitUsage
		}
		var approvalID *int64
		if *approvalRequestID > 0 {
			approvalID = approvalRequestID
		}
		totals := rag.BackfillEmbeddingsResult{}
		for batch := 0; batch < *maxBatches; batch++ {
			result, err := runtime.Manager.BackfillEmbeddings(ctx, rag.BackfillEmbeddingsRequest{
				OrganizationID: runtime.OrganizationID, NamespaceKind: rag.NamespaceKind(*namespaceKind), NamespaceID: *namespaceID,
				ActorRoleID: *actorRoleID, BatchSize: *batchSize, ApprovalRequestID: approvalID,
			})
			if err != nil {
				return ragCommandError(stderr, err)
			}
			totals.Embedded += result.Embedded
			totals.Skipped += result.Skipped
			totals.Done = result.Done
			if result.Done {
				break
			}
		}
		writeValue(stdout, *jsonOutput, totals)
		return exitOK
	case "query":
		var input ragQueryInput
		jsonOutput, code := parseRAGFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		results, err := runtime.Manager.Query(ctx, rag.QueryRequest{OrganizationID: runtime.OrganizationID, ActorRoleID: input.ActorRoleID, Scope: input.Scope, QueryText: input.QueryText, Limit: input.Limit})
		if err != nil {
			return ragCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, results)
		return exitOK
	default:
		printRAGUsage(stderr)
		return exitUsage
	}
}

func parseRAGFile(args []string, stderr io.Writer, target any) (bool, int) {
	flags := flag.NewFlagSet("rag file command", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("file", "", "JSON input file, or - for stdin")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || strings.TrimSpace(*path) == "" {
		return false, exitUsage
	}
	var raw []byte
	var err error
	if *path == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(*path)
	}
	if err != nil {
		fmt.Fprintf(stderr, "read rag input: %v\n", err)
		return false, exitUsage
	}
	if err := decodeRAGStrict(raw, target); err != nil {
		fmt.Fprintf(stderr, "decode rag input: %v\n", err)
		return false, exitUsage
	}
	return *jsonOutput, exitOK
}
func decodeRAGStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple top-level JSON values are not allowed")
		}
		return err
	}
	return nil
}
func ragCommandError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "rag operation failed: %v\n", err)
	switch {
	case errors.Is(err, authorization.ErrApprovalRequired):
		return exitApprovalRequired
	case errors.Is(err, authorization.ErrCapabilityDenied):
		return exitDenied
	case errors.Is(err, rag.ErrRevisionConflict):
		return exitDrift
	case errors.Is(err, rag.ErrInvalidDocument), errors.Is(err, rag.ErrInvalidVersion), errors.Is(err, rag.ErrInvalidTransition), errors.Is(err, rag.ErrInvalidAdmission), errors.Is(err, rag.ErrForbiddenDataClass), errors.Is(err, rag.ErrInvalidReview), errors.Is(err, rag.ErrNotFound), errors.Is(err, rag.ErrConflict), errors.Is(err, rag.ErrInvalidRequest), errors.Is(err, rag.ErrInvalidNamespace), errors.Is(err, rag.ErrVersionNotApproved), errors.Is(err, rag.ErrInvalidChunk), errors.Is(err, rag.ErrInvalidGeneration), errors.Is(err, rag.ErrSourceDrift), errors.Is(err, rag.ErrIdempotencyConflict):
		return exitInvalid
	default:
		return exitInternal
	}
}
func printRAGUsage(out io.Writer) {
	fmt.Fprintln(out, `usage: orgctl rag <propose|review|get|list|reindex|backfill-embeddings|query|ingest-pdf> [options]

  get --id VERSION_ID --actor ROLE_ID [--json]
  list --namespace-kind department|own --namespace-id ID --actor ROLE_ID [--lifecycle LIFECYCLE] [--limit N] [--json]
  backfill-embeddings --namespace-kind department|own --namespace-id ID --actor ROLE_ID [--batch-size N] [--max-batches N] [--json]
  ingest-pdf --file INPUT.json [--json]
      Splits a local PDF into per-page PDFs via poppler, uploads each page
      plus the source PDF to Object Storage, and proposes one document-
      level knowledge candidate. Fail-closed on malformed/encrypted/
      unsupported PDFs (no candidate proposed). Writes a chunk manifest to
      Object Storage for a later `+"`reindex --file`"+` (with
      precomputed_chunks_manifest_ref set) to consume once the document is
      reviewed and approved.`)
}
