package executive

// designDeliverableGuidance states what a department worker's summary has to
// contain. It is told to every department worker, like the egress and structure rules
// beside it in the execution contract, because the rule it states is enforced by a
// reader the worker never meets.
//
// A worker-result carries a summary and evidence. The adversarial reviewer and the
// adjudicator read the candidate design, which is built from the summaries, and nothing
// else the worker produced: not its task, not its instructions. On root 1203
// (2026-09-23) both design workers had the case spelled out in their instructions -- name,
// input, expected output -- and both wrote a summary saying a case had been designed, with
// none of it stated. The reviewers had nothing to judge, and sent the design back with the
// same complaint each round until the rounds ran out.
//
// The rule asks for the proposal itself, in the worker's words, and keeps the two
// prohibitions that already govern the result: nothing reproduced from repository source
// (the values come from the task, which is the owner's text, not from code the worker was
// shown), and no claim that anything was made, run or verified.
func designDeliverableGuidance() string {
	return `Deliverable rule for this result (the reviewers read your summary and nothing else you produced; they never see your task):

- Your summary IS the design. State the proposal itself in concrete terms: every name, input, expected output, assertion and criterion your task or the department constraints specify, written out as specified.
- "A case was designed", "the design covers the requirement" and similar report that work happened without stating it. A summary like that gives the reviewers nothing to judge and is sent back.
- State what was specified. Do not substitute values, names or examples of your own for ones your task already gives.
- The values come from your task, never from repository text you were shown; the egress rule above still applies to anything you describe about the code.
- Do not claim the change was made, that anything was run, or that anything was verified. You are proposing.`
}
