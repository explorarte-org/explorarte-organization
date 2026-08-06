// Package improvement defines the bounded, structured self-improvement
// candidate lifecycle: proposing a candidate, evaluating it against a
// baseline, gating its promotion through canary and active, and rolling it
// back. It has no database, network, provider, secret or process-execution
// dependency, does not import internal/decisiongraph, and never allows a
// candidate to reach "active" without passing through evaluation and an
// explicit approval gate.
package improvement
