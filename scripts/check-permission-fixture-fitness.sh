#!/usr/bin/env bash
# A test fixture that must be group- or world-readable has to say so with an
# explicit Chmod, because os.WriteFile's mode is filtered by the umask.
#
# This exists because the same defect was written twice, a week apart, and the
# second copy was invisible to the first fix's guard: that guard lived inside
# internal/secrets and could only ever see its own package. Under the umask
# 0077 systemd gives these services, such a fixture is created SAFE, the
# validator correctly accepts it, and the test fails while reporting a
# permission bug that does not exist. Both times it took down a production
# gate, not a developer's laptop, because a developer's umask is usually 0022
# and hides it.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

status=0
while IFS=: read -r file line _; do
  # The Chmod that fixes it is written within a few lines of the WriteFile.
  if ! sed -n "${line},$((line + 20))p" "$file" | grep -q 'os.Chmod('; then
    echo "$file:$line: fixture written with group/other bits but never Chmod'd; the umask will strip them" >&2
    status=1
  fi
done < <(grep -rn --include='*_test.go' -E 'os\.WriteFile\([^)]*0o[0-7][1-7][0-7]\)' . | grep -v '/vendor/' || true)

if [ "$status" -eq 0 ]; then
  echo "permission-fixture fitness: ok"
fi
exit "$status"
