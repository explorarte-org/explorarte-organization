package rag

import "time"

type LifecycleEvent struct {
	OrganizationID string    `json:"organization_id"`
	DocumentID     string    `json:"document_id"`
	VersionID      string    `json:"version_id"`
	From           Lifecycle `json:"from"`
	To             Lifecycle `json:"to"`
	ActorRoleID    string    `json:"actor_role_id"`
	Reason         string    `json:"reason"`
	OccurredAt     time.Time `json:"occurred_at"`
}
