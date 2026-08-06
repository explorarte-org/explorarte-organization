// Package decisiongraphtrace is the bridge between internal/decisiongraph
// (Branch 14's durable decision graph ledger) and internal/evaluation's
// TraceSource port (Branch 15). It reads a terminal ("succeeded")
// decisiongraph run directly out of PostgreSQL — the same pattern
// internal/cellworker/postgres uses to read model_invocations without
// going through another branch's service layer — and reduces it to a
// canonical, deterministically ordered JSON payload of node/edge/decision
// content hashes, never raw payloads or private reasoning.
//
// This package deliberately does not reuse decisiongraph.Store.TraceRef's
// internal audit hash: that format is unexported and not a public contract
// between branches. Instead it is self-verifying — TraceRefForRun and
// LoadTrace both derive TraceHash from this package's own canonical
// payload, so evaluation.EvaluationTrace.Validate's integrity check has
// something stable to check against regardless of how decisiongraph's own
// internal hashing evolves.
package decisiongraphtrace
