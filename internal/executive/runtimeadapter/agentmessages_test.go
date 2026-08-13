package runtimeadapter

import (
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

// TestValidateSenderRoleWithPrincipalDeniesRoleMismatch is a pure unit test
// of the defense-in-depth check in isolation. The integration-level
// TestExecutiveMessagingRejectsPrincipalRoleMismatch (internal/executive)
// proves the LEDGER's own independent re-validation denies a mismatch --
// by design, resolveOrProvisionPrincipalForRole can no longer produce a
// mismatched principal itself, so this function is unreachable from a
// mismatch in normal operation. This test exists so "Mutation B: eliminar
// validateSenderRoleWithPrincipal" (EXEC-PRINCIPAL-001 mutation fitness) is
// caught directly, at the exact unit responsible, rather than relying on
// the integration suite to expose an adapter-level bug the ledger's own
// check happens to also catch.
func TestValidateSenderRoleWithPrincipalDeniesRoleMismatch(t *testing.T) {
	cases := []struct {
		name          string
		principalRole string
		senderRole    string
		wantErr       bool
	}{
		{name: "matching role allowed", principalRole: "empresa/ceo", senderRole: "empresa/ceo", wantErr: false},
		{name: "mismatched role denied", principalRole: "empresa/ceo", senderRole: "ingenieria_ia/orquestador", wantErr: true},
		{name: "empty sender role denied", principalRole: "empresa/ceo", senderRole: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			principal := modeldispatch.ExecutionPrincipal{DispatchActorRoleID: tc.principalRole}
			err := validateSenderRoleWithPrincipal(principal, tc.senderRole)
			if tc.wantErr && err == nil {
				t.Fatalf("principal role=%q sender role=%q: expected denial, got nil", tc.principalRole, tc.senderRole)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("principal role=%q sender role=%q: expected no error, got %v", tc.principalRole, tc.senderRole, err)
			}
		})
	}
}
