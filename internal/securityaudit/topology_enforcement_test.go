package securityaudit_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging/topologyfixture"
	"github.com/Mireuz13/explorarte-organization/internal/securityaudit"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// This file closes ORG-04, which is a defect in how this package establishes
// truth rather than a defect in any one rule.
//
// The catalog advertises `topology_check` in the AuthBoundary of several
// agent_messages channels. Every assertion that existed for that claim read
// the catalog's own string literals -- CheckViolations() compares RoleScope
// against "forged"/"bypass", and RoleScope is a constant this package writes
// a few lines above the rule that inspects it. That is a tautology: it
// cannot fail, and it stayed green through the entire period when the
// enforcement point in Store.Send() was a bare `TODO: Implement topology
// check`. A manifest cannot be evidence for itself.
//
// The tests below make the claim falsifiable by binding it to the real
// enforcement path: they call the same TopologyValidator that Store.Send()
// calls, and require an actual denial. Delete or weaken enforcement and
// these fail, no matter what the catalog continues to say about itself.

const topologyClaim = "topology_check"

// TestTopologyClaimIsBackedByRealDenials is the load-bearing test. For the
// catalog to be allowed to claim topology_check anywhere, the real validator
// must actually deny the edges V1 forbids.
func TestTopologyClaimIsBackedByRealDenials(t *testing.T) {
	claiming := channelsClaimingTopologyCheck()
	if len(claiming) == 0 {
		t.Fatal("no channel claims topology_check; if the claim was removed on purpose, remove this test with it -- do not leave an unenforced claim behind")
	}

	validator := agentmessaging.NewTopologyValidator(topologyfixture.NewReader(), topologyfixture.OrganizationID)
	forbidden := []struct {
		name      string
		sender    string
		recipient string
	}{
		{"CEO to arbitrary worker", topologyfixture.RoleCEO, topologyfixture.RoleEngineeringA},
		{"worker to peer worker", topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringB},
		{"engineering leader to finance worker", topologyfixture.RoleEngineeringLead, topologyfixture.RoleFinanceWorker},
		{"worker to foreign leader", topologyfixture.RoleEngineeringA, topologyfixture.RoleFinanceLead},
		{"worker straight to CEO", topologyfixture.RoleEngineeringA, topologyfixture.RoleCEO},
	}

	for _, edge := range forbidden {
		err := validator.ValidateEdge(context.Background(), edge.sender, edge.recipient)
		if err == nil {
			t.Fatalf("channels %v advertise %q, but the real validator permitted a forbidden edge (%s): %s -> %s",
				claiming, topologyClaim, edge.name, edge.sender, edge.recipient)
		}
		if !errors.Is(err, agentmessaging.ErrTopologyViolation) {
			t.Fatalf("forbidden edge %s was rejected, but not as a topology violation: %v", edge.name, err)
		}
	}
}

// TestTopologyClaimStillPermitsLegitimateEdges keeps the test above honest.
// A validator that denied everything would satisfy it while breaking the
// organization, so the permitted edges are asserted in the same breath.
func TestTopologyClaimStillPermitsLegitimateEdges(t *testing.T) {
	validator := agentmessaging.NewTopologyValidator(topologyfixture.NewReader(), topologyfixture.OrganizationID)
	permitted := []struct {
		name      string
		sender    string
		recipient string
	}{
		{"CEO to department leader", topologyfixture.RoleCEO, topologyfixture.RoleEngineeringLead},
		{"leader to own worker", topologyfixture.RoleEngineeringLead, topologyfixture.RoleEngineeringA},
		{"worker to own leader", topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringLead},
		{"leader to CEO", topologyfixture.RoleEngineeringLead, topologyfixture.RoleCEO},
	}
	for _, edge := range permitted {
		if err := validator.ValidateEdge(context.Background(), edge.sender, edge.recipient); err != nil {
			t.Fatalf("topology enforcement denied a legitimate V1 edge (%s): %s -> %s: %v",
				edge.name, edge.sender, edge.recipient, err)
		}
	}
}

