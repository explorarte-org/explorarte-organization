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
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
	skillregistrybootstrap "github.com/Mireuz13/explorarte-organization/internal/skillregistry/bootstrap"
)

type skillManifestInput struct {
	Name                 string   `json:"name"`
	Description          string   `json:"description"`
	Department           string   `json:"department"`
	OwnerRoleID          string   `json:"owner_role_id"`
	MemoryDomain         string   `json:"memory_domain"`
	BaseProtocol         string   `json:"base_protocol"`
	VerifierRef          string   `json:"verifier_ref,omitempty"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
}
type skillSourceInput struct {
	Path           string `json:"path"`
	SHA256         string `json:"sha256"`
	Origin         string `json:"origin"`
	OriginRef      string `json:"origin_ref,omitempty"`
	LegacyImported bool   `json:"legacy_imported,omitempty"`
	RecordedBy     string `json:"recorded_by"`
	RecordRef      string `json:"record_ref"`
}
type skillProposeInput struct {
	SkillID           string             `json:"skill_id"`
	VersionID         string             `json:"version_id"`
	Version           int64              `json:"version"`
	CreatedByRole     string             `json:"created_by_role"`
	Manifest          skillManifestInput `json:"manifest"`
	Source            skillSourceInput   `json:"source"`
	ContentHash       string             `json:"content_hash"`
	SupersedesVersion string             `json:"supersedes_version,omitempty"`
	IdempotencyKey    string             `json:"idempotency_key"`
}
type skillApprovalInput struct {
	VersionID        string `json:"version_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	ActorRoleID      string `json:"actor_role_id"`
	DecisionRef      string `json:"decision_ref"`
	ApprovedBy       string `json:"approved_by"`
	ApprovedAt       string `json:"approved_at"`
}
type skillQualifyInput struct {
	VersionID             string `json:"version_id"`
	ExpectedRevision      int64  `json:"expected_revision"`
	ActorRoleID           string `json:"actor_role_id"`
	SchemaValidationRef   string `json:"schema_validation_ref"`
	CapabilityReviewRef   string `json:"capability_review_ref"`
	InstructionSafetyRef  string `json:"instruction_safety_ref"`
	SourceRecordRef       string `json:"source_record_ref"`
	ValidatedBy           string `json:"validated_by"`
	ValidatedAt           string `json:"validated_at"`
	CapabilitiesPass      bool   `json:"capabilities_pass"`
	InstructionSafetyPass bool   `json:"instruction_safety_pass"`
}
type skillLifecycleInput struct {
	VersionID        string `json:"version_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	ActorRoleID      string `json:"actor_role_id"`
}
type skillAssignInput struct {
	VersionID             string `json:"version_id"`
	AssignmentID          string `json:"assignment_id"`
	RoleID                string `json:"role_id"`
	AssignedBy            string `json:"assigned_by"`
	AssignmentDecisionRef string `json:"assignment_decision_ref"`
	CapabilityReviewRef   string `json:"capability_review_ref"`
	IdempotencyKey        string `json:"idempotency_key"`
}
type skillRevokeInput struct {
	AssignmentID     string `json:"assignment_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	ActorRoleID      string `json:"actor_role_id"`
	Reason           string `json:"reason"`
}

