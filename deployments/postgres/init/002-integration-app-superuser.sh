#!/usr/bin/env bash
set -euo pipefail

# Integration-only. Never mounted into compose.yaml's production postgres
# service -- only into compose.integration.yaml's isolated override, so this
# never touches the shared development/production role.
#
# Several pre-existing integration tests (e.g.
# internal/platform/postgres/integration_test.go's
# TestPostgresMigrationsAndUnitOfWork) run `DROP SCHEMA public CASCADE` to
# reset state and then recreate the pgvector extension as a same-connection
# no-op-if-present safeguard. pgvector's control file does not mark itself
# trusted, so CREATE EXTENSION requires a real superuser -- the app role
# (explorarte_app, intentionally NOSUPERUSER for production) cannot do it,
# which surfaced only once the isolated integration Postgres instance could
# actually run end-to-end for the first time (see POST_INCIDENT_VALIDATION.md).
# Making the role superuser here is safe specifically because this database
# and role exist only inside the disposable integration container/volume
# torn down after every run -- it is not the production privilege model.

: "${ORG_POSTGRES_USER:?ORG_POSTGRES_USER is required}"

psql --set=ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -c "ALTER ROLE \"$ORG_POSTGRES_USER\" SUPERUSER"
