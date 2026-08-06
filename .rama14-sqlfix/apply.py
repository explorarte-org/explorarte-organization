from pathlib import Path


def replace(path: str, old: str, new: str, count: int = 1) -> None:
    p = Path(path)
    text = p.read_text()
    if old not in text:
        raise SystemExit(f"marker not found in {path}: {old!r}")
    p.write_text(text.replace(old, new, count))


store = "internal/decisiongraph/postgres/store.go"
replace(store, "wall_time_delta_ms", "wall_time_ms_delta", 2)
replace(
    store,
    '''eventHash := eventDigest("branch_transitioned", request.RunID, request.NodeID, current, request.ToState, request.EvidenceHash, request.ReasonCode, request.Actor)''',
    '''eventHash := eventDigest("branch_transitioned", request.RunID, request.NodeID, current, request.ToState, request.EvidenceHash, request.ReasonCode, request.Actor, now.UTC().Format(time.RFC3339Nano))''',
)

fitness = "scripts/check-decisiongraph-fitness.sh"
replace(fitness, "rg -q 'wall_time_delta_ms' \"$store\"", "rg -q 'wall_time_ms_delta' \"$store\"")

# Repeat the same reject/reopen transition to prove event hashes do not collide.
test = "internal/decisiongraph/postgres/integration_test.go"
replace(
    test,
    '''\tif err := service.TransitionBranch(ctx, decisiongraph.BranchTransitionRequest{
\t\tRunID: run.ID, NodeID: candidate.NodeID, ToState: decisiongraph.BranchSelected,
\t\tReasonCode: "candidate_selected", Actor: "integration/decider",
\t}); err != nil {
\t\tt.Fatal(err)
\t}
''',
    '''\tif err := service.TransitionBranch(ctx, decisiongraph.BranchTransitionRequest{
\t\tRunID: run.ID, NodeID: candidate.NodeID,
\t\tToState: decisiongraph.BranchRejectedByEvidence,
\t\tReasonCode: "candidate_temporarily_rejected", Actor: "integration/verifier",
\t}); err != nil {
\t\tt.Fatalf("repeat rejection must remain append-only: %v", err)
\t}
\tclock.Set(clock.Now().Add(time.Microsecond))
\tif err := service.TransitionBranch(ctx, decisiongraph.BranchTransitionRequest{
\t\tRunID: run.ID, NodeID: candidate.NodeID, ToState: decisiongraph.BranchActive,
\t\tEvidenceHash: digest("second-candidate-evidence"), ReasonCode: "new_evidence", Actor: "integration/verifier",
\t}); err != nil {
\t\tt.Fatal(err)
\t}
\tif err := service.TransitionBranch(ctx, decisiongraph.BranchTransitionRequest{
\t\tRunID: run.ID, NodeID: candidate.NodeID, ToState: decisiongraph.BranchSelected,
\t\tReasonCode: "candidate_selected", Actor: "integration/decider",
\t}); err != nil {
\t\tt.Fatal(err)
\t}
''',
)
