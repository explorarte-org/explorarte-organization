// Package agentmessaging is a durable inbox for agent-to-agent messages —
// CEO<->coordinador delegation/completion, coordinador<->worker
// delegation/completion — modeled on internal/tasks' outbox_events
// claim/lease/attempt-count/dead-letter shape rather than inventing a new
// locking pattern for the same problem.
package agentmessaging
