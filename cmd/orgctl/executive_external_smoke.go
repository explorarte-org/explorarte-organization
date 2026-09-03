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
	externalSmokeConfirmation = "EXECUTIVE_EXTERNAL_SMOKE_ONCE"
	externalSmokeKeyPrefix    = "external-smoke-"
	externalSmokeMaxUSD       = 0.01
	externalSmokeMaxCalls     = 1
	externalSmokeMaxOutput    = 2000
)

// externalSmokeConfig contains no provider, goal, retry, or budget knobs on
// purpose. The command is a one-shot operational probe, not a second general
// Executive submission API. Keeping the values fixed makes an invocation
// auditable before it can reach Model Runtime.
type externalSmokeConfig struct {
	idempotencyKey string
	jsonOutput     bool
}

func parseExternalSmokeArgs(args []string, stderr io.Writer) (externalSmokeConfig, bool) {
	flags := flag.NewFlagSet("executive external-smoke", flag.ContinueOnError)
	flags.SetOutput(stderr)
	confirmation := flags.String("confirm", "", "must be EXECUTIVE_EXTERNAL_SMOKE_ONCE")
	idempotencyKey := flags.String("idempotency-key", "", "unique external smoke key with external-smoke- prefix")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: orgctl executive external-smoke --confirm EXECUTIVE_EXTERNAL_SMOKE_ONCE --idempotency-key external-smoke-KEY [--json]")
		return externalSmokeConfig{}, false
	}
	if *confirmation != externalSmokeConfirmation {
		fmt.Fprintf(stderr, "external smoke refused: --confirm must equal %s\n", externalSmokeConfirmation)
		return externalSmokeConfig{}, false
	}
	key := strings.TrimSpace(*idempotencyKey)
	if key == "" || !strings.HasPrefix(key, externalSmokeKeyPrefix) || len(key) > 200 {
		fmt.Fprintf(stderr, "external smoke refused: --idempotency-key must be 1..200 bytes with %q prefix\n", externalSmokeKeyPrefix)
		return externalSmokeConfig{}, false
	}
	return externalSmokeConfig{idempotencyKey: key, jsonOutput: *jsonOutput}, true
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
	parsed, ok := parseExternalSmokeArgs(args, stderr)
	if !ok {
		return exitUsage
	}

	budget := executive.DefaultCampaignBudget()
	budget.MaxUSD = modelpricing.USDFromDollars(externalSmokeMaxUSD)
	budget.MaxModelCalls = externalSmokeMaxCalls

	limits := executive.DefaultLimits()
	limits.MaxModelCalls = externalSmokeMaxCalls
	limits.MaxOutputTokens = externalSmokeMaxOutput

	_, runtime, store, ctx, cancel, code := openExecutiveRuntime(
		stderr,
		"executive-external-smoke",
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
		"external_call_limit":     externalSmokeMaxCalls,
		"max_output_tokens":       externalSmokeMaxOutput,
		"hard_cap_usd":            externalSmokeMaxUSD,
		"retry_dispatch_attempts": 1,
		"error":                   errString(resumeErr),
	})
	if resumeErr != nil {
		return executiveExitCode(resumeErr)
	}
	return exitOK
}