// TestCatalogClaimsAreNotSelfCertifying documents the rule this file exists
// to enforce, in the place a future author is most likely to look before
// adding a new AuthBoundary claim.
//
// A control does not exist because the manifest says it exists. It exists
// only when a prohibited action can be shown to fail. Any channel claiming
// topology_check must be covered by the executable denials above; this test
// fails when a new claim appears without anyone extending that coverage.
func TestCatalogClaimsAreNotSelfCertifying(t *testing.T) {
	known := map[string]bool{
		"agent_messages_send":                  true,
		"agent_messages_idempotency_collision": true,
	}
	for _, name := range channelsClaimingTopologyCheck() {
		if !known[name] {
			t.Fatalf("channel %q newly claims %q. Extend TestTopologyClaimIsBackedByRealDenials to prove the claim "+
				"against the real enforcement path, then add the channel here. Do not add it here alone -- that "+
				"reintroduces exactly the self-certifying manifest this file exists to prevent.", name, topologyClaim)
		}
	}
}

func channelsClaimingTopologyCheck() []string {
	var names []string
	for _, channel := range securityaudit.Catalog() {
		if strings.Contains(channel.AuthBoundary, topologyClaim) {
			names = append(names, channel.Name)
		}
	}
	return names
}

// mitigationEvidence maps each payload-channel mitigation label the catalog
// is allowed to claim to the executable proof that the control behind it
// actually holds. A label with no entry here fails the test below.
//
// This is the ORG-04 rule applied to the payload channels: a control exists
// only when a prohibited action can be shown to fail. The cheap way to
// silence secretSmugglingViaPayload is to invent a reassuring DataClass
// string, and that is exactly what this prevents.
var mitigationEvidence = map[string]string{
	"closed_schema_no_free_text":      "TestPayloadSmugglingClaimIsBackedByRealRejection (this package)",
	"free_text_with_secret_rejection": "TestCreateRequestRejectsSecretsInEveryAgentVisibleField (internal/tasks)",
}

// TestPayloadMitigationClaimsHaveNamedEvidence fails when a channel starts
// claiming a mitigation nobody has proved.
func TestPayloadMitigationClaimsHaveNamedEvidence(t *testing.T) {
	for _, channel := range securityaudit.Catalog() {
		switch channel.Name {
		case "agent_messages_payload_smuggling", "tasks_instructions":
		default:
			continue
		}
		if _, proved := mitigationEvidence[channel.DataClass]; !proved {
			t.Fatalf("channel %q claims mitigation %q, which has no named executable evidence. Add a behavioral "+
				"test that drives a real violation through the actual write path and observe it fail, then register "+
				"it in mitigationEvidence. Editing the label alone reintroduces the self-certifying manifest ORG-04 "+
				"was raised for.", channel.Name, channel.DataClass)
		}
	}
}

// TestTasksInstructionsMitigationStaysProved keeps the tasks side honest:
// the catalog may only describe that channel as secret-rejecting for as long
// as the ingress path really rejects.
func TestTasksInstructionsMitigationStaysProved(t *testing.T) {
	for _, channel := range securityaudit.Catalog() {
		if channel.Name != "tasks_instructions" {
			continue
		}
		if channel.DataClass != "free_text_with_secret_rejection" {
			t.Fatalf("tasks_instructions now declares %q; if the ingress rejection was removed, restore the "+
				"critical finding instead of leaving the channel described as something it is not", channel.DataClass)
		}
		if channel.SizeBoundBytes != 65536 {
			t.Fatalf("tasks_instructions declares SizeBoundBytes=%d; the enforced bound is 65536 "+
				"(see ValidateCreateRequest)", channel.SizeBoundBytes)
		}
		return
	}
	t.Fatal("tasks_instructions disappeared from the catalog")
}

