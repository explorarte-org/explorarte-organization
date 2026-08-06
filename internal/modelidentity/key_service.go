package modelidentity

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

const (
	capabilityKeyRegister = "model.execution_identity_key.register"
	capabilityKeyRotate   = "model.execution_identity_key.rotate"
	capabilityKeyRetire   = "model.execution_identity_key.retire"
	capabilityKeyRevoke   = "model.execution_identity_key.revoke"
)

func validOpaqueSecretRef(value string) bool {
	if len(value) < 4 || len(value) > 500 || !strings.Contains(value, "://") {
		return false
	}
	scheme, rest, ok := strings.Cut(value, "://")
	if !ok || rest == "" || !identifierPattern.MatchString(scheme) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func validIdempotencyKey(value string) bool {
	if len(value) < 1 || len(value) > 200 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type KeyService struct {
	organizationID string
	authorizer     CapabilityAuthorizer
	catalog        OrganizationCatalog
	principals     PrincipalResolver
	store          KeyStore
	clock          Clock
}

func NewKeyService(organizationID string, authorizer CapabilityAuthorizer, catalog OrganizationCatalog, principals PrincipalResolver, store KeyStore, clock Clock) (*KeyService, error) {
	if authorizer == nil || catalog == nil || principals == nil || store == nil {
		return nil, fmt.Errorf("model identity key dependencies are incomplete")
	}
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	return &KeyService{organizationID: organizationID, authorizer: authorizer, catalog: catalog, principals: principals, store: store, clock: clock}, nil
}

func (s *KeyService) prepare(ctx context.Context, actor string, command RegisterKeyCommand, capability string) (PreparedKey, error) {
	actor = strings.TrimSpace(actor)
	if command.OrganizationID == "" {
		command.OrganizationID = s.organizationID
	}
	principalKey := strings.TrimSpace(command.ExecutionPrincipalKey)
	secretRef := strings.TrimSpace(command.SecretRef)
	idempotencyKey := strings.TrimSpace(command.IdempotencyKey)
	if command.OrganizationID != s.organizationID || actor == "" || principalKey == "" ||
		!validOpaqueSecretRef(secretRef) || !validIdempotencyKey(idempotencyKey) {
		return PreparedKey{}, ErrInvalidKey
	}
	revision, err := s.catalog.CurrentRevision(ctx, command.OrganizationID)
	if err != nil {
		return PreparedKey{}, err
	}
	if err = s.authorizer.Authorize(ctx, command.OrganizationID, revision, actor, capability); err != nil {
		return PreparedKey{}, fmt.Errorf("%w: %v", ErrAuthorizationDenied, err)
	}
	principal, err := s.principals.ResolveByKey(ctx, command.OrganizationID, principalKey)
	if err != nil {
		return PreparedKey{}, err
	}
	if principal.Status != modeldispatch.PrincipalActive {
		return PreparedKey{}, ErrKeyInactive
	}
	role, err := s.catalog.GetRole(ctx, command.OrganizationID, principal.DispatchActorRoleID)
	if err != nil {
		return PreparedKey{}, err
	}
	if !role.Enabled || !role.Executable || role.AuthorityClass != "execution_service" {
		return PreparedKey{}, ErrKeyInactive
	}
	if err = s.authorizer.Authorize(ctx, command.OrganizationID, revision, principal.DispatchActorRoleID, "model.invoke"); err != nil {
		return PreparedKey{}, ErrKeyInactive
	}
	publicKey, err := DecodePublicKey(command.PublicKeyBase64)
	if err != nil {
		return PreparedKey{}, err
	}
	if command.ValidUntil != nil && !command.ValidUntil.After(s.clock.Now().Add(time.Minute)) {
		return PreparedKey{}, ErrInvalidKey
	}
	prepared := PreparedKey{
		OrganizationID: command.OrganizationID, ExecutionPrincipalID: principal.ID,
		PublicKey: publicKey, PublicKeyFingerprint: PublicKeyFingerprint(publicKey),
		SecretRef: secretRef, ValidUntil: command.ValidUntil,
		IdempotencyKey: idempotencyKey, CreatedByRoleID: actor,
	}
	prepared.RequestHash, err = KeyRequestHash(prepared)
	return prepared, err
}

func (s *KeyService) Register(ctx context.Context, actor string, command RegisterKeyCommand) (RegisterKeyResult, error) {
	prepared, err := s.prepare(ctx, actor, command, capabilityKeyRegister)
	if err != nil {
		return RegisterKeyResult{}, err
	}
	return s.store.RegisterKey(ctx, prepared)
}

func (s *KeyService) Rotate(ctx context.Context, actor string, command RotateKeyCommand) (RegisterKeyResult, error) {
	prepared, err := s.prepare(ctx, actor, command, capabilityKeyRotate)
	if err != nil {
		return RegisterKeyResult{}, err
	}
	return s.store.RotateKey(ctx, prepared)
}

func (s *KeyService) Get(ctx context.Context, id int64) (ExecutionIdentityKey, error) {
	if id <= 0 {
		return ExecutionIdentityKey{}, ErrInvalidKey
	}
	return s.store.GetKey(ctx, id)
}

func (s *KeyService) List(ctx context.Context, principalID int64, limit int) ([]ExecutionIdentityKey, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return s.store.ListKeys(ctx, s.organizationID, principalID, limit)
}

func (s *KeyService) Retire(ctx context.Context, actor string, keyID int64) (ExecutionIdentityKey, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" || keyID <= 0 {
		return ExecutionIdentityKey{}, ErrInvalidKey
	}
	revision, err := s.catalog.CurrentRevision(ctx, s.organizationID)
	if err != nil {
		return ExecutionIdentityKey{}, err
	}
	if err = s.authorizer.Authorize(ctx, s.organizationID, revision, actor, capabilityKeyRetire); err != nil {
		return ExecutionIdentityKey{}, fmt.Errorf("%w: %v", ErrAuthorizationDenied, err)
	}
	return s.store.RetireKey(ctx, keyID, actor)
}

func (s *KeyService) Revoke(ctx context.Context, actor string, keyID int64, reason string) (ExecutionIdentityKey, error) {
	actor = strings.TrimSpace(actor)
	reason = strings.TrimSpace(reason)
	if actor == "" || keyID <= 0 || len(reason) > 100 || !identifierPattern.MatchString(reason) {
		return ExecutionIdentityKey{}, ErrInvalidKey
	}
	revision, err := s.catalog.CurrentRevision(ctx, s.organizationID)
	if err != nil {
		return ExecutionIdentityKey{}, err
	}
	if err = s.authorizer.Authorize(ctx, s.organizationID, revision, actor, capabilityKeyRevoke); err != nil {
		return ExecutionIdentityKey{}, fmt.Errorf("%w: %v", ErrAuthorizationDenied, err)
	}
	return s.store.RevokeKey(ctx, keyID, actor, reason)
}
