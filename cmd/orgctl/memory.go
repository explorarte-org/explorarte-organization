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
	"github.com/Mireuz13/explorarte-organization/internal/memory"
	memorybootstrap "github.com/Mireuz13/explorarte-organization/internal/memory/bootstrap"
)

type memoryEvidenceInput struct { Reference string `json:"reference"`; Digest string `json:"digest"` }
type memoryAdmissionInput struct { DataClass memory.DataClass `json:"data_class"`; AttestedBy string `json:"attested_by"`; SourceBoundary string `json:"source_boundary"`; EvidenceRef string `json:"evidence_ref"`; SanitizationEvidenceRef string `json:"sanitization_evidence_ref,omitempty"`; AttestedAt string `json:"attested_at"` }
type memoryProposeInput struct {
	ID string `json:"id"`; RoleID string `json:"role_id"`; Category string `json:"category"`; Problem string `json:"problem"`; Correction string `json:"correction"`
	SourceKind memory.SourceKind `json:"source_kind"`; SourceRunID int64 `json:"source_run_id"`; EvidenceRefs []memoryEvidenceInput `json:"evidence_refs"`; ProposedBy string `json:"proposed_by"`; Admission memoryAdmissionInput `json:"admission"`; SupersedesEntryID string `json:"supersedes_entry_id,omitempty"`; IdempotencyKey string `json:"idempotency_key"`
}
type memoryReviewInput struct { EntryID string `json:"entry_id"`; ExpectedRevision int64 `json:"expected_revision"`; ActorRoleID string `json:"actor_role_id"`; Reason string `json:"reason"`; Outcome memory.ReviewOutcome `json:"outcome"` }
type memoryMutationInput struct { EntryID string `json:"entry_id"`; ExpectedRevision int64 `json:"expected_revision"`; ActorRoleID string `json:"actor_role_id"`; Reason string `json:"reason"` }

func runMemory(args []string, stdout, stderr io.Writer) int {
	if len(args)==0 { printMemoryUsage(stderr); return exitUsage }
	cfg,err:=config.Load(); if err!=nil { fmt.Fprintf(stderr,"load configuration: %v\n",err); return exitUsage }
	ctx,cancel:=context.WithTimeout(context.Background(),cfg.Tasks.CommandTimeout); defer cancel()
	store,runner,code:=openDatabase(ctx,cfg,stderr,"memory"); if code!=exitOK{return code}; defer store.Close()
	status,err:=runner.Status(ctx); if err!=nil {fmt.Fprintf(stderr,"migration status: %v\n",err);return exitInternal}; if !status.Ready {fmt.Fprintf(stderr,"database schema has %d pending migrations\n",status.Pending);return exitDrift}
	runtime,err:=memorybootstrap.Open(cfg,store); if err!=nil {fmt.Fprintf(stderr,"create memory runtime: %v\n",err);return exitInternal}
	switch args[0] {
	case "propose":
		var input memoryProposeInput; jsonOutput,code:=parseMemoryFile(args[1:],stderr,&input); if code!=exitOK{return code}
		attestedAt,err:=time.Parse(time.RFC3339Nano,strings.TrimSpace(input.Admission.AttestedAt)); if err!=nil {fmt.Fprintf(stderr,"parse admission.attested_at: %v\n",err);return exitUsage}
		refs:=make([]memory.EvidenceRef,0,len(input.EvidenceRefs)); for _,ref:=range input.EvidenceRefs {refs=append(refs,memory.EvidenceRef{Reference:ref.Reference,Digest:ref.Digest})}
		entry,reused,err:=runtime.Manager.Propose(ctx,memory.ProposeRequest{Command:memory.ProposeCommand{ID:input.ID,OrganizationID:runtime.OrganizationID,RoleID:input.RoleID,Category:input.Category,Problem:input.Problem,Correction:input.Correction,SourceKind:input.SourceKind,SourceRunID:input.SourceRunID,EvidenceRefs:refs,ProposedBy:input.ProposedBy,Admission:memory.AdmissionAttestation{DataClass:input.Admission.DataClass,AttestedBy:input.Admission.AttestedBy,SourceBoundary:input.Admission.SourceBoundary,EvidenceRef:input.Admission.EvidenceRef,SanitizationEvidenceRef:input.Admission.SanitizationEvidenceRef,AttestedAt:attestedAt},SupersedesEntryID:input.SupersedesEntryID},IdempotencyKey:input.IdempotencyKey}); if err!=nil{return memoryCommandError(stderr,err)}
		writeValue(stdout,jsonOutput,map[string]any{"entry":entry,"reused":reused}); return exitOK
	case "review":
		var input memoryReviewInput; jsonOutput,code:=parseMemoryFile(args[1:],stderr,&input); if code!=exitOK{return code}; entry,err:=runtime.Manager.Review(ctx,memory.ReviewRequest{Mutation:memory.MutationRequest{OrganizationID:runtime.OrganizationID,EntryID:input.EntryID,ExpectedRevision:input.ExpectedRevision,ActorRoleID:input.ActorRoleID,Reason:input.Reason},Outcome:input.Outcome}); if err!=nil{return memoryCommandError(stderr,err)}; writeValue(stdout,jsonOutput,entry);return exitOK
	case "deprecate","archive":
		var input memoryMutationInput; jsonOutput,code:=parseMemoryFile(args[1:],stderr,&input); if code!=exitOK{return code}; request:=memory.MutationRequest{OrganizationID:runtime.OrganizationID,EntryID:input.EntryID,ExpectedRevision:input.ExpectedRevision,ActorRoleID:input.ActorRoleID,Reason:input.Reason}; var entry memory.Entry; if args[0]=="deprecate"{entry,err=runtime.Manager.Deprecate(ctx,request)}else{entry,err=runtime.Manager.Archive(ctx,request)}; if err!=nil{return memoryCommandError(stderr,err)}; writeValue(stdout,jsonOutput,entry);return exitOK
	case "get":
		flags:=flag.NewFlagSet("memory get",flag.ContinueOnError);flags.SetOutput(stderr);entryID:=flags.String("id","","memory entry id");jsonOutput:=flags.Bool("json",false,"emit JSON");if err:=flags.Parse(args[1:]);err!=nil||flags.NArg()!=0||strings.TrimSpace(*entryID)==""{return exitUsage};entry,err:=runtime.Manager.Get(ctx,runtime.OrganizationID,*entryID);if err!=nil{return memoryCommandError(stderr,err)};writeValue(stdout,*jsonOutput,entry);return exitOK
	case "list":
		flags:=flag.NewFlagSet("memory list",flag.ContinueOnError);flags.SetOutput(stderr);role:=flags.String("role","","role id filter");statusValue:=flags.String("status","","candidate|approved|deprecated|archived|rejected");limit:=flags.Int("limit",100,"maximum entries");jsonOutput:=flags.Bool("json",false,"emit JSON");if err:=flags.Parse(args[1:]);err!=nil||flags.NArg()!=0{return exitUsage};entries,err:=runtime.Manager.List(ctx,memory.ListFilter{OrganizationID:runtime.OrganizationID,RoleID:*role,Status:memory.Status(*statusValue),Limit:*limit});if err!=nil{return memoryCommandError(stderr,err)};writeValue(stdout,*jsonOutput,entries);return exitOK
	default: printMemoryUsage(stderr);return exitUsage
	}
}

