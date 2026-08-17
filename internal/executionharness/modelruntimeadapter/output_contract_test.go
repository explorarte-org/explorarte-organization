package modelruntimeadapter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

const planSchema = `{"type":"object","required":["plan"],"properties":{"plan":{"type":"string"}}}`

func jsonAdapter(t *testing.T, creator InvocationCreator, dispatcher InvocationDispatcher, schema string) *Adapter {
	t.Helper()
	value, err := New(creator, dispatcher, ClockFunc(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }),
		Config{MaxOutputTokens: 256, ThinkingMode: modelruntime.ThinkingDisabled, InvocationTTL: time.Hour,
			OutputMode: modelruntime.OutputJSON, OutputSchema: json.RawMessage(schema)})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// A structured consumer must receive the canonical JSON Model Runtime persisted,
// not the text field, which is empty under a JSON contract.
func TestJSONOutputContractCarriesCanonicalJSONAsFinalOutput(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	dispatch := successfulDispatch(1, "", nil)
	dispatch.Result.OutputMode = modelruntime.OutputJSON
	dispatch.Result.TextOutput = ""
	dispatch.Result.JSONOutput = json.RawMessage(`{"plan":"do the thing"}`)
	dispatch.Invocation.OutputMode = modelruntime.OutputJSON
	canonicalSchema, err := modelruntime.CanonicalizeRawJSON(json.RawMessage(planSchema))
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Invocation.OutputSchema = canonicalSchema

	creator := &fakeCreator{}
	dispatcher := &fakeDispatcher{results: []modelruntime.DispatchResult{dispatch}}
	adapter := jsonAdapter(t, creator, dispatcher, planSchema)

	result, err := adapter.Invoke(context.Background(), spec.Identity, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalOutput != `{"plan":"do the thing"}` {
		t.Fatalf("final output=%q, want the canonical JSON payload", result.FinalOutput)
	}
	if len(creator.commands) != 1 {
		t.Fatalf("created %d invocations", len(creator.commands))
	}
	if creator.commands[0].OutputMode != modelruntime.OutputJSON || len(creator.commands[0].OutputSchema) == 0 {
		t.Fatalf("the output contract did not reach Model Runtime: %+v", creator.commands[0])
	}
}

// A JSON contract that produced no canonical JSON is a broken result, not an
// empty answer to be handed on.
func TestJSONOutputContractRejectsAnEmptyPayload(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	dispatch := successfulDispatch(1, "", nil)
	dispatch.Invocation.OutputMode = modelruntime.OutputJSON
	dispatch.Result.OutputMode = modelruntime.OutputJSON
	dispatch.Result.TextOutput = ""

	adapter := jsonAdapter(t, &fakeCreator{}, &fakeDispatcher{results: []modelruntime.DispatchResult{dispatch}}, planSchema)
	if _, err := adapter.Invoke(context.Background(), spec.Identity, request); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// The decisive property: an invocation created under one output contract must
// never be adopted by a run asking for a different one.
func TestOutputContractDriftIsRefusedInsteadOfReused(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)

	for _, tc := range []struct {
		name    string
		mutate  func(*modelruntime.Invocation)
		adapter func(*testing.T, InvocationCreator, InvocationDispatcher) *Adapter
	}{
		{
			name:   "schema drift",
			mutate: func(v *modelruntime.Invocation) { v.OutputSchema = json.RawMessage(`{"type":"object"}`) },
			adapter: func(t *testing.T, c InvocationCreator, d InvocationDispatcher) *Adapter {
				return jsonAdapter(t, c, d, planSchema)
			},
		},
		{
			name:   "mode drift",
			mutate: func(v *modelruntime.Invocation) { v.OutputMode = modelruntime.OutputText; v.OutputSchema = nil },
			adapter: func(t *testing.T, c InvocationCreator, d InvocationDispatcher) *Adapter {
				return jsonAdapter(t, c, d, planSchema)
			},
		},
		{
			name:   "max output tokens drift",
			mutate: func(v *modelruntime.Invocation) { v.MaxOutputTokens = 512 },
			adapter: func(t *testing.T, c InvocationCreator, d InvocationDispatcher) *Adapter {
				return newAdapter(t, c, d)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			durable := successfulDispatch(7, "recovered answer", nil)
			invocation := durable.Invocation
			if tc.name != "max output tokens drift" {
				invocation.OutputMode = modelruntime.OutputJSON
				canonical, err := modelruntime.CanonicalizeRawJSON(json.RawMessage(planSchema))
				if err != nil {
					t.Fatal(err)
				}
				invocation.OutputSchema = canonical
			}
			tc.mutate(&invocation)
			creator := &fakeCreator{found: &invocation, input: storedInput(request, 41), outcome: durable}
			dispatcher := &fakeDispatcher{}
			adapter := tc.adapter(t, creator, dispatcher)

			_, err := adapter.Invoke(context.Background(), spec.Identity, request)
			if !errors.Is(err, ErrBindingDrift) {
				t.Fatalf("error=%v, want ErrBindingDrift: a durable invocation under a different contract was adopted", err)
			}
			if creator.outcomes != 0 || len(dispatcher.calls) != 0 {
				t.Fatal("a drifting reuse still touched the provider")
			}
		})
	}
}

// The contract is part of the identity of the invocation, so it must be part of
// the key that finds it.
func TestIdempotencyKeyDistinguishesOutputContracts(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)

	text := &fakeCreator{}
	textAdapter := newAdapter(t, text, &fakeDispatcher{results: []modelruntime.DispatchResult{successfulDispatch(1, "answer", nil)}})
	if _, err := textAdapter.Invoke(context.Background(), spec.Identity, request); err != nil {
		t.Fatal(err)
	}

	jsonDispatch := successfulDispatch(1, "", nil)
	jsonDispatch.Invocation.OutputMode = modelruntime.OutputJSON
	jsonDispatch.Result.OutputMode = modelruntime.OutputJSON
	jsonDispatch.Result.TextOutput = ""
	jsonDispatch.Result.JSONOutput = json.RawMessage(`{"plan":"x"}`)
	structured := &fakeCreator{}
	structuredAdapter := jsonAdapter(t, structured, &fakeDispatcher{results: []modelruntime.DispatchResult{jsonDispatch}}, planSchema)
	if _, err := structuredAdapter.Invoke(context.Background(), spec.Identity, request); err != nil {
		t.Fatal(err)
	}

	if len(text.commands) != 1 || len(structured.commands) != 1 {
		t.Fatalf("commands text=%d json=%d", len(text.commands), len(structured.commands))
	}
	textKey, jsonKey := text.commands[0].IdempotencyKey, structured.commands[0].IdempotencyKey
	if textKey == jsonKey {
		t.Fatalf("both contracts derived the same idempotency key %q", textKey)
	}
	prefix := "execution-harness:" + request.CanonicalDigest + ":"
	if !strings.HasPrefix(textKey, prefix) || !strings.HasPrefix(jsonKey, prefix) {
		t.Fatalf("keys lost the projection digest: %q %q", textKey, jsonKey)
	}
}

