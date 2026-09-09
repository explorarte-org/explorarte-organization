package search

import "context"

// =============================================================================
// Runtime bridge — kernel model runtime → search.ModelInvoker
//
// The canonical model runtime (kernel) owns provider transport
// (cloudflare_workers_ai, mistral, openrouter adapters). FreeModelRouter and
// LLMQueryGenerator depend only on the ModelInvoker seam defined here.
//
// Nothing can import both packages (the kernel's modelruntime and this
// package are separate modules), so deployment wiring adapts them with a
// plain function. ModelInvokerFunc makes that adapter first-class:
//
//	invoker := search.ModelInvokerFunc(func(ctx context.Context,
//	    req search.ModelInvocationRequest) (search.ModelInvocationResult, error) {
//	    // map req onto modelruntime.CanonicalRequest
//	    // dispatch through the kernel runtime (provider/model host-resolved)
//	    // map modelruntime.RawResponse back onto ModelInvocationResult
//	})
//
// The mapping itself lives with the deployment/runtime; it carries NO
// provider-specific behavior — that is enforced by the router's
// architecture test and by this type being the ONLY sanctioned bridge.
// =============================================================================

// ModelInvokerFunc adapts a runtime-dispatch function to the ModelInvoker
// seam. The runtime resolves provider/model from the request (host
// authority); the function must honor context cancellation, pass through
// usage and provider request IDs, and return typed errors.
type ModelInvokerFunc func(ctx context.Context, req ModelInvocationRequest) (ModelInvocationResult, error)

// Invoke implements ModelInvoker.
func (f ModelInvokerFunc) Invoke(ctx context.Context, req ModelInvocationRequest) (ModelInvocationResult, error) {
	if f == nil {
		return ModelInvocationResult{}, ErrInvalidRequest
	}
	return f(ctx, req)
}

// Compile-time proof: ModelInvokerFunc satisfies the seam consumed by
// LLMQueryGenerator and FreeModelRouter.
var _ ModelInvoker = ModelInvokerFunc(nil)