func runSkill(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printSkillUsage(stderr)
		return exitUsage
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "skill")
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
	runtime, err := skillregistrybootstrap.Open(cfg, store)
	if err != nil {
		fmt.Fprintf(stderr, "create skill registry runtime: %v\n", err)
		return exitInternal
	}
	switch args[0] {
	case "propose":
		var input skillProposeInput
		jsonOutput, code := parseSkillFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		skill, version, reused, err := runtime.Manager.Propose(ctx, skillregistry.ProposeRequest{
			Command: skillregistry.CreateDraftCommand{
				SkillID: input.SkillID, VersionID: input.VersionID, OrganizationID: runtime.OrganizationID, Version: input.Version, CreatedByRole: input.CreatedByRole,
				Manifest:    skillregistry.Manifest{Name: input.Manifest.Name, Description: input.Manifest.Description, Department: input.Manifest.Department, OwnerRoleID: input.Manifest.OwnerRoleID, MemoryDomain: input.Manifest.MemoryDomain, BaseProtocol: input.Manifest.BaseProtocol, VerifierRef: input.Manifest.VerifierRef, RequiredCapabilities: input.Manifest.RequiredCapabilities},
				Source:      skillregistry.SourceRecord{Path: input.Source.Path, SHA256: input.Source.SHA256, Origin: skillregistry.OriginKind(input.Source.Origin), OriginRef: input.Source.OriginRef, LegacyImported: input.Source.LegacyImported, RecordedBy: input.Source.RecordedBy, RecordRef: input.Source.RecordRef},
				ContentHash: input.ContentHash, SupersedesVersion: input.SupersedesVersion,
			},
			IdempotencyKey: input.IdempotencyKey,
		})
		if err != nil {
			return skillCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, map[string]any{"skill": skill, "version": version, "reused": reused})
		return exitOK
	case "approve":
		var input skillApprovalInput
		jsonOutput, code := parseSkillFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		approvedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.ApprovedAt))
		if err != nil {
			fmt.Fprintf(stderr, "parse approved_at: %v\n", err)
			return exitUsage
		}
		version, err := runtime.Manager.HumanApprove(ctx, skillLifecycleMutation(runtime.OrganizationID, input.VersionID, input.ExpectedRevision, input.ActorRoleID), skillregistry.ApprovalEvidence{DecisionRef: input.DecisionRef, ApprovedBy: input.ApprovedBy, ApprovedAt: approvedAt})
		if err != nil {
			return skillCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, version)
		return exitOK
	case "qualify":
		var input skillQualifyInput
		jsonOutput, code := parseSkillFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		validatedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.ValidatedAt))
		if err != nil {
			fmt.Fprintf(stderr, "parse validated_at: %v\n", err)
			return exitUsage
		}
		version, err := runtime.Manager.QualifyCandidate(ctx, skillLifecycleMutation(runtime.OrganizationID, input.VersionID, input.ExpectedRevision, input.ActorRoleID), skillregistry.ValidationEvidence{
			SchemaValidationRef: input.SchemaValidationRef, CapabilityReviewRef: input.CapabilityReviewRef, InstructionSafetyRef: input.InstructionSafetyRef, SourceRecordRef: input.SourceRecordRef,
			ValidatedBy: input.ValidatedBy, ValidatedAt: validatedAt, CapabilitiesPass: input.CapabilitiesPass, InstructionSafetyPass: input.InstructionSafetyPass,
		})
		if err != nil {
			return skillCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, version)
		return exitOK
	case "activate":
		var input skillApprovalInput
		jsonOutput, code := parseSkillFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		approvedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.ApprovedAt))
		if err != nil {
			fmt.Fprintf(stderr, "parse approved_at: %v\n", err)
			return exitUsage
		}
		version, err := runtime.Manager.Activate(ctx, skillLifecycleMutation(runtime.OrganizationID, input.VersionID, input.ExpectedRevision, input.ActorRoleID), skillregistry.ApprovalEvidence{DecisionRef: input.DecisionRef, ApprovedBy: input.ApprovedBy, ApprovedAt: approvedAt})
		if err != nil {
			return skillCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, version)
		return exitOK
	case "suspend", "retire":
		var input skillLifecycleInput
		jsonOutput, code := parseSkillFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		mutation := skillLifecycleMutation(runtime.OrganizationID, input.VersionID, input.ExpectedRevision, input.ActorRoleID)
		var version skillregistry.SkillVersion
		if args[0] == "suspend" {
			version, err = runtime.Manager.Suspend(ctx, mutation)
		} else {
			version, err = runtime.Manager.Retire(ctx, mutation)
		}
		if err != nil {
			return skillCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, version)
		return exitOK
	case "assign":
		var input skillAssignInput
		jsonOutput, code := parseSkillFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		assignment, reused, err := runtime.Manager.Assign(ctx, skillregistry.AssignRequest{
			VersionID: input.VersionID,
			Command: skillregistry.AssignCommand{
				AssignmentID: input.AssignmentID, OrganizationID: runtime.OrganizationID, RoleID: input.RoleID,
				AssignedBy: input.AssignedBy, AssignmentDecisionRef: input.AssignmentDecisionRef, CapabilityReviewRef: input.CapabilityReviewRef,
			},
			IdempotencyKey: input.IdempotencyKey,
		})
		if err != nil {
			return skillCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, map[string]any{"assignment": assignment, "reused": reused})
		return exitOK
	case "revoke":
		var input skillRevokeInput
		jsonOutput, code := parseSkillFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		assignment, err := runtime.Manager.RevokeAssignment(ctx, skillregistry.RevokeRequest{OrganizationID: runtime.OrganizationID, AssignmentID: input.AssignmentID, ExpectedRevision: input.ExpectedRevision, ActorRoleID: input.ActorRoleID, Reason: input.Reason})
		if err != nil {
			return skillCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, assignment)
		return exitOK
	case "get-version":
		flags := flag.NewFlagSet("skill get-version", flag.ContinueOnError)
		flags.SetOutput(stderr)
		versionID := flags.String("id", "", "skill version id")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || strings.TrimSpace(*versionID) == "" {
			return exitUsage
		}
		version, err := runtime.Manager.GetVersion(ctx, runtime.OrganizationID, *versionID)
		if err != nil {
			return skillCommandError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, version)
		return exitOK
	case "list-versions":
		flags := flag.NewFlagSet("skill list-versions", flag.ContinueOnError)
		flags.SetOutput(stderr)
		skillID := flags.String("skill-id", "", "skill id")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || strings.TrimSpace(*skillID) == "" {
			return exitUsage
		}
		versions, err := runtime.Manager.ListVersions(ctx, runtime.OrganizationID, *skillID)
		if err != nil {
			return skillCommandError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, versions)
		return exitOK
	case "get-assignment":
		flags := flag.NewFlagSet("skill get-assignment", flag.ContinueOnError)
		flags.SetOutput(stderr)
		assignmentID := flags.String("id", "", "assignment id")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || strings.TrimSpace(*assignmentID) == "" {
			return exitUsage
		}
		assignment, err := runtime.Manager.GetAssignment(ctx, runtime.OrganizationID, *assignmentID)
		if err != nil {
			return skillCommandError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, assignment)
		return exitOK
	case "list-assignments":
		flags := flag.NewFlagSet("skill list-assignments", flag.ContinueOnError)
		flags.SetOutput(stderr)
		role := flags.String("role", "", "role id")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || strings.TrimSpace(*role) == "" {
			return exitUsage
		}
		assignments, err := runtime.Manager.ListActiveAssignmentsForRole(ctx, runtime.OrganizationID, *role)
		if err != nil {
			return skillCommandError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, assignments)
		return exitOK
	default:
		printSkillUsage(stderr)
		return exitUsage
	}
}

