package modelidentity

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

type keyTestAuthorizer struct{ err error }

func (a keyTestAuthorizer) Authorize(context.Context, string, int64, string, string) error {
	return a.err
}

type keyTestCatalog struct {
	revision int64
	role     modeldispatch.RoleRef
}

func (c keyTestCatalog) CurrentRevision(context.Context, string) (int64, error) {
	return c.revision, nil
}
func (c keyTestCatalog) GetRole(context.Context, string, string) (modeldispatch.RoleRef, error) {
	if c.role.ID == "" {
		return modeldispatch.RoleRef{ID: "ingenieria_ia/code-runner", Enabled: true, Executable: true, AuthorityClass: "execution_service"}, nil
	}
	return c.role, nil
}

type keyTestPrincipals struct {
	principal modeldispatch.ExecutionPrincipal
}

func (p keyTestPrincipals) ResolveByKey(context.Context, string, string) (modeldispatch.ExecutionPrincipal, error) {
	return p.principal, nil
}

type keyTestStore struct {
	prepared PreparedKey
	result   RegisterKeyResult
}

func (s *keyTestStore) RegisterKey(_ context.Context, p PreparedKey) (RegisterKeyResult, error) {
	s.prepared = p
	return s.result, nil
}
func (s *keyTestStore) RotateKey(_ context.Context, p PreparedKey) (RegisterKeyResult, error) {
	s.prepared = p
	return s.result, nil
}
func (s *keyTestStore) GetKey(context.Context, int64) (ExecutionIdentityKey, error) {
	return ExecutionIdentityKey{}, nil
}
func (s *keyTestStore) ListKeys(context.Context, string, int64, int) ([]ExecutionIdentityKey, error) {
	return nil, nil
}
func (s *keyTestStore) RetireKey(context.Context, int64, string) (ExecutionIdentityKey, error) {
	return ExecutionIdentityKey{}, nil
}
func (s *keyTestStore) RevokeKey(context.Context, int64, string, string) (ExecutionIdentityKey, error) {
	return ExecutionIdentityKey{}, nil
}
func (s *keyTestStore) ResolveActiveKeyByFingerprint(context.Context, string, int64, string) (ExecutionIdentityKey, error) {
	return ExecutionIdentityKey{}, nil
}

func validKeyCommand() RegisterKeyCommand {
	public := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	return RegisterKeyCommand{
		OrganizationID:        "explorarte",
		ExecutionPrincipalKey: "oracle-01/model-runtime-01",
		PublicKeyBase64:       base64.RawStdEncoding.EncodeToString(public),
		SecretRef:             "file://model-execution/oracle-01/key-0001",
		IdempotencyKey:        "identity-key-register-1",
	}
}

func TestKeyServicePreparesBoundedPublicMetadata(t *testing.T) {
	now := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	store := &keyTestStore{}
	service, err := NewKeyService("explorarte", keyTestAuthorizer{}, keyTestCatalog{revision: 7}, keyTestPrincipals{principal: modeldispatch.ExecutionPrincipal{ID: 11, OrganizationID: "explorarte", PrincipalKey: "oracle-01/model-runtime-01", Status: modeldispatch.PrincipalActive}}, store, ClockFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Register(context.Background(), "empresa/human", validKeyCommand()); err != nil {
		t.Fatal(err)
	}
	if store.prepared.ExecutionPrincipalID != 11 || store.prepared.PublicKeyFingerprint == "" || store.prepared.RequestHash == "" || store.prepared.SecretRef != "file://model-execution/oracle-01/key-0001" {
		t.Fatalf("unexpected prepared key: %+v", store.prepared)
	}
}

func TestKeyServiceRejectsUnsafeInputsAndAuthorization(t *testing.T) {
	now := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	principal := modeldispatch.ExecutionPrincipal{ID: 11, OrganizationID: "explorarte", PrincipalKey: "oracle-01/model-runtime-01", Status: modeldispatch.PrincipalActive}
	newService := func(authErr error, value modeldispatch.ExecutionPrincipal) *KeyService {
		s, err := NewKeyService("explorarte", keyTestAuthorizer{err: authErr}, keyTestCatalog{revision: 7}, keyTestPrincipals{principal: value}, &keyTestStore{}, ClockFunc(func() time.Time { return now }))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	cases := []RegisterKeyCommand{
		func() RegisterKeyCommand { c := validKeyCommand(); c.SecretRef = "/tmp/private.pem"; return c }(),
		func() RegisterKeyCommand { c := validKeyCommand(); c.SecretRef = "file://bad ref"; return c }(),
		func() RegisterKeyCommand { c := validKeyCommand(); c.IdempotencyKey = ""; return c }(),
		func() RegisterKeyCommand { c := validKeyCommand(); c.PublicKeyBase64 = "not-a-key"; return c }(),
	}
	for _, command := range cases {
		if _, err := newService(nil, principal).Register(context.Background(), "empresa/human", command); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("expected invalid key for %+v, got %v", command, err)
		}
	}
	if _, err := newService(errors.New("deny"), principal).Register(context.Background(), "empresa/human", validKeyCommand()); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("authorization error=%v", err)
	}
	principal.Status = modeldispatch.PrincipalDisabled
	if _, err := newService(nil, principal).Register(context.Background(), "empresa/human", validKeyCommand()); !errors.Is(err, ErrKeyInactive) {
		t.Fatalf("inactive principal error=%v", err)
	}
}

func TestKeyServiceRejectsUnboundedRevocationReason(t *testing.T) {
	service, err := NewKeyService("explorarte", keyTestAuthorizer{}, keyTestCatalog{revision: 7}, keyTestPrincipals{}, &keyTestStore{}, ClockFunc(time.Now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Revoke(context.Background(), "empresa/human", 1, "free form reason"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("error=%v", err)
	}
}
