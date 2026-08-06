from pathlib import Path


def replace(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    if old not in text:
        raise SystemExit(f"marker not found in {path}: {old!r}")
    p.write_text(text.replace(old, new, 1))


cli = "cmd/orgctl/decision.go"
replace(
    cli,
    '''\tClaimToken        string                       `json:"claim_token"`
''',
    "",
)
replace(
    cli,
    '''\t\terr := service.FinishExecution(ctx, decisiongraph.FinishExecutionRequest{
\t\t\tExecutionID: input.ExecutionID, ClaimToken: input.ClaimToken, FinalState: input.FinalState,
''',
    '''\t\tclaimToken, err := readSecretToken(os.Stdin)
\t\tif err != nil {
\t\t\tfmt.Fprintln(stderr, err)
\t\t\treturn exitUsage
\t\t}
\t\terr = service.FinishExecution(ctx, decisiongraph.FinishExecutionRequest{
\t\t\tExecutionID: input.ExecutionID, ClaimToken: claimToken, FinalState: input.FinalState,
''',
)

fitness = "scripts/check-decisiongraph-fitness.sh"
replace(
    fitness,
    '''rg -q 'DisallowUnknownFields' cmd/orgctl/decision.go || fail "decision JSON input is not strict"
''',
    '''rg -q 'DisallowUnknownFields' cmd/orgctl/decision.go || fail "decision JSON input is not strict"
rg -q 'readSecretToken\(os\.Stdin\)' cmd/orgctl/decision.go || fail "decision finish token is not read from stdin"
if rg -n 'json:"claim_token"' cmd/orgctl/decision.go; then
  fail "decision claim token must not be accepted from a JSON file"
fi
''',
)

# Keep the CLI test independent from process stdin while asserting the JSON
# schema no longer accepts claim_token.
test = "cmd/orgctl/decision_test.go"
replace(
    test,
    '''func TestParseDecisionFileRejectsMultipleTopLevelValues(t *testing.T) {
''',
    '''func TestParseDecisionFinishRejectsClaimTokenInJSON(t *testing.T) {
\tpath := writeDecisionInput(t, `{"execution_id":1,"claim_token":"must-use-stdin","final_state":"succeeded","input_tokens":0,"output_tokens":0}`)
\tvar input decisionFinishInput
\tvar stderr bytes.Buffer
\tif _, code := parseDecisionFile([]string{"--file", path}, &stderr, &input); code != exitUsage {
\t\tt.Fatalf("code=%d, want %d", code, exitUsage)
\t}
}

func TestParseDecisionFileRejectsMultipleTopLevelValues(t *testing.T) {
''',
)
