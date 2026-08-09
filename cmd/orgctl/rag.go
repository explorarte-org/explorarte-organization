package main

import (
	"bytes"
	"context"
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
		generation, err := runtime.Manager.Reindex(ctx, rag.ReindexRequest{OrganizationID: runtime.OrganizationID, NamespaceKind: input.NamespaceKind, NamespaceID: input.NamespaceID, ActorRoleID: input.ActorRoleID, ApprovalRequestID: input.ApprovalRequestID})
		if err != nil {
			return ragCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, generation)
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
	fmt.Fprintln(out, `usage: orgctl rag <propose|review|get|list|reindex|query> [options]

  get --id VERSION_ID --actor ROLE_ID [--json]
  list --namespace-kind department|own --namespace-id ID --actor ROLE_ID [--lifecycle LIFECYCLE] [--limit N] [--json]`)
}
