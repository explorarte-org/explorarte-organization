// Package decisiongraph defines the bounded, structured and durable decision
// graph used to coordinate multi-step agent runs. It stores typed nodes,
// semantic edges, dependency ordering, evidence-backed branch transitions,
// terminal decisions and atomic budget accounting without storing private
// chain-of-thought. Provider transport, credentials, tools and task completion
// remain owned by their existing bounded contexts.
package decisiongraph
