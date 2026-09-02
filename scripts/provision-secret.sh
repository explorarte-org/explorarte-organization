#!/usr/bin/env bash
# provision-secret.sh (G2-002): writes a secret file to /etc/explorarte/secrets
# with the correct ownership and permissions for the container UID that
# consumes it, in one atomic step -- so a secret can never exist on disk
# with the wrong owner even momentarily.
#
# Background: grok-api-key was created root:root and stayed that way for
# three weeks before this session found it, because secret creation
# (manual sudo file write) was a separate, unchecked step from the
# container wiring that would eventually consume it. This script collapses
# those into one command with no window for the mismatch to exist.
#
# Usage: sudo ./provision-secret.sh <name> <uid:gid> [source-file]
#   name         file name under /etc/explorarte/secrets (e.g. grok-api-key)
#   uid:gid      the CONSUMING CONTAINER's numeric UID:GID -- 65532 for
#                orgd/model-worker/code-runner (see compose.yaml's USER
#                directives), never a username, since the container and
#                the host do not necessarily share a passwd database
#   source-file  path to read the secret content from; omitted means read
#                from stdin (preferred for anything sensitive -- avoids the
#                content ever touching a second file with its own,
#                separately-managed permissions)
set -euo pipefail

if [[ $# -lt 2 || $# -gt 3 ]]; then
  echo "usage: $0 <name> <uid:gid> [source-file]" >&2
  exit 2
fi

name="$1"
owner="$2"
source="${3:-}"

if [[ "$name" == */* || "$name" == "." || "$name" == ".." ]]; then
  echo "refusing: name must be a bare file name, not a path ($name)" >&2
  exit 2
fi
if [[ ! "$owner" =~ ^[0-9]+:[0-9]+$ ]]; then
  echo "refusing: uid:gid must be numeric (e.g. 65532:65532), got '$owner' -- a username is not guaranteed to resolve the same way inside the consuming container" >&2
  exit 2
fi

target="/etc/explorarte/secrets/${name}"
tmp="$(mktemp "${target}.XXXXXX")"
trap 'rm -f "$tmp"' EXIT

if [[ -n "$source" ]]; then
  cat -- "$source" > "$tmp"
else
  cat > "$tmp"
fi

chown "$owner" "$tmp"
chmod 0600 "$tmp"
mv -f "$tmp" "$target"
trap - EXIT

echo "provisioned $target owner=$owner mode=0600"
