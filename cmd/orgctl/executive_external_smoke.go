package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	executivebootstrap "github.com/Mireuz13/explorarte-organization/internal/executive/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

const (
	externalSmokeConfirmation         = "EXECUTIVE_EXTERNAL_SMOKE_ONCE"
	externalSmokeKeyPrefix            = "external-smoke-"
	externalSmokeMaxUSD               = 0.01
	externalSmokeMaxCalls             = 1
	externalSmokeMaxOutput            = 2000
	externalSmokeBudgetedConfirmation = "EXECUTIVE_EXTERNAL_SMOKE_5USD_ONCE"
	externalSmokeBudgetedKeyPrefix    = "external-smoke-5usd-"
	externalSmokeBudgetedMaxUSD       = 5
	externalSmokeBudgetedMaxCalls     = 1
	// Model Runtime accepts at most 1,048,576 output tokens. The campaign
	// ceiling remains 5M below; this per-call ceiling is the largest value
	// that can reach the runtime without being rejected before dispatch.
	externalSmokeBudgetedMaxOutput       = 1_048_576
	externalSmokeBudgetedMaxTokens int64 = 5_000_000
)

// externalSmokeConfig contains no provider, goal, retry, or budget knobs on
// purpose. The command is a one-shot operational probe, not a second general
// Executive submission API. Keeping the values fixed makes an invocation
// auditable before it can reach Model Runtime.
type externalSmokeConfig struct {
	idempotencyKey string
	jsonOutput     bool
}

// externalSmokeSpec is deliberately fixed per command. It contains no
// provider, goal, retry, or caller-controlled budget knobs: each confirmation
// token identifies one auditable set of ceilings.
type externalSmokeSpec struct {
	commandName       string
	confirmation      string
	keyPrefix         string
	maxUSD            float64
	maxCalls          int
	maxOutputTokens   int
	maxCampaignTokens int64
}

func externalSmokeDefaultSpec() externalSmokeSpec {
	return externalSmokeSpec{
		commandName:       "external-smoke",
		confirmation:      externalSmokeConfirmation,
		keyPrefix:         externalSmokeKeyPrefix,
		maxUSD:            externalSmokeMaxUSD,
		maxCalls:          externalSmokeMaxCalls,
		maxOutputTokens:   externalSmokeMaxOutput,
		maxCampaignTokens: executive.DefaultCampaignBudget().MaxTokens,
	}
}

func externalSmokeBudgetedSpec() externalSmokeSpec {
	return externalSmokeSpec{
		commandName:       "external-smoke-5usd",
		confirmation:      externalSmokeBudgetedConfirmation,
		keyPrefix:         externalSmokeBudgetedKeyPrefix,
		maxUSD:            externalSmokeBudgetedMaxUSD,
		maxCalls:          externalSmokeBudgetedMaxCalls,
		maxOutputTokens:   externalSmokeBudgetedMaxOutput,
		maxCampaignTokens: externalSmokeBudgetedMaxTokens,
	}
}

func parseExternalSmokeArgs(args []string, stderr io.Writer) (externalSmokeConfig, bool) {
	return parseExternalSmokeArgsFor(args, stderr, externalSmokeDefaultSpec())
}

func parseExternalSmokeArgsFor(args []string, stderr io.Writer, spec externalSmokeSpec) (externalSmokeConfig, bool) {
	flags := flag.NewFlagSet("executive "+spec.commandName, flag.ContinueOnError)
	flags.SetOutput(stderr)
	confirmation := flags.String("confirm", "", "must be "+spec.confirmation)
	idempotencyKey := flags.String("idempotency-key", "", "unique external smoke key with "+spec.keyPrefix+" prefix")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, externalSmokeUsage(spec))
		return externalSmokeConfig{}, false
	}
	if *confirmation != spec.confirmation {
		fmt.Fprintf(stderr, "external smoke refused: --confirm must equal %s\n", spec.confirmation)
		return externalSmokeConfig{}, false
	}
	key := strings.TrimSpace(*idempotencyKey)
	if key == "" || !strings.HasPrefix(key, spec.keyPrefix) || len(key) > 200 {
		fmt.Fprintf(stderr, "external smoke refused: --idempotency-key must be 1..200 bytes with %q prefix\n", spec.keyPrefix)
		return externalSmokeConfig{}, false
	}
	return externalSmokeConfig{idempotencyKey: key, jsonOutput: *jsonOutput}, true
}