func parseMemoryFile(args []string,stderr io.Writer,target any)(bool,int){flags:=flag.NewFlagSet("memory file command",flag.ContinueOnError);flags.SetOutput(stderr);path:=flags.String("file","","JSON input file, or - for stdin");jsonOutput:=flags.Bool("json",false,"emit JSON");if err:=flags.Parse(args);err!=nil||flags.NArg()!=0||strings.TrimSpace(*path)==""{return false,exitUsage};var raw []byte;var err error;if *path=="-"{raw,err=io.ReadAll(os.Stdin)}else{raw,err=os.ReadFile(*path)};if err!=nil{fmt.Fprintf(stderr,"read memory input: %v\n",err);return false,exitUsage};if err:=decodeMemoryStrict(raw,target);err!=nil{fmt.Fprintf(stderr,"decode memory input: %v\n",err);return false,exitUsage};return *jsonOutput,exitOK}
func decodeMemoryStrict(raw []byte,target any)error{decoder:=json.NewDecoder(bytes.NewReader(raw));decoder.DisallowUnknownFields();if err:=decoder.Decode(target);err!=nil{return err};var extra any;if err:=decoder.Decode(&extra);!errors.Is(err,io.EOF){if err==nil{return errors.New("multiple top-level JSON values are not allowed")};return err};return nil}
func memoryCommandError(stderr io.Writer,err error)int{fmt.Fprintf(stderr,"memory operation failed: %v\n",err);switch{case errors.Is(err,authorization.ErrApprovalRequired):return exitApprovalRequired;case errors.Is(err,authorization.ErrCapabilityDenied):return exitDenied;case errors.Is(err,memory.ErrRevisionConflict):return exitDrift;case errors.Is(err,memory.ErrInvalidRequest),errors.Is(err,memory.ErrInvalidEntry),errors.Is(err,memory.ErrInvalidEvidenceRef),errors.Is(err,memory.ErrInvalidAdmission),errors.Is(err,memory.ErrForbiddenDataClass),errors.Is(err,memory.ErrInvalidTransition),errors.Is(err,memory.ErrInvalidReview),errors.Is(err,memory.ErrEntryNotFound),errors.Is(err,memory.ErrDuplicateCandidate),errors.Is(err,memory.ErrConflict):return exitInvalid;default:return exitInternal}}
func printMemoryUsage(out io.Writer){fmt.Fprintln(out,"usage: orgctl memory <propose|review|deprecate|archive|get|list> [options]")}
