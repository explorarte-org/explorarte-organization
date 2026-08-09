// Package agentbudget tracks multidimensional resource limits (USD, tokens,
// model calls, wall time, DAG depth, retries, subagents) for an execution
// tree, with a child task sharing its parent's remaining budget by default
// and optionally being given its own carved-out allocation. Consumption is
// a pure, overflow-safe check-and-add — the same reserve-then-commit shape
// internal/decisiongraph already uses for its own run-scoped budget — kept
// as a separate implementation here because it is scoped to an agent/task
// tree, not a decision-graph run.
package agentbudget
