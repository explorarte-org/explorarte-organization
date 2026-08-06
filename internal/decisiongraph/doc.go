// Package decisiongraph defines the bounded, structured decision graph used to
// coordinate multi-step agent runs. It stores typed nodes, semantic edges,
// dependency ordering, evidence-backed decisions and budget accounting without
// storing private chain-of-thought. PostgreSQL durability and runtime wiring are
// added later in Branch 14; this core intentionally has no database, network,
// provider, secret or process-execution dependency.
package decisiongraph
