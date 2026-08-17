package contextcompiler

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// ResolvedProviderContext is the single deterministic answer to "for this
// canonical context snapshot, what are the exact bytes and digest the
// provider is allowed to see." Every productive caller (Executive's context
// adapter, Model Runtime's context adapter) derives its provider-visible
// context through ResolveProviderContext below so the two can never diverge
// -- this is the fix for the latent defect where Executive sent the
// canonical PortableRenderer envelope through the Harness while Model
// Runtime independently resolved a projected ProviderRender for the same
// snapshot, and PrepareModelInput required the two to be byte-identical.
type ResolvedProviderContext struct {
	// Bytes is the exact byte stream the provider is allowed to see.
	Bytes []byte
	// Digest is the SHA-256 hex digest of Bytes.
	Digest string
	// FellBack is true when the canonical, unprojected render was used
	// instead of a compiled ProviderRender -- either because the task
	// class has no registered ContextProfile, or because
	// BuildProviderRender itself failed for a snapshot the compiler did
	// project. Always explicit and observable, never silent.
	FellBack bool
	// FallbackReason explains FellBack; empty when FellBack is false.
	FallbackReason string
	// ProviderRender is the compiled StablePrefix/DynamicSuffix render
	// this resolution produced. Only populated when FellBack is false.
	ProviderRender contextengine.ProviderRender
	// Compilation is the CompilationResult this resolution was built from
	// (R10's ExecutionContextView shape -- profile identity, segment diffs,
	// stable/dynamic byte partition, authority order hash). Exposed so a
	// durable persistence layer (M1.1) can record it without calling
	// CompileForTaskClass a second time -- ResolveProviderContext remains
	// the single call site.
	Compilation CompilationResult
}

// ResolveProviderContext is the ONLY provider-visible context render
// resolution algorithm. It projects canonical exactly as CompileForTaskClass
// already does (TaskClassOf(canonical.ActorRoleID), unchanged), and then:
//
//   - if the compiler did not fall back, renders the projected snapshot
//     through contextengine.BuildProviderRender (StablePrefix/DynamicSuffix,
//     no AuditEnvelope fields in the provider-visible bytes);
//   - otherwise, or if BuildProviderRender itself errors, renders through
//     the legacy contextengine.PortableRenderer -- explicit and observable
//     (FellBack=true), never silent.
//
// ResolveProviderContext does not fetch or validate the snapshot itself:
// callers must supply an already-fetched, already-validated
// contextengine.Snapshot (via Service.Get + Service.Validate, or
// equivalent), exactly as every existing call site already did before this
// function existed. This function is a pure projection/render step, not a
// bypass around contextengine's own validation.
func ResolveProviderContext(ctx context.Context, canonical contextengine.Snapshot) (ResolvedProviderContext, error) {
	result, err := CompileForTaskClass(canonical)
	if err != nil {
		return ResolvedProviderContext{}, err
	}
	if !result.FellBackToCanonical {
		if render, buildErr := contextengine.BuildProviderRender(result.Projected); buildErr == nil {
			return ResolvedProviderContext{
				Bytes:          render.Bytes(),
				Digest:         render.ProviderRenderHash,
				ProviderRender: render,
				Compilation:    result,
			}, nil
		}
	}
	rendered, err := contextengine.NewRenderer().Render(ctx, result.Projected)
	if err != nil {
		return ResolvedProviderContext{}, err
	}
	reason := "task_class_not_projected"
	if !result.FellBackToCanonical {
		reason = "provider_render_build_failed"
	}
	return ResolvedProviderContext{
		Bytes:          rendered,
		Digest:         contextengine.DigestCanonicalBytes(rendered),
		FellBack:       true,
		FallbackReason: reason,
		Compilation:    result,
	}, nil
}