func skillLifecycleMutation(organizationID, versionID string, expectedRevision int64, actorRoleID string) skillregistry.LifecycleMutationRequest {
	return skillregistry.LifecycleMutationRequest{OrganizationID: organizationID, VersionID: versionID, ExpectedRevision: expectedRevision, ActorRoleID: actorRoleID}
}

func parseSkillFile(args []string, stderr io.Writer, target any) (bool, int) {
	flags := flag.NewFlagSet("skill file command", flag.ContinueOnError)
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
		fmt.Fprintf(stderr, "read skill input: %v\n", err)
		return false, exitUsage
	}
	if err := decodeSkillStrict(raw, target); err != nil {
		fmt.Fprintf(stderr, "decode skill input: %v\n", err)
		return false, exitUsage
	}
	return *jsonOutput, exitOK
}
func decodeSkillStrict(raw []byte, target any) error {
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
func skillCommandError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "skill registry operation failed: %v\n", err)
	switch {
	case errors.Is(err, authorization.ErrApprovalRequired):
		return exitApprovalRequired
	case errors.Is(err, authorization.ErrCapabilityDenied):
		return exitDenied
	case errors.Is(err, skillregistry.ErrRevisionConflict):
		return exitDrift
	case errors.Is(err, skillregistry.ErrInvalidSkill), errors.Is(err, skillregistry.ErrInvalidVersion), errors.Is(err, skillregistry.ErrInvalidAssignment), errors.Is(err, skillregistry.ErrInvalidTransition), errors.Is(err, skillregistry.ErrMissingActivationProof), errors.Is(err, skillregistry.ErrSchemaValidationFailed), errors.Is(err, skillregistry.ErrCapabilityReviewFailed), errors.Is(err, skillregistry.ErrInstructionSafetyFailed), errors.Is(err, skillregistry.ErrGovernanceEvidence), errors.Is(err, skillregistry.ErrVersionNotActive), errors.Is(err, skillregistry.ErrAssignmentNotActive), errors.Is(err, skillregistry.ErrAssignmentConflict), errors.Is(err, skillregistry.ErrNotFound), errors.Is(err, skillregistry.ErrSourceDrift), errors.Is(err, skillregistry.ErrIdempotencyConflict):
		return exitInvalid
	default:
		return exitInternal
	}
}
func printSkillUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: orgctl skill <propose|approve|qualify|activate|suspend|retire|assign|revoke|get-version|list-versions|get-assignment|list-assignments> [options]")
}
