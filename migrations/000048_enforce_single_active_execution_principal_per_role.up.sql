-- Migration 000048: at most one active role-bound execution principal per role.
--
-- EXEC-PRINCIPAL-001: agent-messaging's per-hop sender authentication
-- (validateSenderRoleWithPrincipal in internal/executive/runtimeadapter)
-- requires resolving "the active execution principal authorized to act as
-- role X" deterministically -- for THAT specific purpose, exactly one
-- principal per role, or a resolver would have no basis to pick among
-- candidates.
--
-- This must NOT be a blanket constraint over the whole table: model
-- dispatch (internal/modeldispatch, a different, pre-existing subsystem)
-- legitimately registers multiple distinct principals sharing one
-- dispatch_actor_role_id -- e.g. several worker processes all authorized
-- to dispatch model calls "as" the same subject role
-- (internal/modelruntime/postgres/integration_test.go's own "execution
-- principal mismatch" test proves this by registering exactly that). That
-- is a different concept: a technical dispatcher identity scoped to a
-- role (semantics A), not the one authenticated organizational sender for
-- that role (semantics B), and this migration must not collapse the two.
--
-- The role-bound/ principal_key prefix (see
-- runtimeadapter.roleBoundPrincipalKeyPrefix) is what distinguishes an
-- EXEC-PRINCIPAL-001 role-bound principal from every other principal in
-- this table, so the uniqueness is scoped to exactly that namespace.
CREATE UNIQUE INDEX model_execution_principals_active_role_bound_idx
    ON model_execution_principals (organization_id, dispatch_actor_role_id)
    WHERE status = 'active' AND principal_key LIKE 'role-bound/%';