func TestOutputContractConfigurationIsValidated(t *testing.T) {
	base := Config{MaxOutputTokens: 256, ThinkingMode: modelruntime.ThinkingDisabled, InvocationTTL: time.Hour}
	clock := ClockFunc(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() })

	jsonWithoutSchema := base
	jsonWithoutSchema.OutputMode = modelruntime.OutputJSON
	if _, err := New(&fakeCreator{}, &fakeDispatcher{}, clock, jsonWithoutSchema); err == nil {
		t.Fatal("a JSON output contract without a schema was accepted")
	}

	textWithSchema := base
	textWithSchema.OutputSchema = json.RawMessage(planSchema)
	if _, err := New(&fakeCreator{}, &fakeDispatcher{}, clock, textWithSchema); err == nil {
		t.Fatal("a text output contract carrying a schema was accepted")
	}

	unknown := base
	unknown.OutputMode = modelruntime.OutputMode("xml")
	if _, err := New(&fakeCreator{}, &fakeDispatcher{}, clock, unknown); err == nil {
		t.Fatal("an unknown output mode was accepted")
	}

	// The default stays free text, so every existing caller is unchanged.
	adapter, err := New(&fakeCreator{}, &fakeDispatcher{}, clock, base)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.config.OutputMode != modelruntime.OutputText {
		t.Fatalf("default output mode=%q", adapter.config.OutputMode)
	}
}

// Model Runtime normalizes a capability set before persisting it. If the
// adapter hashed the caller's raw slice instead, a configuration with a
// duplicate, a stray space or a different ordering would create an invocation
// under one digest and then recompute a different one from the durable row --
// reporting binding drift on a reuse that is in fact identical.
func TestCapabilityCanonicalizationSurvivesDurableReuse(t *testing.T) {
	spec := fixtureSpec()
	request := project(t, spec, nil)
	clock := ClockFunc(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() })

	canonicalConfig := Config{
		MaxOutputTokens: 256, ThinkingMode: modelruntime.ThinkingDisabled, InvocationTTL: time.Hour,
		RequiredCapabilities: []modelruntime.ModelCapability{"a", "b"},
	}
	messyConfig := canonicalConfig
	messyConfig.RequiredCapabilities = []modelruntime.ModelCapability{" b ", "a", "a", "", "b"}

	// A durable row as Model Runtime would have persisted it: normalized.
	durable := successfulDispatch(1, "recovered answer", nil)
	invocation := durable.Invocation
	invocation.RequiredCapabilities = []modelruntime.ModelCapability{"a", "b"}

	for _, tc := range []struct {
		name   string
		config Config
	}{
		{"already canonical", canonicalConfig},
		{"duplicates, whitespace and permuted order", messyConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			creator := &fakeCreator{found: &invocation, input: storedInput(request, 41), outcome: durable}
			adapter, err := New(creator, &fakeDispatcher{}, clock, tc.config)
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Invoke(context.Background(), spec.Identity, request)
			if err != nil {
				t.Fatalf("durable reuse reported drift for an identical capability set: %v", err)
			}
			if result.FinalOutput != "recovered answer" {
				t.Fatalf("result=%+v", result)
			}
		})
	}

	// And both spellings must derive the same contract, so they address the
	// same durable invocation rather than two.
	canonicalAdapter, err := New(&fakeCreator{}, &fakeDispatcher{}, clock, canonicalConfig)
	if err != nil {
		t.Fatal(err)
	}
	messyAdapter, err := New(&fakeCreator{}, &fakeDispatcher{}, clock, messyConfig)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalAdapter.outputContract != messyAdapter.outputContract {
		t.Fatalf("equivalent capability sets derived different contracts: %q vs %q", canonicalAdapter.outputContract, messyAdapter.outputContract)
	}

	// A genuinely different capability set must still be a different contract.
	otherConfig := canonicalConfig
	otherConfig.RequiredCapabilities = []modelruntime.ModelCapability{"a", "c"}
	otherAdapter, err := New(&fakeCreator{}, &fakeDispatcher{}, clock, otherConfig)
	if err != nil {
		t.Fatal(err)
	}
	if otherAdapter.outputContract == canonicalAdapter.outputContract {
		t.Fatal("a different capability set derived the same contract")
	}
}
