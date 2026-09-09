package search

import (
	"fmt"
	"strings"
)

// =============================================================================
// Research model pool V1 — config-driven construction
//
// Builds the FreeModelRouter candidate pool from canonical configuration.
// The router NEVER builds invokers: the deployment provides an
// InvokerFactory backed by the canonical model runtime
// (cloudflare_workers_ai / mistral / openrouter adapters). A provider whose
// invoker is absent is skipped honestly — never fabricated.
// =============================================================================

// Research pool defaults. The Cloudflare model is the one the deployment
// already smoke-tested (HTTP 200 Workers AI). OpenRouter entries come from
// SmokeFreeModelPool (verified against the live catalog). Mistral has NO
// invented default: RESEARCH_MISTRAL_MODEL must come from config.
const (
	DefaultResearchCloudflareModel = "@cf/zai-org/glm-4.7-flash"
	ProviderCloudflareWorkersAI    = "cloudflare_workers_ai"
	ProviderMistral                = "mistral"
	ProviderOpenRouter             = "openrouter"
)

// ResearchPoolConfig resolves the pool from the environment.
type ResearchPoolConfig struct {
	CloudflareModel         string
	MistralModel            string // empty = no Mistral candidate (nothing invented)
	MistralCreditCeilingUSD string // empty = Mistral candidate ineligible (fail-closed)
	OpenRouterPool          []string
	// AllowPaid mirrors FREE_MODEL_ALLOW_PAID; default false.
	AllowPaid bool
}

// LoadResearchPoolConfig reads config WITHOUT duplicating existing env
// names: RESEARCH_CLOUDFLARE_MODEL, RESEARCH_MISTRAL_MODEL,
// FREE_MODEL_ALLOW_PAID are new; SmokeFreeModelPool is reused for
// OpenRouter; the Cloudflare default is the deployment-verified model.
func LoadResearchPoolConfig(lookup LookupEnv) ResearchPoolConfig {
	cfg := ResearchPoolConfig{
		CloudflareModel: DefaultResearchCloudflareModel,
		OpenRouterPool:  SmokeFreeModelPool(),
	}
	if raw, ok := lookup("RESEARCH_CLOUDFLARE_MODEL"); ok && strings.TrimSpace(raw) != "" {
		cfg.CloudflareModel = strings.TrimSpace(raw)
	}
	if raw, ok := lookup("RESEARCH_MISTRAL_MODEL"); ok && strings.TrimSpace(raw) != "" {
		cfg.MistralModel = strings.TrimSpace(raw)
	}
	if raw, ok := lookup("MISTRAL_CREDIT_CEILING_USD"); ok {
		cfg.MistralCreditCeilingUSD = strings.TrimSpace(raw)
	}
	// envBool follows the webconfig conventions.
	if parsed, err := envBool(lookup, "FREE_MODEL_ALLOW_PAID", false); err == nil {
		cfg.AllowPaid = parsed
	}
	return cfg
}

// InvokerFactory resolves the canonical runtime invoker for a provider.
// Deployment wiring backs it with the model runtime's provider adapters;
// returning false means "provider not wired in this runtime".
type InvokerFactory func(provider string) (ModelInvoker, bool)

// BuildResearchModelPool constructs the V1 candidate pool:
//
//  1. cloudflare_workers_ai @cf/zai-org/glm-4.7-flash  free_daily
//  2. openrouter       :free verified IDs               free_model
//  3. mistral          RESEARCH_MISTRAL_MODEL           credit_monthly
//
// A candidate is registered only when its canonical runtime invoker exists.
// Priority within a class: Cloudflare first (daily bucket, deployment-
// verified), then OpenRouter catalog order, then Mistral credit.
func BuildResearchModelPool(cfg ResearchPoolConfig, factory InvokerFactory, events ModelRouterEventSink) (*FreeModelRouter, error) {
	if factory == nil {
		return nil, fmt.Errorf("%w: model pool requires a canonical runtime invoker factory", ErrInvalidRequest)
	}
	candidates := make([]ModelCandidate, 0, 3+len(cfg.OpenRouterPool))

	// 1. Cloudflare Workers AI — free daily bucket (reused, never rebuilt).
	if invoker, ok := factory(ProviderCloudflareWorkersAI); ok && invoker != nil {
		candidates = append(candidates, ModelCandidate{
			Provider:      ProviderCloudflareWorkersAI,
			ModelID:       cfg.CloudflareModel,
			CapacityClass: CapacityFreeDaily,
			Capabilities:  []ModelCapabilityRequirement{CapabilityTextGeneration, CapabilityStructuredJSON},
			Priority:      0,
			Enabled:       true,
			Invoker:       invoker,
		})
	}

	// 2. OpenRouter :free pool — catalog-verified IDs only.
	for i, modelID := range cfg.OpenRouterPool {
		if !strings.HasSuffix(modelID, ":free") {
			// The pool is free-only by construction; refuse accidents.
			return nil, fmt.Errorf("%w: openrouter pool entry %q lacks :free suffix", ErrInvalidRequest, modelID)
		}
		invoker, ok := factory(ProviderOpenRouter)
		if !ok || invoker == nil {
			break // provider not wired: skip the whole class
		}
		candidates = append(candidates, ModelCandidate{
			Provider:      ProviderOpenRouter,
			ModelID:       modelID,
			CapacityClass: CapacityFreeModel,
			Capabilities:  []ModelCapabilityRequirement{CapabilityTextGeneration, CapabilityStructuredJSON},
			Priority:      10 + i,
			Enabled:       true,
			Invoker:       invoker,
		})
	}

	// 3. Mistral — finite monthly credit. NO invented model identity: the
	// candidate exists only when config supplies the model, the runtime
	// wires the provider, AND the deployment configured an explicit local
	// credit ceiling (fail-closed: no ceiling => candidate ineligible —
	// never a default USD-10 assumption).
	if cfg.MistralModel != "" && strings.TrimSpace(cfg.MistralCreditCeilingUSD) != "" {
		if invoker, ok := factory(ProviderMistral); ok && invoker != nil {
			candidates = append(candidates, ModelCandidate{
				Provider:      ProviderMistral,
				ModelID:       cfg.MistralModel,
				CapacityClass: CapacityCreditMonthly,
				Capabilities:  []ModelCapabilityRequirement{CapabilityTextGeneration, CapabilityStructuredJSON},
				Priority:      50,
				Enabled:       true,
				Invoker:       invoker,
			})
		}
	}

	routerCfg := DefaultFreeModelRouterConfig()
	routerCfg.AllowPaid = cfg.AllowPaid
	router, err := NewFreeModelRouter(candidates, routerCfg, events)
	if err != nil {
		return nil, err
	}
	return router, nil
}
