// Package composition describes the operating topology of the organization:
// which components exist, which keys each one requires and provides, and
// which of its effects can be taken back.
//
// It is deliberately small and deliberately ignorant. It does not know about
// tasks, prompts, memory, Git, Postgres, or systemd, and it must not learn.
// Its whole surface answers three questions:
//
//	is this composition well formed?
//	in what order may its components be brought up?
//	which of its effects can be undone, and which only reconciled?
//
// Nothing here performs an effect or touches a running process. A graph that
// validates is a claim about shape, not a claim about the world. Bringing
// that shape about is the reconciler's job, and the reconciler is not here
// yet -- on purpose. The vocabulary has to be right before anything acts on
// it, because every guarantee the runtime will later offer is stated in these
// words.
package composition
