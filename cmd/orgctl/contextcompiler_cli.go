package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// runContextCompiler implements `orgctl contextcompiler
// <shadow-compile|provider-render-shadow> <snapshot_id>`: READ-ONLY. It
// fetches the real, already-persisted canonical contextengine.Snapshot
// (never creates a new one, never dispatches anything to a provider). This
// is shadow/dry-run mode only, per the owner's explicit phased plan: no
// provider call may happen until a human reviews this output.
func runContextCompiler(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: orgctl contextcompiler <shadow-compile|provider-render-shadow> SNAPSHOT_ID [--json]")
		return exitUsage
	}
	if args[0] == "provider-render-shadow" {
		return runProviderRenderShadow(args[1:], stdout, stderr)
	}
	if args[0] != "shadow-compile" {
		fmt.Fprintln(stderr, "usage: orgctl contextcompiler <shadow-compile|provider-render-shadow> SNAPSHOT_ID [--json]")
		return exitUsage
	}
	flags := flag.NewFlagSet("contextcompiler shadow-compile", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args[1:]) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "context snapshot")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}

	_, runtime, cleanup, code := openContextRuntime(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // read-only single-snapshot fetch
	defer cancel()

	snapshot, err := runtime.Service.Get(ctx, id, true)
	if err != nil {
		return contextError(stderr, err)
	}

	result, err := contextcompiler.CompileForTaskClass(snapshot)
	if err != nil {
		fmt.Fprintf(stderr, "compile: %v\n", err)
		return exitInternal
	}

	originalBytes := 0
	for _, seg := range snapshot.Segments {
		if seg.Included {
			originalBytes += seg.ByteCount
		}
	}
	projectedBytes := result.StablePrefixBytes + result.DynamicSuffixBytes

	report := shadowCompileReport{
		ContextSnapshotID:     result.ContextSnapshotID,
		ActorRoleID:           snapshot.ActorRoleID,
		ContextProfileID:      result.ContextProfileID,
		ContextProfileVersion: result.ContextProfileVersion,
		FellBackToCanonical:   result.FellBackToCanonical,
		OriginalTotalBytes:    originalBytes,
		ProjectedTotalBytes:   projectedBytes,
		StablePrefixBytes:     result.StablePrefixBytes,
		DynamicSuffixBytes:    result.DynamicSuffixBytes,
		AuthorityOrderHash:    result.AuthorityOrderHash,
		CompiledContentHash:   result.CompiledContentHash,
	}
	for _, d := range result.SegmentDiffs {
		report.Segments = append(report.Segments, shadowSegmentReport{
			SourceReference: d.SourceReference,
			AuthorityTier:   string(d.AuthorityTier),
			OriginalBytes:   d.OriginalBytes,
			ProjectedBytes:  d.ProjectedBytes,
			Projected:       d.Projected,
			Reason:          d.Reason,
		})
	}

	if *jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(stderr, "encode: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	fmt.Fprintf(stdout, "snapshot=%d actor=%s profile=%s/%s fell_back=%v\n", report.ContextSnapshotID, report.ActorRoleID, report.ContextProfileID, report.ContextProfileVersion, report.FellBackToCanonical)
	fmt.Fprintf(stdout, "original_bytes=%d projected_bytes=%d reduction=%.1f%%\n", originalBytes, projectedBytes, reductionPct(originalBytes, projectedBytes))
	for _, s := range report.Segments {
		marker := " "
		if s.Projected {
			marker = "*"
		}
		fmt.Fprintf(stdout, "%s %-9s %-55s %7d -> %7d  %s\n", marker, s.AuthorityTier, s.SourceReference, s.OriginalBytes, s.ProjectedBytes, s.Reason)
	}
	return exitOK
}

func reductionPct(original, projected int) float64 {
	if original == 0 {
		return 0
	}
	return (1 - float64(projected)/float64(original)) * 100
}

type shadowCompileReport struct {
	ContextSnapshotID     int64                 `json:"context_snapshot_id"`
	ActorRoleID           string                `json:"actor_role_id"`
	ContextProfileID      string                `json:"context_profile_id"`
	ContextProfileVersion string                `json:"context_profile_version"`
	FellBackToCanonical   bool                  `json:"fell_back_to_canonical"`
	OriginalTotalBytes    int                   `json:"original_total_bytes"`
	ProjectedTotalBytes   int                   `json:"projected_total_bytes"`
	StablePrefixBytes     int                   `json:"stable_prefix_bytes"`
	DynamicSuffixBytes    int                   `json:"dynamic_suffix_bytes"`
	AuthorityOrderHash    string                `json:"authority_order_hash"`
	CompiledContentHash   string                `json:"compiled_content_hash"`
	Segments              []shadowSegmentReport `json:"segments"`
}

type shadowSegmentReport struct {
	SourceReference string `json:"source_reference"`
	AuthorityTier   string `json:"authority_tier"`
	OriginalBytes   int    `json:"original_bytes"`
	ProjectedBytes  int    `json:"projected_bytes"`
	Projected       bool   `json:"projected"`
	Reason          string `json:"reason,omitempty"`
}

// runProviderRenderShadow implements `orgctl contextcompiler
// provider-render-shadow <snapshot_id>`: R10.4 Fase 2 shadow determinism.
// READ-ONLY, no provider call. Fetches the real persisted snapshot, runs
// the SAME contextcompiler.CompileForTaskClass projection R10 already
// uses, and -- only if the compiler did not fall back to canonical (i.e.
// only for research.corpus_curate/v1) -- calls
// contextengine.BuildProviderRender directly, printing its hashes/bytes.
// This exercises the exact same code path resolveRender (in
// internal/modelruntime/bootstrap/runtime.go) uses for real dispatch,
// without needing a running DispatchService.
func runProviderRenderShadow(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("contextcompiler provider-render-shadow", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if parseInterspersed(flags, args) != nil || flags.NArg() != 1 {
		return exitUsage
	}
	id, err := positiveID(flags.Arg(0), "context snapshot")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}

	_, runtime, cleanup, code := openContextRuntime(stderr)
	if code != exitOK {
		return code
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snapshot, err := runtime.Service.Get(ctx, id, true)
	if err != nil {
		return contextError(stderr, err)
	}

	result, err := contextcompiler.CompileForTaskClass(snapshot)
	if err != nil {
		fmt.Fprintf(stderr, "compile: %v\n", err)
		return exitInternal
	}

	report := providerRenderShadowReport{
		ContextSnapshotID:   snapshot.ID,
		ActorRoleID:         snapshot.ActorRoleID,
		FellBackToCanonical: result.FellBackToCanonical,
	}
	if !result.FellBackToCanonical {
		render, buildErr := contextengine.BuildProviderRender(result.Projected)
		if buildErr != nil {
			report.BuildError = buildErr.Error()
		} else {
			report.Version = render.Version
			report.StablePrefixHash = render.StablePrefixHash
			report.StablePrefixBytes = render.StablePrefixBytes
			report.DynamicSuffixHash = render.DynamicSuffixHash
			report.DynamicSuffixBytes = render.DynamicSuffixBytes
			report.ProviderRenderHash = render.ProviderRenderHash
			report.ProviderVisibleBytes = render.ProviderVisibleBytes
		}
	}

	if *jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(stderr, "encode: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	fmt.Fprintf(stdout, "snapshot=%d actor=%s fell_back=%v version=%s\n", report.ContextSnapshotID, report.ActorRoleID, report.FellBackToCanonical, report.Version)
	fmt.Fprintf(stdout, "stable_prefix_hash=%s stable_prefix_bytes=%d\n", report.StablePrefixHash, report.StablePrefixBytes)
	fmt.Fprintf(stdout, "dynamic_suffix_hash=%s dynamic_suffix_bytes=%d\n", report.DynamicSuffixHash, report.DynamicSuffixBytes)
	fmt.Fprintf(stdout, "provider_render_hash=%s provider_visible_bytes=%d\n", report.ProviderRenderHash, report.ProviderVisibleBytes)
	if report.BuildError != "" {
		fmt.Fprintf(stdout, "build_error=%s\n", report.BuildError)
	}
	return exitOK
}

type providerRenderShadowReport struct {
	ContextSnapshotID    int64  `json:"context_snapshot_id"`
	ActorRoleID          string `json:"actor_role_id"`
	FellBackToCanonical  bool   `json:"fell_back_to_canonical"`
	Version              string `json:"provider_render_version,omitempty"`
	StablePrefixHash     string `json:"stable_prefix_hash,omitempty"`
	StablePrefixBytes    int    `json:"stable_prefix_bytes,omitempty"`
	DynamicSuffixHash    string `json:"dynamic_suffix_hash,omitempty"`
	DynamicSuffixBytes   int    `json:"dynamic_suffix_bytes,omitempty"`
	ProviderRenderHash   string `json:"provider_render_hash,omitempty"`
	ProviderVisibleBytes int    `json:"provider_visible_bytes,omitempty"`
	BuildError           string `json:"build_error,omitempty"`
}
