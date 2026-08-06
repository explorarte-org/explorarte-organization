// Package evaluation defines the bounded, structured evaluation core used to
// score a self-improvement candidate against a baseline over a fixed suite of
// cases. It loads opaque traces through TraceSource, executes them through
// Evaluator, and reduces the results to metrics, verdicts and baseline-versus-
// candidate comparisons. This core intentionally has no database, network,
// provider, secret or process-execution dependency, and does not import
// internal/decisiongraph: TraceRef is a placeholder opaque reference until
// Branch 14 merges and a decision-graph-backed TraceSource adapter is added.
package evaluation
