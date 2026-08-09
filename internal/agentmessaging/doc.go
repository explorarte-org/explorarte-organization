// Package agentmessaging is a durable inbox for agent-to-agent messages —
// CEO<->coordinador delegation/completion, coordinador<->worker
// delegation/completion — modeled on internal/tasks' outbox_events
// claim/lease/attempt-count/dead-letter shape rather than inventing a new
// locking pattern for the same problem. ClaimNext is self-healing: in the
// same transaction that claims available work it requeues expired claims or
// dead-letters them after max attempts. Late acknowledgements are rejected,
// so consumers must keep message handling idempotent by durable message ID.
package agentmessaging
