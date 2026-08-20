package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/composition"
	"github.com/Mireuz13/explorarte-organization/internal/composition/observe"
	compositionpostgres "github.com/Mireuz13/explorarte-organization/internal/composition/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	egressbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelegress/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

func printCompositionUsage(w io.Writer) {
	fmt.Fprint(w, `usage: orgctl composition <command>

  observe            read the world and report admission and convergence
  step [--apply]     compute the next safe transition; --apply performs it
  run [--interval=D] observe, take at most one step, repeat

The reconciler runs outside the runtime it reconciles. It changes the durable
lifecycle record; replacing a process is a separate act and this command does
not perform one.
`)
}

func runComposition(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printCompositionUsage(stderr)
		return exitUsage
	}
	command, rest := args[0], args[1:]

	jsonOutput := false
	apply := false
	interval := 30 * time.Second
	for _, arg := range rest {
		switch {
		case arg == "--json":
			jsonOutput = true
		case arg == "--apply":
			apply = true
		case arg == "-h" || arg == "--help":
			printCompositionUsage(stdout)
			return exitOK
		case len(arg) > 11 && arg[:11] == "--interval=":
			d, err := time.ParseDuration(arg[11:])
			if err != nil || d < time.Second {
				fmt.Fprintf(stderr, "invalid --interval %q\n", arg[11:])
				return exitUsage
			}
			interval = d
		default:
			fmt.Fprintf(stderr, "unknown argument %q\n", arg)
			return exitUsage
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store, _, code := openDatabase(ctx, cfg, stderr, "composition")
	if code != exitOK {
		return code
	}
	defer store.Close()

	reconciler, code := openReconciler(ctx, cfg, store, stderr)
	if code != exitOK {
		return code
	}

	switch command {
	case "observe":
		return reconciler.report(ctx, jsonOutput, stdout, stderr)
	case "step":
		return reconciler.once(ctx, apply, stdout, stderr)
	case "run":
		return reconciler.loop(ctx, interval, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown composition command %q\n", command)
		printCompositionUsage(stderr)
		return exitUsage
	}
}

type reconciler struct {
	graph    *composition.Graph
	observer observe.Observer
	store    *compositionpostgres.Store
}

func openReconciler(ctx context.Context, cfg config.Config, store *postgres.Store, stderr io.Writer) (reconciler, int) {
	graph, err := composition.Baseline()
	if err != nil {
		fmt.Fprintf(stderr, "build baseline composition: %v\n", err)
		return reconciler{}, exitInternal
	}
	lifecycle, err := compositionpostgres.New(store.Pool())
	if err != nil {
		fmt.Fprintf(stderr, "open composition store: %v\n", err)
		return reconciler{}, exitInternal
	}
	egressRuntime, err := egressbootstrap.Open(cfg, store)
	if err != nil {
		fmt.Fprintf(stderr, "create model egress runtime: %v\n", err)
		return reconciler{}, exitInternal
	}
	observer := observe.Observer{
		Egress: egressRuntime.Service,
		Schema: observe.NewSchemaTip(store.Pool()),
	}
	// The fleet's identity is read from the fleet, never from this
	// process. Without a configured endpoint those two keys stay
	// unobserved, which denies admission and says why -- the correct
	// answer for a deployment that does not publish what it is running.
	if url := os.Getenv("ORG_COMPOSITION_FLEET_VERSION_URL"); url != "" {
		observer.Fleet = observe.NewHTTPBuild(url)
	}
	if dir, ref := os.Getenv("ORG_COMPOSITION_REPO_DIR"), os.Getenv("ORG_COMPOSITION_TARGET_REF"); dir != "" && ref != "" {
		observer.Desired = observe.NewGitRef(dir, ref)
	}
	_ = ctx
	return reconciler{graph: graph, observer: observer, store: lifecycle}, exitOK
}

type observationReport struct {
	Observed   map[string]string `json:"observed"`
	Unobserved map[string]string `json:"unobserved,omitempty"`
	Admitted   []string          `json:"admitted"`
	Refused    map[string]string `json:"refused,omitempty"`
	Converged  bool              `json:"converged"`
	Lifecycle  string            `json:"lifecycle_error,omitempty"`
	Divergence []string          `json:"divergence,omitempty"`
	Next       string            `json:"next_step,omitempty"`
}

// look reads both halves and keeps them independent.
//
// The observation of the world's keys and the durable lifecycle record are
// two different reads, and a failure of the second must not suppress the
// first. Before the lifecycle table exists there is nothing to load and every
// key is still perfectly readable -- which is exactly the state a deployment
// is in when somebody most wants to ask what this model sees, and the worst
// possible moment to answer "database error" instead of answering.
func (r reconciler) look(ctx context.Context) (composition.Observation, *composition.World, observe.Result, error) {
	result := r.observer.Observe(ctx)
	world, err := r.store.Load(ctx)
	if err != nil {
		return result.Observation, nil, result, err
	}
	return result.Observation, world, result, nil
}

func (r reconciler) report(ctx context.Context, asJSON bool, stdout, stderr io.Writer) int {
	obs, world, result, lifecycleErr := r.look(ctx)
	admitted, refused := r.graph.Admissible(obs)
	converged, divergence := r.graph.Converged(obs)

	report := observationReport{
		Observed: map[string]string{}, Unobserved: map[string]string{},
		Admitted: admitted, Refused: refused, Converged: converged, Divergence: divergence,
	}
	if lifecycleErr != nil {
		report.Lifecycle = lifecycleErr.Error()
	}
	for k, v := range obs {
		report.Observed[string(k)] = v
	}
	for _, k := range result.Missing() {
		report.Unobserved[string(k)] = result.Unobserved[k]
	}
	if world != nil {
		if step, ok := composition.Next(r.graph, world, obs, time.Now()); ok {
			report.Next = step.String()
		}
	}

	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "encode report: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	for _, k := range sortedObservationKeys(obs) {
		fmt.Fprintf(stdout, "observed   %-32s %s\n", k, obs[k])
	}
	for _, k := range result.Missing() {
		fmt.Fprintf(stdout, "unobserved %-32s %s\n", k, result.Unobserved[k])
	}
	for _, id := range admitted {
		fmt.Fprintf(stdout, "admitted   %s\n", id)
	}
	for _, id := range r.graph.Order() {
		if reason, ok := refused[id]; ok {
			fmt.Fprintf(stdout, "refused    %s: %s\n", id, reason)
		}
	}
	if converged {
		fmt.Fprintln(stdout, "converged  yes")
	} else {
		for _, d := range divergence {
			fmt.Fprintf(stdout, "diverged   %s\n", d)
		}
	}
	switch {
	case lifecycleErr != nil:
		fmt.Fprintf(stdout, "lifecycle  unavailable: %v\n", lifecycleErr)
		fmt.Fprintln(stdout, "next       unknown -- the observation above stands on its own")
	case report.Next != "":
		fmt.Fprintf(stdout, "next       %s\n", report.Next)
	default:
		fmt.Fprintln(stdout, "next       nothing to do")
	}
	return exitOK
}

// once takes at most one step. Reporting what it would do and doing it are
// the same code path with one flag between them, so a dry run cannot drift
// from the thing it is a rehearsal for.
func (r reconciler) once(ctx context.Context, apply bool, stdout, stderr io.Writer) int {
	obs, world, _, err := r.look(ctx)
	if err != nil {
		// Taking a step needs the durable record; observing does not.
		fmt.Fprintf(stderr, "load composition state: %v\n", err)
		return exitDatabase
	}
	now := time.Now()
	step, ok := composition.Next(r.graph, world, obs, now)
	if !ok {
		fmt.Fprintln(stdout, "nothing to do")
		return exitOK
	}
	if !apply {
		fmt.Fprintf(stdout, "would %s\n", step)
		return exitOK
	}
	episode, _ := world.Episode(step.Episode)
	if err := r.store.ApplyStep(ctx, step, episode.State, now); err != nil {
		if errors.Is(err, compositionpostgres.ErrStaleStep) {
			// Somebody else moved first. That is an ordinary outcome
			// of a durable world with more than one observer, not a
			// failure, and the answer is to look again next turn
			// rather than to force a decision about a world that no
			// longer exists.
			fmt.Fprintf(stdout, "skipped %s: %v\n", step, err)
			return exitOK
		}
		fmt.Fprintf(stderr, "apply %s: %v\n", step, err)
		return exitDatabase
	}
	fmt.Fprintf(stdout, "applied %s\n", step)
	return exitOK
}

// loop is the RunOnce shape: observe the durable world, take at most one safe
// step, return. Never a plan and never a batch -- a batch assumes the process
// survives to the end of it, and this process exists precisely because
// processes do not.
func (r reconciler) loop(ctx context.Context, interval time.Duration, stdout, stderr io.Writer) int {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if code := r.once(ctx, true, stdout, stderr); code != exitOK && code != exitDatabase {
			return code
		}
		select {
		case <-ctx.Done():
			fmt.Fprintln(stdout, "stopping")
			return exitOK
		case <-ticker.C:
		}
	}
}

func sortedObservationKeys(obs composition.Observation) []composition.Key {
	out := make([]composition.Key, 0, len(obs))
	for k := range obs {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
