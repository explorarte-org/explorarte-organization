from pathlib import Path


def replace(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    if old not in text:
        raise SystemExit(f"marker not found in {path}: {old!r}")
    p.write_text(text.replace(old, new, 1))


replace(
    "cmd/orgctl/main.go",
    '''\tcase "model":
\t\treturn runModel(args[1:], stdout, stderr)
''',
    '''\tcase "model":
\t\treturn runModel(args[1:], stdout, stderr)
\tcase "decision":
\t\treturn runDecision(args[1:], stdout, stderr)
''',
)
replace(
    "cmd/orgctl/main.go",
    '''  model <registry|invocation>
exit codes:''',
    '''  model <registry|invocation>
  decision <create|append|start|transition|claim|finish|observe|verify|decide|recover|trace>
exit codes:''',
)
replace(
    "scripts/test-integration.sh",
    '''    /tmp/orgctl model identity policy status --json >/tmp/model-identity-policy-status.json
    grep -Fq "\\\"synchronized\\\": true" /tmp/model-identity-policy-status.json
''',
    '''    /tmp/orgctl model identity policy status --json >/tmp/model-identity-policy-status.json
    grep -Fq "\\\"synchronized\\\": true" /tmp/model-identity-policy-status.json
    /tmp/orgctl decision recover --limit 1 --json >/tmp/decision-recover.json
    grep -Fq "\\\"recovered\\\": 0" /tmp/decision-recover.json
''',
)
replace(
    "scripts/check-decisiongraph-fitness.sh",
    '''  internal/decisiongraph/postgres/integration_test.go; do
''',
    '''  internal/decisiongraph/postgres/integration_test.go \\
  cmd/orgctl/decision.go; do
''',
)
replace(
    "scripts/check-decisiongraph-fitness.sh",
    '''rg -q 'wall_time_delta_ms' "$store" || fail "wall-time budget events are missing"
''',
    '''rg -q 'wall_time_delta_ms' "$store" || fail "wall-time budget events are missing"
rg -q 'case "decision"' cmd/orgctl/main.go || fail "orgctl decision command is not wired"
rg -q 'DisallowUnknownFields' cmd/orgctl/decision.go || fail "decision JSON input is not strict"
''',
)