func externalSmokeUsage(spec externalSmokeSpec) string {
	return fmt.Sprintf(
		"usage: orgctl executive %s --confirm %s --idempotency-key %sKEY [--json]",
		spec.commandName, spec.confirmation, spec.keyPrefix,
	)
}

func externalSmokeGoal() executive.OwnerGoal {
	return executive.OwnerGoal{
		Goal: "Smoke test only: verify that the deployed Executive can accept and execute one bounded owner request. Do not modify repositories, canonical state, production configuration, or external systems.",
		AcceptanceCriteria: []executive.AcceptanceCriterion{
			{Text: "The smoke request is accepted and its bounded execution state is durably observable.", Phase: executive.AcceptanceDesign},
		},
	}
}

// runExecutiveExternalSmoke is deliberately narrower than executive submit:
// it creates one fixed synthetic request, resumes it once, and then exits.
// The durable campaign budget and the Orchestrator limits independently reject
// a second provider call or a larger output before Model Runtime can send it.
// An error is terminal for this command; there is no retry loop here.
func runExecutiveExternalSmoke(args []string, stdout, stderr io.Writer) int {
	return runExecutiveExternalSmokeWithSpec(args, stdout, stderr, externalSmokeDefaultSpec())
}

func runExecutiveExternalSmoke5USD(args []string, stdout, stderr io.Writer) int {
	return runExecutiveExternalSmokeWithSpec(args, stdout, stderr, externalSmokeBudgetedSpec())
}

func runExecutiveExternalSmokeWithSpec(args []string, stdout, stderr io.Writer, spec externalSmokeSpec) int {
	parsed, ok := parseExternalSmokeArgsFor(args, stderr, spec)
	if !ok {
		return exitUsage
	}

	budget := executive.DefaultCampaignBudget()
	budget.MaxUSD = modelpricing.USDFromDollars(spec.maxUSD)
	budget.MaxTokens = spec.maxCampaignTokens
	budget.MaxModelCalls = int64(spec.maxCalls)

	limits := executive.DefaultLimits()
	limits.MaxModelCalls = spec.maxCalls
	limits.MaxOutputTokens = spec.maxOutputTokens

	_, runtime, store, ctx, cancel, code := openExecutiveRuntime(
		stderr,
		"executive-"+spec.commandName,
		executiveModelCallDeadline,
		executivebootstrap.WithExecutiveLimits(limits),
		executivebootstrap.WithNoRetries(),
	)
	if code != exitOK {
		return code
	}
	defer cancel()
	defer store.Close()

	run, reused, err := runtime.Orchestrator.Submit(ctx, executive.SubmitRequest{
		Goal:           externalSmokeGoal(),
		ActorRoleID:    executive.OwnerRoleID,
		IdempotencyKey: parsed.idempotencyKey,
		Budget:         &budget,
	})
	if err != nil {
		fmt.Fprintf(stderr, "external smoke submit: %v\n", err)
		return executiveExitCode(err)
	}
	if reused {
		// Reusing an existing key would make this invocation's result depend on
		// an earlier run and could expose a previously-spent campaign. It is
		// safe to inspect such a run with `executive status`, but never to
		// drive it from this one-shot command.
		fmt.Fprintln(stderr, "external smoke refused: idempotency key already exists; use a new key")
		return exitInvalid
	}

	resumed, resumeErr := runtime.Orchestrator.ResumeDurable(ctx, run.RootTaskID)
	writeExecutiveValue(stdout, parsed.jsonOutput, map[string]any{
		"run":                     resumed,
		"submitted":               run,
		"reused":                  false,
		"external_call_limit":     spec.maxCalls,
		"max_output_tokens":       spec.maxOutputTokens,
		"max_campaign_tokens":     spec.maxCampaignTokens,
		"hard_cap_usd":            spec.maxUSD,
		"retry_dispatch_attempts": 1,
		"error":                   errString(resumeErr),
	})
	if resumeErr != nil {
		return executiveExitCode(resumeErr)
	}
	return exitOK
}