// TestPayloadSmugglingClaimIsBackedByRealRejection supplies the evidence
// that TestSecretDetectionClaimRequiresEvidence demands for the
// agent_messages_payload_smuggling channel.
//
// The catalog labels that channel "structured_payload_with_secret_detection",
// and nothing in internal/agentmessaging scans for secrets -- grep finds no
// occurrence of secret or clinical anywhere in the package. The label is
// inaccurate, but the control behind it is real and stronger than the label
// suggests: the V1 payload schema is closed and carries exactly one integer
// field, so there is no free-text field for a secret to travel in, and any
// additional field is rejected outright. A scanner would be a weaker control
// than having nowhere to hide.
//
// That distinction is only worth anything if it is enforced, so this test
// pushes real smuggling attempts through the actual validation path.
func TestPayloadSmugglingClaimIsBackedByRealRejection(t *testing.T) {
	recipientTask := int64(2)
	base := func(payload string) agentmessaging.SendCommand {
		return agentmessaging.SendCommand{
			OrganizationID:  "explorarte",
			SenderRoleID:    "ingenieria/lead",
			SenderTaskID:    1,
			RecipientRoleID: "ingenieria/worker_a",
			RecipientTaskID: &recipientTask,
			CorrelationID:   "corr-1",
			CausationID:     "cause-1",
			MessageType:     agentmessaging.MessageDelegation,
			SchemaVersion:   agentmessaging.SchemaVersionV1,
			Payload:         json.RawMessage(payload),
			IdempotencyKey:  "idem-1",
			MaxAttempts:     5,
		}
	}

	// The honest baseline: a well-formed payload is accepted, so the
	// rejections below mean something.
	if err := base(`{"delegated_task_id":2}`).Validate(); err != nil {
		t.Fatalf("a well-formed delegation payload must validate, got: %v", err)
	}

	smuggling := []struct {
		name    string
		payload string
	}{
		{"free-text field alongside the task id", `{"delegated_task_id":2,"note":"patient Sonia Silva, diagnosis ADHD"}`},
		{"credential in an extra field", `{"delegated_task_id":2,"api_key":"sk-live-0000000000"}`},
		{"nested object smuggling a record", `{"delegated_task_id":2,"ctx":{"clinical_note":"confidential"}}`},
		{"field renamed to look benign", `{"delegated_task_id":2,"schema_version":"v1"}`},
		{"only free text, no task id", `{"note":"secret"}`},
	}
	for _, attempt := range smuggling {
		t.Run(attempt.name, func(t *testing.T) {
			if err := base(attempt.payload).Validate(); err == nil {
				t.Fatalf("payload %s was accepted; the closed-schema control that the catalog reports as "+
					"secret detection is not actually holding", attempt.payload)
			}
		})
	}
}

// TestTasksMitigationIsProvedHere binds the catalog's claim to the control
// in the same place the claim is made.
//
// Without this, the binding is a convention: mitigationEvidence names a test
// in internal/tasks, and neutralising the ingress rejection fails that test
// while every assertion in this package stays green. A reader of
// securityaudit would still see "free_text_with_secret_rejection" and no
// local reason to doubt it. Calling the real validator from here closes that
// gap -- remove the rejection and this package fails too.
func TestTasksMitigationIsProvedHere(t *testing.T) {
	request := tasks.NormalizeCreateRequest(tasks.CreateRequest{
		OrganizationID:    "explorarte",
		RequestedByRoleID: "ingenieria/lead",
		AssignedRoleID:    "ingenieria/worker_a",
		IdempotencyKey:    "idem-securityaudit",
		Title:             "Rotar credenciales",
		Instructions:      "Despliega usando ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 en el runner.",
		MaxAttempts:       5,
	})
	err := tasks.ValidateCreateRequest(request)
	if err == nil {
		t.Fatal("tasks_instructions is described as free_text_with_secret_rejection, but the real ingress path " +
			"accepted credential material")
	}
	if !errors.Is(err, tasks.ErrSecretRejected) {
		t.Fatalf("ingress rejected the credential with the wrong error class: %v", err)
	}

	clean := tasks.NormalizeCreateRequest(tasks.CreateRequest{
		OrganizationID:    "explorarte",
		RequestedByRoleID: "ingenieria/lead",
		AssignedRoleID:    "ingenieria/worker_a",
		IdempotencyKey:    "idem-securityaudit-2",
		Title:             "Redactar informe clínico",
		Instructions:      "Redacta el informe de la paciente Sonia Silva (TH001-PX-001), diagnóstico TDAH inatento.",
		MaxAttempts:       5,
	})
	if err := tasks.ValidateCreateRequest(clean); err != nil {
		t.Fatalf("the mitigation must reject credentials without becoming a censor of clinical data: %v", err)
	}
}
