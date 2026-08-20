#!/usr/bin/env bash
# Launcher template for the composition reconciler.
#
# ORGCTL is pinned to an explicit build on purpose. The reconciler's job is to
# replace the fleet's binary; resolving its own executable through the same
# ref it is reconciling would mean the tool and its subject move together,
# which is precisely the coupling this component exists to break.
set -euo pipefail

ORGCTL="${ORG_COMPOSITION_ORGCTL:?pin an explicit orgctl build}"

# Where the desired build is read from. The ref is the truth -- it is what a
# promotion moves -- and reading it is an observation, not a lookup of what
# somebody recorded they intended.
export ORG_COMPOSITION_REPO_DIR="${ORG_COMPOSITION_REPO_DIR:-/opt/explorarte/organization-v2-program}"
export ORG_COMPOSITION_TARGET_REF="${ORG_COMPOSITION_TARGET_REF:-origin/main}"

# Database and application environment are supplied the same way every other
# worker on this host receives them.
: "${ORG_DATABASE_NAME:?database environment not loaded}"

exec "$ORGCTL" composition run --interval="${ORG_COMPOSITION_INTERVAL:-30s}"
