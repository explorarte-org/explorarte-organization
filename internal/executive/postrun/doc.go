// Package postrun reads a terminal decisiongraph run left behind by
// internal/executive (Rama 25), independently re-verifies its completion
// obligations, and — only when there is a real, non-fabricated problem to
// record — proposes a governed internal/memory candidate for human review.
//
// This package never mutates internal/tasks or internal/decisiongraph
// state, never bypasses internal/memory's own authorization gate, and never
// auto-approves anything: a proposed candidate still needs a human reviewer
// to reach memory.StatusApproved, exactly like every other memory source.
package postrun
