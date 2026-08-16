# Q3-002 deterministic candidate navigation packets

These groups are navigation aids, never inventory units. Package or
directory identity MUST NOT be used as capability identity. Every path
listed here remains independently registered in source-universe.jsonl.

## .github

Tracked artifacts: 4; kinds: configuration_or_declaration=4

Non-Go artifacts:
- `.github/workflows/ci.yml` (configuration_or_declaration)
- `.github/workflows/r22-branch-feedback.yml` (configuration_or_declaration)
- `.github/workflows/r23-verify.yml` (configuration_or_declaration)
- `.github/workflows/r24-verify.yml` (configuration_or_declaration)

## cmd/orgctl

Tracked artifacts: 50; kinds: go_source=35, test_only_signal=15

Production Go paths:
- `cmd/orgctl/agents.go`
- `cmd/orgctl/authorization.go`
- `cmd/orgctl/budget.go`
- `cmd/orgctl/completion.go`
- `cmd/orgctl/context.go`
- `cmd/orgctl/contextcompiler_cli.go`
- `cmd/orgctl/corpus.go`
- `cmd/orgctl/corpus_cluster_cli.go`
- `cmd/orgctl/corpus_enrich_cli.go`
- `cmd/orgctl/corpus_semantic_cli.go`
- `cmd/orgctl/corpuscuration_dedup_cli.go`
- `cmd/orgctl/cost.go`
- `cmd/orgctl/curation_cli.go`
- `cmd/orgctl/decision.go`
- `cmd/orgctl/evaluation.go`
- `cmd/orgctl/executive.go`
- `cmd/orgctl/executive_entry.go`
- `cmd/orgctl/executive_smoke_cli.go`
- `cmd/orgctl/improvement.go`
- `cmd/orgctl/main.go`
- `cmd/orgctl/memory.go`
- `cmd/orgctl/model_assignment.go`
- `cmd/orgctl/model_egress.go`
- `cmd/orgctl/model_identity.go`
- `cmd/orgctl/model_principal.go`
- `cmd/orgctl/models.go`
- `cmd/orgctl/objectstorage.go`
- `cmd/orgctl/postrun.go`
- `cmd/orgctl/rag.go`
- `cmd/orgctl/shadow.go`
- `cmd/orgctl/skill.go`
- `cmd/orgctl/sleep.go`
- `cmd/orgctl/staging.go`
- `cmd/orgctl/tasks.go`
- `cmd/orgctl/worker.go`

Function/method locators (mechanical index, not positive decisions):
- `cmd/orgctl/agents.go:15` — `runAgents`
- `cmd/orgctl/agents.go:46` — `runAgentsTree`
- `cmd/orgctl/agents.go:132` — `printAgentsUsage`
- `cmd/orgctl/authorization.go:15` — `runAuthorization`
- `cmd/orgctl/authorization.go:51` — `openAuthorizationRuntime`
- `cmd/orgctl/authorization.go:84` — `authorizationEvaluate`
- `cmd/orgctl/authorization.go:122` — `authorizationRequest`
- `cmd/orgctl/authorization.go:147` — `authorizationGet`
- `cmd/orgctl/authorization.go:165` — `authorizationList`
- `cmd/orgctl/authorization.go:184` — `authorizationDecide`
- `cmd/orgctl/authorization.go:209` — `authorizationConsume`
- `cmd/orgctl/authorization.go:233` — `authorizationCancel`
- `cmd/orgctl/authorization.go:255` — `authorizationExpire`
- `cmd/orgctl/authorization.go:271` — `authorizationError`
- `cmd/orgctl/authorization.go:295` — `printAuthorizationUsage`
- `cmd/orgctl/budget.go:19` — `runBudget`
- `cmd/orgctl/budget.go:40` — `runBudgetStatus`
- `cmd/orgctl/budget.go:93` — `runBudgetSetPrice`
- `cmd/orgctl/budget.go:168` — `runBudgetSetBalance`
- `cmd/orgctl/budget.go:227` — `runBudgetCreateRoot`
- `cmd/orgctl/budget.go:293` — `printBudgetUsage`
- `cmd/orgctl/completion.go:14` — `printCompletionUsage`
- `cmd/orgctl/completion.go:24` — `runCompletion`
- `cmd/orgctl/completion.go:70` — `completionVerify`
- `cmd/orgctl/context.go:21` — `runContext`
- `cmd/orgctl/context.go:56` — `openContextRuntime`
- `cmd/orgctl/context.go:101` — `contextValidateSource`
- `cmd/orgctl/context.go:177` — `contextBuild`
- `cmd/orgctl/context.go:209` — `contextGet`
- `cmd/orgctl/context.go:233` — `contextList`
- `cmd/orgctl/context.go:255` — `contextRender`
- `cmd/orgctl/context.go:290` — `contextValidate`
- `cmd/orgctl/context.go:310` — `contextInvalidate`
- `cmd/orgctl/context.go:333` — `contextError`
- `cmd/orgctl/context.go:351` — `redactedSegments`
- `cmd/orgctl/context.go:362` — `(s *stringListFlag).String`
- `cmd/orgctl/context.go:363` — `(s *stringListFlag).Set`
- `cmd/orgctl/context.go:372` — `printContextUsage`
- `cmd/orgctl/contextcompiler_cli.go:21` — `runContextCompiler`
- `cmd/orgctl/contextcompiler_cli.go:117` — `reductionPct`
- `cmd/orgctl/contextcompiler_cli.go:158` — `runProviderRenderShadow`
- `cmd/orgctl/corpus.go:23` — `runCorpus`
- `cmd/orgctl/corpus.go:49` — `printCorpusUsage`
- `cmd/orgctl/corpus.go:63` — `runCorpusCensus`
- `cmd/orgctl/corpus_cluster_cli.go:23` — `runCorpusCluster`
- `cmd/orgctl/corpus_enrich_cli.go:26` — `runCorpusEnrichAbstracts`
- `cmd/orgctl/corpus_semantic_cli.go:35` — `runCorpusEmbed`
- `cmd/orgctl/corpus_semantic_cli.go:108` — `runCorpusClusterSemantic`
- `cmd/orgctl/corpuscuration_dedup_cli.go:33` — `runCorpusCurationDedup`
- `cmd/orgctl/cost.go:15` — `runCost`
- `cmd/orgctl/cost.go:34` — `runCostCalls`
- `cmd/orgctl/cost.go:84` — `runCostEvents`
- `cmd/orgctl/cost.go:133` — `writeCostCalls`
- `cmd/orgctl/cost.go:153` — `runCostSummary`
- `cmd/orgctl/cost.go:202` — `printCostUsage`
- `cmd/orgctl/curation_cli.go:22` — `runCuration`
- `cmd/orgctl/curation_cli.go:61` — `runCurationValidateOutput`
- `cmd/orgctl/curation_cli.go:120` — `runCurationValidateAdjudication`
- `cmd/orgctl/curation_cli.go:186` — `runCurationValidateExternalImport`
- `cmd/orgctl/decision.go:120` — `runDecision`
- `cmd/orgctl/decision.go:361` — `parseDecisionFile`
- `cmd/orgctl/decision.go:389` — `parseDecisionRunID`
- `cmd/orgctl/decision.go:404` — `decisionCommandError`
- `cmd/orgctl/decision.go:430` — `printDecisionUsage`
- `cmd/orgctl/evaluation.go:24` — `printEvaluationUsage`
- `cmd/orgctl/evaluation.go:38` — `evaluationRunners`
- `cmd/orgctl/evaluation.go:50` — `runEvaluation`
- `cmd/orgctl/evaluation.go:74` — `fixturesForSuite`
- `cmd/orgctl/evaluation.go:99` — `evaluationSeed`
- `cmd/orgctl/evaluation.go:139` — `evaluationRun`
- `cmd/orgctl/evaluation.go:270` — `evaluationReport`
- `cmd/orgctl/evaluation.go:332` — `evaluationCompare`
- `cmd/orgctl/evaluation.go:475` — `boolPtrString`
- `cmd/orgctl/executive.go:25` — `runExecutive`
- `cmd/orgctl/executive.go:53` — `runExecutiveSubmit`
- `cmd/orgctl/executive.go:85` — `runExecutiveStatus`
- `cmd/orgctl/executive.go:113` — `runExecutiveResume`
- `cmd/orgctl/executive.go:141` — `runExecutiveWorker`
- `cmd/orgctl/executive.go:186` — `readExecutiveGoal`
- `cmd/orgctl/executive.go:208` — `openExecutiveRuntime`
- `cmd/orgctl/executive.go:230` — `openExecutiveDatabase`
- `cmd/orgctl/executive.go:261` — `executiveExitCode`
- `cmd/orgctl/executive.go:276` — `writeExecutiveValue`
- `cmd/orgctl/executive.go:305` — `runExecutiveReconcileGating`
- `cmd/orgctl/executive.go:329` — `printExecutiveUsage`
- `cmd/orgctl/executive_entry.go:10` — `init`
- `cmd/orgctl/executive_smoke_cli.go:29` — `runExecutiveSmoke`
- `cmd/orgctl/executive_smoke_cli.go:75` — `errString`
- `cmd/orgctl/improvement.go:65` — `(c improvementComparisonInput).comparison`
- `cmd/orgctl/improvement.go:117` — `(g *cliApprovalGate).AuthorizePromotion`
- `cmd/orgctl/improvement.go:128` — `(nonApprovingGate).AuthorizePromotion`
- `cmd/orgctl/improvement.go:132` — `runImprovement`
- `cmd/orgctl/improvement.go:250` — `runImprovementTransition`
- `cmd/orgctl/improvement.go:280` — `runImprovementVerdict`
- `cmd/orgctl/improvement.go:302` — `runImprovementRollback`
- `cmd/orgctl/improvement.go:326` — `runImprovementPromotion`
- `cmd/orgctl/improvement.go:372` — `parseImprovementFile`
- `cmd/orgctl/improvement.go:400` — `parseImprovementCandidateID`
- `cmd/orgctl/improvement.go:416` — `parseImprovementRunID`
- `cmd/orgctl/improvement.go:431` — `improvementCommandError`
- `cmd/orgctl/improvement.go:464` — `printImprovementUsage`
- `cmd/orgctl/main.go:46` — `main`
- `cmd/orgctl/main.go:55` — `run`
- `cmd/orgctl/main.go:126` — `runMigrate`
- `cmd/orgctl/main.go:170` — `runRegistry`
- `cmd/orgctl/main.go:236` — `registryValidate`
- `cmd/orgctl/main.go:265` — `executeRegistryCommand`
- `cmd/orgctl/main.go:412` — `parseJSONOnly`
- `cmd/orgctl/main.go:429` — `openDatabase`
- `cmd/orgctl/main.go:449` — `writeValue`
- `cmd/orgctl/main.go:475` — `printWarnings`
- `cmd/orgctl/main.go:480` — `registryError`
- `cmd/orgctl/main.go:499` — `encodeError`
- `cmd/orgctl/main.go:504` — `runHealth`
- `cmd/orgctl/main.go:542` — `printUsage`
- `cmd/orgctl/main.go:583` — `printRegistryUsage`
- `cmd/orgctl/memory.go:61` — `runMemory`
- `cmd/orgctl/memory.go:236` — `parseMemoryFile`
- `cmd/orgctl/memory.go:261` — `decodeMemoryStrict`
- `cmd/orgctl/memory.go:276` — `memoryCommandError`
- `cmd/orgctl/memory.go:291` — `printMemoryUsage`
- `cmd/orgctl/model_assignment.go:14` — `modelAssignment`
- `cmd/orgctl/model_assignment.go:134` — `printModelAssignmentUsage`
- `cmd/orgctl/model_egress.go:17` — `openModelEgressRuntime`
- `cmd/orgctl/model_egress.go:55` — `modelEgress`
- `cmd/orgctl/model_egress.go:117` — `modelEgressError`
- `cmd/orgctl/model_identity.go:27` — `decodeIdentityKeyCommand`
- `cmd/orgctl/model_identity.go:49` — `modelIdentity`
- `cmd/orgctl/model_identity.go:66` — `modelIdentityPolicy`
- `cmd/orgctl/model_identity.go:128` — `modelIdentityKey`
- `cmd/orgctl/model_identity.go:231` — `printModelIdentityUsage`
- `cmd/orgctl/model_principal.go:17` — `modelPrincipalCommandFromJSON`
- `cmd/orgctl/model_principal.go:31` — `modelPrincipal`
- `cmd/orgctl/model_principal.go:130` — `printModelPrincipalUsage`
- `cmd/orgctl/models.go:21` — `runModel`
- `cmd/orgctl/models.go:90` — `openModelRegistryRuntime`
- `cmd/orgctl/models.go:123` — `openModelRuntime`
- `cmd/orgctl/models.go:156` — `modelRegistry`
- `cmd/orgctl/models.go:219` — `modelInvocation`
- `cmd/orgctl/models.go:354` — `parseModelJSON`
- `cmd/orgctl/models.go:370` — `modelError`
- `cmd/orgctl/models.go:387` — `printModelUsage`
- `cmd/orgctl/objectstorage.go:20` — `openObjectStorageClient`
- `cmd/orgctl/objectstorage.go:38` — `newObjectStorageClient`
- `cmd/orgctl/objectstorage.go:53` — `runObjectStorage`
- `cmd/orgctl/objectstorage.go:196` — `objectStorageSeed`
- `cmd/orgctl/objectstorage.go:267` — `guessContentType`
- `cmd/orgctl/postrun.go:20` — `runPostrun`
- `cmd/orgctl/postrun.go:121` — `printPostrunUsage`
- `cmd/orgctl/rag.go:140` — `runRAGIngestPDF`
- `cmd/orgctl/rag.go:289` — `runRAG`
- `cmd/orgctl/rag.go:515` — `parseRAGFile`
- `cmd/orgctl/rag.go:540` — `decodeRAGStrict`
- `cmd/orgctl/rag.go:555` — `ragCommandError`
- `cmd/orgctl/rag.go:570` — `printRAGUsage`
- `cmd/orgctl/shadow.go:30` — `printShadowUsage`
- `cmd/orgctl/shadow.go:44` — `runShadow`
- `cmd/orgctl/shadow.go:125` — `newShadowService`
- `cmd/orgctl/shadow.go:129` — `shadowVerify`
- `cmd/orgctl/shadow.go:150` — `shadowReplay`
- `cmd/orgctl/shadow.go:170` — `shadowFinishRun`
- `cmd/orgctl/shadow.go:188` — `shadowReport`
- `cmd/orgctl/shadow.go:220` — `shadowReportError`
- `cmd/orgctl/shadow.go:243` — `(g *shadowGround).RoleExists`
- `cmd/orgctl/shadow.go:254` — `(g *shadowGround).DepartmentExists`
- `cmd/orgctl/shadow.go:265` — `(g *shadowGround).LeaderOf`
- `cmd/orgctl/shadow.go:276` — `(g *shadowGround).EvaluateCapability`
- `cmd/orgctl/shadow.go:309` — `(g *shadowGround).CanonicalReportingClosed`
- `cmd/orgctl/skill.go:93` — `runSkill`
- `cmd/orgctl/skill.go:310` — `skillLifecycleMutation`
- `cmd/orgctl/skill.go:314` — `parseSkillFile`
- `cmd/orgctl/skill.go:339` — `decodeSkillStrict`
- `cmd/orgctl/skill.go:354` — `skillCommandError`
- `cmd/orgctl/skill.go:369` — `printSkillUsage`
- `cmd/orgctl/sleep.go:17` — `runSleep`
- `cmd/orgctl/sleep.go:84` — `printSleepUsage`
- `cmd/orgctl/staging.go:17` — `runStaging`
- `cmd/orgctl/staging.go:59` — `stagingRepo`
- `cmd/orgctl/staging.go:97` — `stagingWorkspace`
- `cmd/orgctl/staging.go:214` — `stagingCheck`
- `cmd/orgctl/staging.go:239` — `stagingPromotion`
- `cmd/orgctl/staging.go:329` — `openStagingRuntime`
- `cmd/orgctl/staging.go:361` — `readStagingToken`
- `cmd/orgctl/staging.go:367` — `stagingError`
- `cmd/orgctl/staging.go:401` — `redactStagingError`
- `cmd/orgctl/staging.go:414` — `printStagingUsage`
- `cmd/orgctl/tasks.go:20` — `runTask`
- `cmd/orgctl/tasks.go:81` — `runOutbox`
- `cmd/orgctl/tasks.go:171` — `openTaskService`
- `cmd/orgctl/tasks.go:230` — `taskCreate`
- `cmd/orgctl/tasks.go:258` — `taskGet`
- `cmd/orgctl/tasks.go:276` — `taskList`
- `cmd/orgctl/tasks.go:302` — `taskClaim`
- `cmd/orgctl/tasks.go:321` — `taskClaimSpecific`
- `cmd/orgctl/tasks.go:348` — `taskStart`
- `cmd/orgctl/tasks.go:375` — `taskHeartbeat`
- `cmd/orgctl/tasks.go:403` — `taskResult`
- `cmd/orgctl/tasks.go:442` — `taskFinalize`
- `cmd/orgctl/tasks.go:467` — `taskBlock`
- `cmd/orgctl/tasks.go:492` — `taskUnblock`
- `cmd/orgctl/tasks.go:514` — `taskCancel`
- `cmd/orgctl/tasks.go:538` — `taskDependencyAdd`
- `cmd/orgctl/tasks.go:563` — `taskRequirementAdd`
- `cmd/orgctl/tasks.go:596` — `taskEvidenceAdd`
- `cmd/orgctl/tasks.go:626` — `taskEvents`
- `cmd/orgctl/tasks.go:647` — `taskAttempts`
- `cmd/orgctl/tasks.go:665` — `taskReconcile`
- `cmd/orgctl/tasks.go:681` — `taskDeadList`
- `cmd/orgctl/tasks.go:697` — `taskDeadShow`
- `cmd/orgctl/tasks.go:715` — `normalizeTaskSubcommand`
- `cmd/orgctl/tasks.go:733` — `taskError`
- `cmd/orgctl/tasks.go:757` — `parseInterspersed`
- `cmd/orgctl/tasks.go:791` — `inputReader`
- `cmd/orgctl/tasks.go:802` — `readSecretToken`
- `cmd/orgctl/tasks.go:817` — `positiveID`
- `cmd/orgctl/tasks.go:825` — `parseStatuses`
- `cmd/orgctl/tasks.go:841` — `printTaskUsage`
- `cmd/orgctl/tasks.go:865` — `printOutboxUsage`
- `cmd/orgctl/worker.go:26` — `runModelWorker`
- `cmd/orgctl/worker.go:87` — `(o *stderrObserver).OnListError`
- `cmd/orgctl/worker.go:93` — `(o *stderrObserver).OnDispatchError`
- `cmd/orgctl/worker.go:99` — `openModelWorkerRuntime`

## cmd/orgd

Tracked artifacts: 1; kinds: go_source=1

Production Go paths:
- `cmd/orgd/main.go`

Function/method locators (mechanical index, not positive decisions):
- `cmd/orgd/main.go:24` — `main`
- `cmd/orgd/main.go:31` — `run`

## cmd/question-identity-gate

Tracked artifacts: 2; kinds: go_source=1, test_only_signal=1

Production Go paths:
- `cmd/question-identity-gate/main.go`

Function/method locators (mechanical index, not positive decisions):
- `cmd/question-identity-gate/main.go:14` — `main`
- `cmd/question-identity-gate/main.go:21` — `run`

## config

Tracked artifacts: 1; kinds: configuration_or_declaration=1

Non-Go artifacts:
- `config/repositories.example.yaml` (configuration_or_declaration)

## deployments

Tracked artifacts: 10; kinds: deployment_or_build_infrastructure=2, documentation_or_data=3, supporting_script_signal=5

Non-Go artifacts:
- `deployments/bgem3/RUNBOOK-deploy-sidecar.md` (documentation_or_data)
- `deployments/postgres/RUNBOOK-enable-pgvector.md` (documentation_or_data)
- `deployments/postgres/RUNBOOK-restore-against-existing-database.md` (documentation_or_data)
- `deployments/postgres/init/000-enable-pgvector.sh` (supporting_script_signal)
- `deployments/postgres/init/001-create-app-user.sh` (supporting_script_signal)
- `deployments/postgres/init/002-integration-app-superuser.sh` (supporting_script_signal)
- `deployments/postgres/reassign-owner.sql` (deployment_or_build_infrastructure)
- `deployments/postgres/verify-restore-ownership.sh` (supporting_script_signal)
- `deployments/repairs/RAG-INTEGRITY-001-repair-16-canonical-hashes.sql` (deployment_or_build_infrastructure)
- `deployments/verify-deployment-topology.sh` (supporting_script_signal)

## docs

Tracked artifacts: 114; kinds: configuration_or_declaration=31, documentation_or_data=83

Non-Go artifacts:
- `docs/INTEGRATION_EVIDENCE.md` (documentation_or_data)
- `docs/adr/ADR-0001-modular-kernel.md` (documentation_or_data)
- `docs/adr/ADR-0002-postgres-durable-engine.md` (documentation_or_data)
- `docs/adr/ADR-0003-logic-shadow-mode.md` (documentation_or_data)
- `docs/adr/ADR-0004-independent-cells.md` (documentation_or_data)
- `docs/adr/ADR-0005-staging-before-promotion.md` (documentation_or_data)
- `docs/adr/ADR-0006-hybrid-logic-ir-shadow.md` (documentation_or_data)
- `docs/canonical-single-provider-test/architecture-characteristics.yaml` (configuration_or_declaration)
- `docs/canonical-single-provider-test/capability-matrix.yaml` (configuration_or_declaration)
- `docs/canonical-single-provider-test/cell-boundaries.yaml` (configuration_or_declaration)
- `docs/canonical-single-provider-test/decisions-required.yaml` (configuration_or_declaration)
- `docs/canonical-single-provider-test/instruction-precedence.yaml` (configuration_or_declaration)
- `docs/canonical-single-provider-test/leader-worker-map.yaml` (configuration_or_declaration)
- `docs/canonical-single-provider-test/memory-policy.yaml` (configuration_or_declaration)
- `docs/canonical-single-provider-test/model-egress-policy.yaml` (configuration_or_declaration)
- `docs/canonical-single-provider-test/model-execution-identity-policy.yaml` (configuration_or_declaration)
- `docs/canonical-single-provider-test/model-routing.yaml` (configuration_or_declaration)
- `docs/canonical-single-provider-test/organization.yaml` (configuration_or_declaration)
- `docs/canonical-single-provider-test/reasoning-assurance.yaml` (configuration_or_declaration)
- `docs/canonical-single-provider-test/role-catalog.yaml` (configuration_or_declaration)
- `docs/canonical-single-provider-test/source-manifest.yaml` (configuration_or_declaration)
- `docs/canonical/architecture-characteristics.yaml` (configuration_or_declaration)
- `docs/canonical/capability-matrix.yaml` (configuration_or_declaration)
- `docs/canonical/cell-boundaries.yaml` (configuration_or_declaration)
- `docs/canonical/decisions-required.yaml` (configuration_or_declaration)
- `docs/canonical/instruction-precedence.yaml` (configuration_or_declaration)
- `docs/canonical/instrument-v4-controller-binding-001.provenance.yaml` (configuration_or_declaration)
- `docs/canonical/leader-worker-map.yaml` (configuration_or_declaration)
- `docs/canonical/memory-policy.yaml` (configuration_or_declaration)
- `docs/canonical/model-egress-policy.yaml` (configuration_or_declaration)
- `docs/canonical/model-execution-identity-policy.yaml` (configuration_or_declaration)
- `docs/canonical/model-routing.yaml` (configuration_or_declaration)
- `docs/canonical/organization.yaml` (configuration_or_declaration)
- `docs/canonical/q3-002-campaign-binding-v1.json` (configuration_or_declaration)
- `docs/canonical/q3-organizational-capability-ontology-v1.md` (documentation_or_data)
- `docs/canonical/q3-organizational-capability-ontology-v1.provenance.yaml` (configuration_or_declaration)
- `docs/canonical/reasoning-assurance.yaml` (configuration_or_declaration)
- `docs/canonical/role-catalog.yaml` (configuration_or_declaration)
- `docs/canonical/source-manifest.yaml` (configuration_or_declaration)
- `docs/implementation/BRANCH_CONTEXT.md` (documentation_or_data)
- `docs/implementation/branch-01-bootstrap/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-02-postgres-storage/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-03-organization-registry/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-04-durable-task-engine/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-05-staging-promotion-engine/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-06-capability-policy-engine/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-06-capability-policy-engine/VALIDATION.md` (documentation_or_data)
- `docs/implementation/branch-07-context-engine/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-08-model-runtime-gateway/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-09-model-egress-authorization/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-10-model-dispatcher-assignments/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-11-model-execution-identity/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-12-model-provider-adapter/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-13-persistent-cell-worker/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-14-durable-decision-graph/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-15-bounded-self-improvement/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-16-task-completion-verifier/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-18-organizational-memory/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-19-skill-registry-lifecycle/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-20-approved-knowledge-rag/DESIGN.md` (documentation_or_data)
- `docs/implementation/branch-20-approved-knowledge-rag/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-21-alibaba-claude-cli-adapter/DESIGN.md` (documentation_or_data)
- `docs/implementation/branch-21-alibaba-claude-cli-adapter/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-21-alibaba-claude-cli-adapter/VPS-SMOKE.md` (documentation_or_data)
- `docs/implementation/branch-23-executive-evidence-recovery-layer/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-24-executive-scoped-egress/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-25-executive-decision-trace-wiring/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-26-executive-closure-lesson-job/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-27-organizational-sleep-consolidation/INTEGRATION.md` (documentation_or_data)
- `docs/implementation/branch-29-embedding-retrieval/DESIGN.md` (documentation_or_data)
- `docs/implementation/branch-30-canary-evaluation-bge-m3/DESIGN.md` (documentation_or_data)
- `docs/implementation/branch-30-canary-evaluation-bge-m3/HANDOFF.md` (documentation_or_data)
- `docs/implementation/branch-31-token-context-governance/DESIGN.md` (documentation_or_data)
- `docs/implementation/branch-31-token-context-governance/EVIDENCE.md` (documentation_or_data)
- `docs/implementation/branch-31-token-context-governance/HANDOFF.md` (documentation_or_data)
- `docs/implementation/grok-audit-baseline-001/HANDOFF.md` (documentation_or_data)
- `docs/implementation/rag-knowledge-integrity-hardening-v1/DESIGN.md` (documentation_or_data)
- `docs/implementation/rag-knowledge-integrity-hardening-v1/HANDOFF.md` (documentation_or_data)
- `docs/implementation/rag-knowledge-integrity-hardening-v1/TEST-EVIDENCE.md` (documentation_or_data)
- `docs/implementation/security-agent-communication-hardening-v1/DESIGN.md` (documentation_or_data)
- `docs/implementation/security-agent-communication-hardening-v1/HANDOFF.md` (documentation_or_data)
- `docs/reports/CONTEXT_ECONOMY_R9_BASELINE.md` (documentation_or_data)
- `docs/reports/CURATION_CANARY_REPORT.md` (documentation_or_data)
- `docs/reports/DEEPSEEK_VS_MIMO_CURATION_CANARY.md` (documentation_or_data)
- `docs/reports/DEEPSEEK_VS_MIMO_QUALITY_AUDIT.md` (documentation_or_data)
- `docs/reports/MIMO_V25_INTEGRATION_AUDIT.md` (documentation_or_data)
- `docs/reports/MIMO_V25_SMOKE_REPORT.md` (documentation_or_data)
- `docs/reports/P0_FIX_AUDIT.md` (documentation_or_data)
- `docs/reports/P0_FIX_EVIDENCE.md` (documentation_or_data)
- `docs/reports/R10_2_FINAL_VERDICT.md` (documentation_or_data)
- `docs/reports/R10_3_MIMO_CREDIT_CALIBRATION.md` (documentation_or_data)
- `docs/reports/R10_3_NEGATIVE_TIER_ADJUDICATION.md` (documentation_or_data)
- `docs/reports/R10_3_OUTPUT_BUDGET_CALIBRATION.md` (documentation_or_data)
- `docs/reports/R10_3_PRODUCTION_READINESS_VERDICT.md` (documentation_or_data)
- `docs/reports/R10_4_1_DEEPSEEK_FULL_SILVER_PROJECTION.md` (documentation_or_data)
- `docs/reports/R10_4_1_DEEPSEEK_SENTINEL_RECHECK.md` (documentation_or_data)
- `docs/reports/R10_4_1_FINAL_VERDICT.md` (documentation_or_data)
- `docs/reports/R10_4_DEEPSEEK_CACHE_CANARY.md` (documentation_or_data)
- `docs/reports/R10_4_FINAL_VERDICT.md` (documentation_or_data)
- `docs/reports/R10_4_PROVIDER_RENDER_AUDIT.md` (documentation_or_data)
- `docs/reports/R10_4_SHADOW_DETERMINISM_REPORT.md` (documentation_or_data)
- `docs/reports/R10_CANARY_REPORT.md` (documentation_or_data)
- `docs/reports/R10_CONTEXT_SHADOW_REPORT.md` (documentation_or_data)
- `docs/reports/R10_DESIGN_AUDIT.md` (documentation_or_data)
- `docs/reports/R9_CANARY_REPORT.md` (documentation_or_data)
- `docs/reports/R9_VS_R10_COMPARISON.md` (documentation_or_data)
- `docs/reports/instrument-v4-controller-binding-001.md` (documentation_or_data)
- `docs/reports/organization-redesign-001-boundary-repair-001.md` (documentation_or_data)
- `docs/reports/organization-redesign-001-evidence-manifest.md` (documentation_or_data)
- `docs/reports/organization-redesign-001-rerun-001.md` (documentation_or_data)
- `docs/reports/organization-redesign-001-rerun-002.md` (documentation_or_data)
- `docs/reports/q3-002-campaign-binding-repair-001.md` (documentation_or_data)
- `docs/reports/q3-operational-definition-001.md` (documentation_or_data)
- `docs/review/APPROVAL_CHECKLIST.md` (documentation_or_data)

## empresa

Tracked artifacts: 3; kinds: configuration_or_declaration=3

Non-Go artifacts:
- `empresa/AGENT.md` (configuration_or_declaration)
- `empresa/ceo/PERFIL.md` (configuration_or_declaration)
- `empresa/human/PERFIL.md` (configuration_or_declaration)

## ingenieria_ia

Tracked artifacts: 11; kinds: configuration_or_declaration=11

Non-Go artifacts:
- `ingenieria_ia/AGENT.md` (configuration_or_declaration)
- `ingenieria_ia/arquitecto_software/PERFIL.md` (configuration_or_declaration)
- `ingenieria_ia/ciberseguridad/PERFIL.md` (configuration_or_declaration)
- `ingenieria_ia/code-runner/PERFIL.md` (configuration_or_declaration)
- `ingenieria_ia/data_engineer/PERFIL.md` (configuration_or_declaration)
- `ingenieria_ia/frontend/PERFIL.md` (configuration_or_declaration)
- `ingenieria_ia/ingeniero_ia/PERFIL.md` (configuration_or_declaration)
- `ingenieria_ia/ml_data_scientist/PERFIL.md` (configuration_or_declaration)
- `ingenieria_ia/orquestador/PERFIL.md` (configuration_or_declaration)
- `ingenieria_ia/qa/PERFIL.md` (configuration_or_declaration)
- `ingenieria_ia/semantic_engineer/PERFIL.md` (configuration_or_declaration)

## internal/agentbudget

Tracked artifacts: 7; kinds: go_source=5, test_only_signal=2

Production Go paths:
- `internal/agentbudget/doc.go`
- `internal/agentbudget/errors.go`
- `internal/agentbudget/ports.go`
- `internal/agentbudget/postgres/store.go`
- `internal/agentbudget/types.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/agentbudget/postgres/store.go:19` — `New`
- `internal/agentbudget/postgres/store.go:32` — `scanBudget`
- `internal/agentbudget/postgres/store.go:46` — `(s *Store).GetBudget`
- `internal/agentbudget/postgres/store.go:57` — `(s *Store).CreateRootBudget`
- `internal/agentbudget/postgres/store.go:110` — `(s *Store).InheritForChild`
- `internal/agentbudget/postgres/store.go:243` — `(s *Store).ConsumeModelCall`
- `internal/agentbudget/postgres/store.go:286` — `(s *Store).ResolveBudgetForTask`
- `internal/agentbudget/types.go:23` — `(l Limits).Validate`
- `internal/agentbudget/types.go:35` — `DefaultLimits`
- `internal/agentbudget/types.go:65` — `Reserve`

## internal/agentmessaging

Tracked artifacts: 10; kinds: go_source=6, test_only_signal=4

Production Go paths:
- `internal/agentmessaging/doc.go`
- `internal/agentmessaging/errors.go`
- `internal/agentmessaging/ports.go`
- `internal/agentmessaging/postgres/store.go`
- `internal/agentmessaging/topology.go`
- `internal/agentmessaging/topologyfixture/fixture.go`
- `internal/agentmessaging/types.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/agentmessaging/postgres/store.go:39` — `New`
- `internal/agentmessaging/postgres/store.go:64` — `scanMessage`
- `internal/agentmessaging/postgres/store.go:78` — `(s *Store).Send`
- `internal/agentmessaging/postgres/store.go:194` — `(s *Store).validateExecutionPrincipalForSender`
- `internal/agentmessaging/postgres/store.go:228` — `(s *Store).validateTaskOwnership`
- `internal/agentmessaging/postgres/store.go:253` — `computeCanonicalRequestHash`
- `internal/agentmessaging/postgres/store.go:284` — `(s *Store).ClaimNext`
- `internal/agentmessaging/postgres/store.go:404` — `(s *Store).validateExecutionPrincipalForClaim`
- `internal/agentmessaging/postgres/store.go:433` — `(s *Store).Ack`
- `internal/agentmessaging/postgres/store.go:459` — `(s *Store).Nack`
- `internal/agentmessaging/postgres/store.go:489` — `verifyClaimWithPrincipal`
- `internal/agentmessaging/postgres/store.go:545` — `newToken`
- `internal/agentmessaging/postgres/store.go:554` — `hashToken`
- `internal/agentmessaging/postgres/store.go:559` — `nullableString`
- `internal/agentmessaging/topology.go:26` — `NewTopologyValidator`
- `internal/agentmessaging/topology.go:32` — `(v *TopologyValidator).ValidateEdge`
- `internal/agentmessaging/topology.go:140` — `extractUnitFromRole`
- `internal/agentmessaging/topology.go:149` — `(v *TopologyValidator).senderUnitHasWorker`
- `internal/agentmessaging/types.go:36` — `(k MessageType).Valid`
- `internal/agentmessaging/types.go:105` — `(c SendCommand).Validate`
- `internal/agentmessaging/types.go:180` — `rejectDuplicateJSONKeys`
- `internal/agentmessaging/types.go:239` — `(c SendCommand).validateSemanticInvariants`

## internal/agentmessagingfixtures

Tracked artifacts: 4; kinds: test_only_signal=4

Production Go paths:
- `internal/agentmessagingfixtures/activate.go`
- `internal/agentmessagingfixtures/runner.go`

## internal/app

Tracked artifacts: 2; kinds: go_source=1, test_only_signal=1

Production Go paths:
- `internal/app/app.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/app/app.go:67` — `New`
- `internal/app/app.go:125` — `newWithDependencies`
- `internal/app/app.go:138` — `(a *App).Run`
- `internal/app/app.go:199` — `(a *App).prepareDatabase`
- `internal/app/app.go:253` — `(a *App).runTaskReconciler`
- `internal/app/app.go:268` — `(a *App).reconcileTasksOnce`
- `internal/app/app.go:292` — `(a *App).runStagingReconciler`
- `internal/app/app.go:307` — `(a *App).reconcileStagingOnce`
- `internal/app/app.go:325` — `(a *App).ensureSchema`
- `internal/app/app.go:346` — `(a *App).Ready`
- `internal/app/app.go:376` — `(a *App).Addr`

## internal/authorization

Tracked artifacts: 19; kinds: go_source=13, test_only_signal=6

Production Go paths:
- `internal/authorization/authorizer.go`
- `internal/authorization/bootstrap/bootstrap.go`
- `internal/authorization/domain.go`
- `internal/authorization/errors.go`
- `internal/authorization/hash.go`
- `internal/authorization/interfaces.go`
- `internal/authorization/postgres/helpers.go`
- `internal/authorization/postgres/requests.go`
- `internal/authorization/postgres/store.go`
- `internal/authorization/postgres/transitions.go`
- `internal/authorization/service.go`
- `internal/authorization/state_machine.go`
- `internal/authorization/validation.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/authorization/authorizer.go:35` — `(r registryPolicyReader).GetOrganization`
- `internal/authorization/authorizer.go:38` — `(r registryPolicyReader).GetCurrentRevision`
- `internal/authorization/authorizer.go:41` — `(r registryPolicyReader).GetAuthorizationRole`
- `internal/authorization/authorizer.go:54` — `New`
- `internal/authorization/authorizer.go:61` — `NewWithPolicyReader`
- `internal/authorization/authorizer.go:100` — `newAuthorizer`
- `internal/authorization/authorizer.go:155` — `(a *Authorizer).Evaluate`
- `internal/authorization/authorizer.go:165` — `(a *Authorizer).evaluate`
- `internal/authorization/authorizer.go:252` — `(a *Authorizer).Authorize`
- `internal/authorization/authorizer.go:270` — `legacyEvaluationError`
- `internal/authorization/authorizer.go:292` — `(a *Authorizer).MatrixHash`
- `internal/authorization/authorizer.go:294` — `(a *Authorizer).KnownCapabilities`
- `internal/authorization/authorizer.go:303` — `(a *Authorizer).Capability`
- `internal/authorization/authorizer.go:308` — `(a *Authorizer).globalHardDenied`
- `internal/authorization/authorizer.go:312` — `(a *Authorizer).authorityHardDenied`
- `internal/authorization/authorizer.go:316` — `(a *Authorizer).authorityKnown`
- `internal/authorization/authorizer.go:321` — `(a *Authorizer).hardDenied`
- `internal/authorization/authorizer.go:325` — `contains`
- `internal/authorization/bootstrap/bootstrap.go:21` — `Open`
- `internal/authorization/domain.go:73` — `(s RequestStatus).Valid`
- `internal/authorization/domain.go:82` — `(s RequestStatus).Terminal`
- `internal/authorization/domain.go:98` — `(d Decision).Valid`
- `internal/authorization/hash.go:11` — `RequestHash`
- `internal/authorization/interfaces.go:34` — `(f ClockFunc).Now`
- `internal/authorization/postgres/helpers.go:19` — `scanRequest`
- `internal/authorization/postgres/helpers.go:28` — `scanUse`
- `internal/authorization/postgres/helpers.go:34` — `transitionUpdateError`
- `internal/authorization/postgres/helpers.go:41` — `rollback`
- `internal/authorization/postgres/helpers.go:47` — `appendEvent`
- `internal/authorization/postgres/helpers.go:112` — `getOrganizationTx`
- `internal/authorization/postgres/helpers.go:123` — `getRoleTx`
- `internal/authorization/postgres/helpers.go:127` — `getRevisionTx`
- `internal/authorization/postgres/helpers.go:131` — `lockRequest`
- `internal/authorization/postgres/helpers.go:135` — `eventExists`
- `internal/authorization/postgres/helpers.go:141` — `withEventCorrelation`
- `internal/authorization/postgres/helpers.go:151` — `authorizationErrorForReason`
- `internal/authorization/postgres/requests.go:14` — `(s *Store).CreateRequest`
- `internal/authorization/postgres/requests.go:100` — `(s *Store).GetRequest`
- `internal/authorization/postgres/requests.go:104` — `(s *Store).ListRequests`
- `internal/authorization/postgres/requests.go:137` — `(s *Store).DecideRequest`
- `internal/authorization/postgres/requests.go:220` — `transitionExpired`
- `internal/authorization/postgres/store.go:23` — `New`
- `internal/authorization/postgres/store.go:30` — `(s *Store).Pool`
- `internal/authorization/postgres/store.go:39` — `mapError`
- `internal/authorization/postgres/store.go:70` — `(s *Store).GetOrganization`
- `internal/authorization/postgres/store.go:81` — `(s *Store).GetCurrentRevision`
- `internal/authorization/postgres/store.go:85` — `getCurrentRevision`
- `internal/authorization/postgres/store.go:114` — `scanRole`
- `internal/authorization/postgres/store.go:123` — `(s *Store).GetAuthorizationRole`
- `internal/authorization/postgres/transitions.go:12` — `(s *Store).ConsumeApproval`
- `internal/authorization/postgres/transitions.go:137` — `(s *Store).CancelRequest`
- `internal/authorization/postgres/transitions.go:187` — `(s *Store).ExpireRequests`
- `internal/authorization/postgres/transitions.go:227` — `commandScopeMatches`
- `internal/authorization/service.go:26` — `NewService`
- `internal/authorization/service.go:39` — `(s *Service).Evaluate`
- `internal/authorization/service.go:84` — `(s *Service).RequestApproval`
- `internal/authorization/service.go:126` — `(s *Service).DecideRequest`
- `internal/authorization/service.go:142` — `(s *Service).validateDecision`
- `internal/authorization/service.go:181` — `(s *Service).ConsumeApproval`
- `internal/authorization/service.go:207` — `(s *Service).validateConsumption`
- `internal/authorization/service.go:270` — `(s *Service).CancelRequest`
- `internal/authorization/service.go:278` — `(s *Service).ExpireRequests`
- `internal/authorization/service.go:288` — `(s *Service).GetRequest`
- `internal/authorization/service.go:302` — `(s *Service).ListRequests`
- `internal/authorization/service.go:322` — `(s *Service).OrganizationID`
- `internal/authorization/service.go:324` — `(s *Service).CurrentRevision`
- `internal/authorization/service.go:335` — `reasonFromError`
- `internal/authorization/service.go:358` — `errorForReason`
- `internal/authorization/state_machine.go:3` — `CanTransition`
- `internal/authorization/validation.go:19` — `DigestAction`
- `internal/authorization/validation.go:24` — `ValidateDigest`
- `internal/authorization/validation.go:31` — `validateEvaluationRequest`
- `internal/authorization/validation.go:62` — `validateRequestCommand`
- `internal/authorization/validation.go:99` — `normalizeOptional`

## internal/cellworker

Tracked artifacts: 14; kinds: go_source=8, test_only_signal=6

Production Go paths:
- `internal/cellworker/backoff.go`
- `internal/cellworker/config.go`
- `internal/cellworker/env.go`
- `internal/cellworker/errors.go`
- `internal/cellworker/interfaces.go`
- `internal/cellworker/observer.go`
- `internal/cellworker/postgres/store.go`
- `internal/cellworker/worker.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/cellworker/backoff.go:19` — `newBackoff`
- `internal/cellworker/backoff.go:26` — `(b *backoff).Next`
- `internal/cellworker/backoff.go:48` — `(b *backoff).Reset`
- `internal/cellworker/config.go:36` — `(c Config).Validate`
- `internal/cellworker/env.go:25` — `LoadConfig`
- `internal/cellworker/env.go:56` — `envInt`
- `internal/cellworker/env.go:68` — `envDuration`
- `internal/cellworker/interfaces.go:38` — `(systemClock).Now`
- `internal/cellworker/interfaces.go:40` — `(systemClock).Sleep`
- `internal/cellworker/observer.go:19` — `(NoopObserver).OnListError`
- `internal/cellworker/observer.go:20` — `(NoopObserver).OnDispatchError`
- `internal/cellworker/postgres/store.go:23` — `New`
- `internal/cellworker/postgres/store.go:39` — `(s *Store).ListEligible`
- `internal/cellworker/worker.go:32` — `New`
- `internal/cellworker/worker.go:55` — `(w *Worker).Run`
- `internal/cellworker/worker.go:150` — `(w *Worker).dispatchWithGrace`

## internal/codeexecutionfixtures

Tracked artifacts: 6; kinds: test_only_signal=6

Production Go paths:
- `internal/codeexecutionfixtures/activate.go`
- `internal/codeexecutionfixtures/gobugfix.go`
- `internal/codeexecutionfixtures/postgresmigration.go`
- `internal/codeexecutionfixtures/runner.go`

## internal/completion

Tracked artifacts: 8; kinds: go_source=6, test_only_signal=2

Production Go paths:
- `internal/completion/errors.go`
- `internal/completion/fake.go`
- `internal/completion/ports.go`
- `internal/completion/postgres/store.go`
- `internal/completion/service.go`
- `internal/completion/types.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/completion/fake.go:15` — `(f *fakeTasks).TaskFacts`
- `internal/completion/fake.go:30` — `(f *fakeArtifacts).ArtifactDigest`
- `internal/completion/fake.go:42` — `(f *fakeChecks).CheckPassed`
- `internal/completion/fake.go:50` — `(f *fakeApprovals).ApprovalConsumed`
- `internal/completion/fake.go:63` — `(f *fakeBranches).CurrentBranchStateForAttempt`
- `internal/completion/ports.go:118` — `(f ClockFunc).Now`
- `internal/completion/postgres/store.go:26` — `New`
- `internal/completion/postgres/store.go:45` — `(s *Store).TaskFacts`
- `internal/completion/postgres/store.go:99` — `(s *Store).ArtifactDigest`
- `internal/completion/postgres/store.go:114` — `(s *Store).CheckPassed`
- `internal/completion/postgres/store.go:133` — `(s *Store).ApprovalConsumed`
- `internal/completion/postgres/store.go:155` — `(s *Store).CurrentBranchStateForAttempt`
- `internal/completion/service.go:19` — `NewService`
- `internal/completion/service.go:32` — `(s *Service).Verify`
- `internal/completion/service.go:71` — `(s *Service).checkRequirementsSatisfied`
- `internal/completion/service.go:85` — `(s *Service).checkArtifactExists`
- `internal/completion/service.go:105` — `(s *Service).checkChecksPassed`
- `internal/completion/service.go:119` — `(s *Service).checkApprovalPresent`
- `internal/completion/service.go:137` — `(s *Service).checkNoRejectedBranchReused`
- `internal/completion/service.go:155` — `aggregateVerdict`
- `internal/completion/service.go:171` — `itoa`
- `internal/completion/types.go:24` — `(l VerificationLabel).Valid`

## internal/config

Tracked artifacts: 2; kinds: go_source=1, test_only_signal=1

Production Go paths:
- `internal/config/config.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/config/config.go:204` — `Load`
- `internal/config/config.go:208` — `LoadFrom`
- `internal/config/config.go:302` — `loadDatabase`
- `internal/config/config.go:379` — `loadTasks`
- `internal/config/config.go:440` — `loadAuthorization`
- `internal/config/config.go:460` — `loadContext`
- `internal/config/config.go:500` — `loadStaging`
- `internal/config/config.go:545` — `(cfg Config).Validate`
- `internal/config/config.go:599` — `(cfg TaskConfig).Validate`
- `internal/config/config.go:624` — `(cfg AuthorizationConfig).Validate`
- `internal/config/config.go:640` — `(cfg ContextConfig).Validate`
- `internal/config/config.go:668` — `(cfg StagingConfig).Validate`
- `internal/config/config.go:708` — `(cfg DatabaseConfig).Validate`
- `internal/config/config.go:751` — `(cfg DatabaseConfig).ConnectionString`
- `internal/config/config.go:772` — `text`
- `internal/config/config.go:780` — `optionalText`
- `internal/config/config.go:788` — `duration`
- `internal/config/config.go:803` — `integer`
- `internal/config/config.go:818` — `integer64`
- `internal/config/config.go:833` — `boolean`
- `internal/config/config.go:845` — `logLevel`
- `internal/config/config.go:861` — `validateAddr`

## internal/contextcompiler

Tracked artifacts: 7; kinds: go_source=5, test_only_signal=2

Production Go paths:
- `internal/contextcompiler/contextcompiler_compiler.go`
- `internal/contextcompiler/contextcompiler_domain.go`
- `internal/contextcompiler/contextcompiler_output_budget.go`
- `internal/contextcompiler/contextcompiler_profiles.go`
- `internal/contextcompiler/contextcompiler_projections.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/contextcompiler/contextcompiler_compiler.go:23` — `TaskClassOf`
- `internal/contextcompiler/contextcompiler_compiler.go:53` — `Compile`
- `internal/contextcompiler/contextcompiler_compiler.go:124` — `CompileForTaskClass`
- `internal/contextcompiler/contextcompiler_compiler.go:137` — `finalize`
- `internal/contextcompiler/contextcompiler_output_budget.go:43` — `CorpusCurateOutputTokenBudget`
- `internal/contextcompiler/contextcompiler_profiles.go:21` — `ResearchCorpusCurateV1`
- `internal/contextcompiler/contextcompiler_profiles.go:45` — `Registry`
- `internal/contextcompiler/contextcompiler_projections.go:32` — `RoleCatalogSelfEntry`

## internal/contextengine

Tracked artifacts: 35; kinds: go_source=21, test_only_signal=14

Production Go paths:
- `internal/contextengine/assembler.go`
- `internal/contextengine/bootstrap/bootstrap.go`
- `internal/contextengine/canonical/provider.go`
- `internal/contextengine/canonical/skills.go`
- `internal/contextengine/clock.go`
- `internal/contextengine/document/loader.go`
- `internal/contextengine/document/markdown.go`
- `internal/contextengine/document/path_security.go`
- `internal/contextengine/document/yaml.go`
- `internal/contextengine/domain.go`
- `internal/contextengine/errors.go`
- `internal/contextengine/hashing.go`
- `internal/contextengine/interfaces.go`
- `internal/contextengine/postgres/store.go`
- `internal/contextengine/precedence.go`
- `internal/contextengine/providerrender.go`
- `internal/contextengine/providers.go`
- `internal/contextengine/renderer.go`
- `internal/contextengine/service.go`
- `internal/contextengine/state_machine.go`
- `internal/contextengine/validation.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/contextengine/assembler.go:11` — `NewAssembler`
- `internal/contextengine/assembler.go:13` — `(a *DeterministicAssembler).Assemble`
- `internal/contextengine/assembler.go:123` — `optionalIndexes`
- `internal/contextengine/assembler.go:135` — `applyCountLimit`
- `internal/contextengine/assembler.go:146` — `omissionCandidates`
- `internal/contextengine/assembler.go:159` — `lessOmittable`
- `internal/contextengine/assembler.go:169` — `optionalSource`
- `internal/contextengine/assembler.go:173` — `includedBytes`
- `internal/contextengine/bootstrap/bootstrap.go:35` — `Open`
- `internal/contextengine/canonical/provider.go:49` — `New`
- `internal/contextengine/canonical/provider.go:67` — `(p *Provider).Load`
- `internal/contextengine/canonical/provider.go:117` — `(p *Provider).Validate`
- `internal/contextengine/canonical/provider.go:131` — `(p *Provider).read`
- `internal/contextengine/canonical/provider.go:154` — `schemaVersion`
- `internal/contextengine/canonical/provider.go:175` — `validatePrecedence`
- `internal/contextengine/canonical/provider.go:187` — `digestBundle`
- `internal/contextengine/canonical/skills.go:35` — `NewSkillProvider`
- `internal/contextengine/canonical/skills.go:64` — `normalizeLifecycle`
- `internal/contextengine/canonical/skills.go:83` — `(p *SkillProvider).ListActiveForRole`
- `internal/contextengine/canonical/skills.go:96` — `(p *SkillProvider).GetActiveForRole`
- `internal/contextengine/canonical/skills.go:113` — `(p *SkillProvider).ValidateVersion`
- `internal/contextengine/canonical/skills.go:127` — `(p *SkillProvider).Record`
- `internal/contextengine/canonical/skills.go:132` — `validateSkillCatalogNode`
- `internal/contextengine/canonical/skills.go:139` — `sourceFileName`
- `internal/contextengine/clock.go:7` — `(systemClock).Now`
- `internal/contextengine/document/loader.go:22` — `NewLoader`
- `internal/contextengine/document/loader.go:44` — `(l *Loader).Root`
- `internal/contextengine/document/loader.go:46` — `(l *Loader).Load`
- `internal/contextengine/document/markdown.go:20` — `NormalizeText`
- `internal/contextengine/document/markdown.go:32` — `ParseMarkdown`
- `internal/contextengine/document/path_security.go:11` — `resolveWithinRoot`
- `internal/contextengine/document/path_security.go:33` — `ensureContained`
- `internal/contextengine/document/path_security.go:44` — `validateOpenedFile`
- `internal/contextengine/document/yaml.go:18` — `ParseStrictYAML`
- `internal/contextengine/document/yaml.go:38` — `validateNode`
- `internal/contextengine/document/yaml.go:92` — `(e *DuplicateKeyError).Error`
- `internal/contextengine/document/yaml.go:96` — `DecodeStringMap`
- `internal/contextengine/document/yaml.go:110` — `CanonicalYAML`
- `internal/contextengine/document/yaml.go:122` — `encodeCanonical`
- `internal/contextengine/document/yaml.go:161` — `normalizeTag`
- `internal/contextengine/document/yaml.go:168` — `writeLengthPrefixed`
- `internal/contextengine/domain.go:223` — `RenderRank`
- `internal/contextengine/domain.go:252` — `AuthorityPriority`
- `internal/contextengine/errors.go:77` — `(e *RejectionError).Error`
- `internal/contextengine/errors.go:91` — `(e *RejectionError).Unwrap`
- `internal/contextengine/errors.go:98` — `Reject`
- `internal/contextengine/errors.go:102` — `ReasonOf`
- `internal/contextengine/hashing.go:11` — `DigestCanonicalBytes`
- `internal/contextengine/hashing.go:12` — `DigestMarkdown`
- `internal/contextengine/hashing.go:14` — `digest`
- `internal/contextengine/hashing.go:32` — `DigestBuildRequest`
- `internal/contextengine/hashing.go:80` — `writeField`
- `internal/contextengine/hashing.go:91` — `writeInt`
- `internal/contextengine/hashing.go:94` — `writeBool`
- `internal/contextengine/postgres/store.go:24` — `New`
- `internal/contextengine/postgres/store.go:31` — `(s *Store).Pool`
- `internal/contextengine/postgres/store.go:33` — `(s *Store).AllocateID`
- `internal/contextengine/postgres/store.go:42` — `(s *Store).Create`
- `internal/contextengine/postgres/store.go:89` — `(s *Store).Get`
- `internal/contextengine/postgres/store.go:103` — `(s *Store).GetByIdempotency`
- `internal/contextengine/postgres/store.go:107` — `(s *Store).List`
- `internal/contextengine/postgres/store.go:144` — `(s *Store).Invalidate`
- `internal/contextengine/postgres/store.go:205` — `(s *Store).RecordForbiddenSourceRejection`
- `internal/contextengine/postgres/store.go:257` — `(s *Store).RecordValidationFailure`
- `internal/contextengine/postgres/store.go:290` — `policyDriftReason`
- `internal/contextengine/postgres/store.go:309` — `scanSnapshot`
- `internal/contextengine/postgres/store.go:327` — `insertSnapshot`
- `internal/contextengine/postgres/store.go:349` — `insertSegment`
- `internal/contextengine/postgres/store.go:357` — `getSnapshot`
- `internal/contextengine/postgres/store.go:365` — `getByIdempotency`
- `internal/contextengine/postgres/store.go:380` — `listSegments`
- `internal/contextengine/postgres/store.go:399` — `appendAuditAndOutbox`
- `internal/contextengine/postgres/store.go:403` — `appendAuditAndOutboxWithCorrelation`
- `internal/contextengine/postgres/store.go:415` — `appendAudit`
- `internal/contextengine/postgres/store.go:427` — `eventPayload`
- `internal/contextengine/postgres/store.go:452` — `nullableContent`
- `internal/contextengine/postgres/store.go:458` — `deref`
- `internal/contextengine/postgres/store.go:465` — `rollback`
- `internal/contextengine/postgres/store.go:471` — `mapError`
- `internal/contextengine/precedence.go:18` — `ValidatePrecedence`
- `internal/contextengine/providerrender.go:54` — `IsDynamicProviderTier`
- `internal/contextengine/providerrender.go:86` — `(r ProviderRender).Bytes`
- `internal/contextengine/providerrender.go:98` — `escapeUntrustedContent`
- `internal/contextengine/providerrender.go:116` — `providerHeader`
- `internal/contextengine/providerrender.go:129` — `BuildProviderRender`
- `internal/contextengine/providerrender.go:137` — `BuildProviderRenderLegacy`
- `internal/contextengine/providerrender.go:192` — `BuildProviderRenderV2`
- `internal/contextengine/providerrender.go:241` — `wrapUntrustedData`
- `internal/contextengine/providers.go:10` — `(NoopOwnerConstraintProvider).ListApplicable`
- `internal/contextengine/providers.go:13` — `(NoopOwnerConstraintProvider).ValidateVersion`
- `internal/contextengine/providers.go:19` — `(UnavailableMemoryProvider).ListApproved`
- `internal/contextengine/providers.go:22` — `(UnavailableMemoryProvider).ValidateVersion`
- `internal/contextengine/providers.go:28` — `(UnavailableProjectProvider).GetProjectContext`
- `internal/contextengine/providers.go:34` — `(UnavailableProjectProvider).ValidateVersion`
- `internal/contextengine/providers.go:40` — `(UnavailableTaskProvider).GetTaskContext`
- `internal/contextengine/providers.go:46` — `(UnavailableTaskProvider).ValidateVersion`
- `internal/contextengine/providers.go:52` — `(UnavailableRAGProvider).ListApprovedEvidence`
- `internal/contextengine/providers.go:55` — `(UnavailableRAGProvider).ValidateVersion`
- `internal/contextengine/renderer.go:13` — `NewRenderer`
- `internal/contextengine/renderer.go:50` — `(r *PortableRenderer).Render`
- `internal/contextengine/service.go:40` — `NewService`
- `internal/contextengine/service.go:71` — `(s *contextService).Build`
- `internal/contextengine/service.go:143` — `(s *contextService).rejectBuild`
- `internal/contextengine/service.go:158` — `isForbiddenSourceReason`
- `internal/contextengine/service.go:168` — `(s *contextService).resolve`
- `internal/contextengine/service.go:318` — `(s *contextService).resolveSkills`
- `internal/contextengine/service.go:354` — `(s *contextService).revalidateResolved`
- `internal/contextengine/service.go:412` — `(s *contextService).Get`
- `internal/contextengine/service.go:415` — `(s *contextService).List`
- `internal/contextengine/service.go:419` — `(s *contextService).Render`
- `internal/contextengine/service.go:437` — `(s *contextService).Validate`
- `internal/contextengine/service.go:549` — `(s *contextService).Invalidate`
- `internal/contextengine/service.go:568` — `(s *contextService).verifySnapshotIntegrity`
- `internal/contextengine/service.go:579` — `canonicalRecords`
- `internal/contextengine/service.go:588` — `documentRecord`
- `internal/contextengine/service.go:593` — `normalizeSource`
- `internal/contextengine/service.go:608` — `segmentToRecord`
- `internal/contextengine/service.go:612` — `driftForKind`
- `internal/contextengine/service.go:619` — `revisionID`
- `internal/contextengine/state_machine.go:3` — `CanTransition`
- `internal/contextengine/validation.go:21` — `ValidateBuildRequest`
- `internal/contextengine/validation.go:55` — `ValidateSourceMetadata`
- `internal/contextengine/validation.go:98` — `ValidateDataClass`
- `internal/contextengine/validation.go:111` — `ValidateProfile`
- `internal/contextengine/validation.go:139` — `ValidateAgent`
- `internal/contextengine/validation.go:146` — `ValidateSkill`
- `internal/contextengine/validation.go:248` — `ValidateSkillLifecycle`
- `internal/contextengine/validation.go:258` — `NormalizeSkillIDs`
- `internal/contextengine/validation.go:264` — `requiredString`
- `internal/contextengine/validation.go:276` — `requiredValue`
- `internal/contextengine/validation.go:284` — `allowedInstructionClass`
- `internal/contextengine/validation.go:293` — `allowedTrustClass`
- `internal/contextengine/validation.go:302` — `IsOperational`

## internal/corpuscensus

Tracked artifacts: 15; kinds: go_source=9, test_only_signal=6

Production Go paths:
- `internal/corpuscensus/corpuscensus_bronze.go`
- `internal/corpuscensus/corpuscensus_census.go`
- `internal/corpuscensus/corpuscensus_checkpoint.go`
- `internal/corpuscensus/corpuscensus_classify.go`
- `internal/corpuscensus/corpuscensus_dedup.go`
- `internal/corpuscensus/corpuscensus_domain.go`
- `internal/corpuscensus/corpuscensus_orchestrator.go`
- `internal/corpuscensus/corpuscensus_topics_v2.go`
- `internal/corpuscensus/corpuscensus_validate.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/corpuscensus/corpuscensus_bronze.go:35` — `(p BronzePaper).str`
- `internal/corpuscensus/corpuscensus_bronze.go:71` — `NewSQLiteCLIReader`
- `internal/corpuscensus/corpuscensus_bronze.go:83` — `(r *SQLiteCLIReader).query`
- `internal/corpuscensus/corpuscensus_bronze.go:103` — `(r *SQLiteCLIReader).ListPapers`
- `internal/corpuscensus/corpuscensus_bronze.go:111` — `(r *SQLiteCLIReader).ListSources`
- `internal/corpuscensus/corpuscensus_census.go:91` — `classifyCoverageTier`
- `internal/corpuscensus/corpuscensus_census.go:104` — `BuildCensus`
- `internal/corpuscensus/corpuscensus_census.go:263` — `(c Census).SortedTopics`
- `internal/corpuscensus/corpuscensus_checkpoint.go:30` — `OpenStateStore`
- `internal/corpuscensus/corpuscensus_checkpoint.go:63` — `(s *StateStore).isTerminal`
- `internal/corpuscensus/corpuscensus_checkpoint.go:71` — `(s *StateStore).Get`
- `internal/corpuscensus/corpuscensus_checkpoint.go:76` — `(s *StateStore).Put`
- `internal/corpuscensus/corpuscensus_checkpoint.go:80` — `(s *StateStore).All`
- `internal/corpuscensus/corpuscensus_checkpoint.go:93` — `(s *StateStore).Flush`
- `internal/corpuscensus/corpuscensus_classify.go:24` — `TopicsForCollection`
- `internal/corpuscensus/corpuscensus_classify.go:40` — `ClassifyAuthorityTier`
- `internal/corpuscensus/corpuscensus_classify.go:55` — `DeterministicQuality`
- `internal/corpuscensus/corpuscensus_classify.go:82` — `ClassifySourceType`
- `internal/corpuscensus/corpuscensus_classify.go:109` — `DetectLanguage`
- `internal/corpuscensus/corpuscensus_classify.go:141` — `LooksLikeReferencesPage`
- `internal/corpuscensus/corpuscensus_dedup.go:14` — `ResolveWorkIdentity`
- `internal/corpuscensus/corpuscensus_dedup.go:42` — `normalizeTitle`
- `internal/corpuscensus/corpuscensus_dedup.go:50` — `titleYearKey`
- `internal/corpuscensus/corpuscensus_dedup.go:68` — `newUnionFind`
- `internal/corpuscensus/corpuscensus_dedup.go:72` — `(u *unionFind).find`
- `internal/corpuscensus/corpuscensus_dedup.go:86` — `(u *unionFind).union`
- `internal/corpuscensus/corpuscensus_dedup.go:98` — `GroupWorks`
- `internal/corpuscensus/corpuscensus_dedup.go:143` — `SelectCanonicalArtifact`
- `internal/corpuscensus/corpuscensus_dedup.go:161` — `yearOf`
- `internal/corpuscensus/corpuscensus_domain.go:63` — `(d Decision).Valid`
- `internal/corpuscensus/corpuscensus_domain.go:135` — `(q QualitySignals).Score`
- `internal/corpuscensus/corpuscensus_orchestrator.go:32` — `(c OrchestratorConfig).resolvedConcurrency`
- `internal/corpuscensus/corpuscensus_orchestrator.go:63` — `(o *Orchestrator).Run`
- `internal/corpuscensus/corpuscensus_orchestrator.go:144` — `(o *Orchestrator).processOne`
- `internal/corpuscensus/corpuscensus_topics_v2.go:57` — `TopicsForTitle`
- `internal/corpuscensus/corpuscensus_topics_v2.go:73` — `CombineTopics`
- `internal/corpuscensus/corpuscensus_topics_v2.go:90` — `sortStrings`
- `internal/corpuscensus/corpuscensus_validate.go:25` — `DefaultValidationConfig`
- `internal/corpuscensus/corpuscensus_validate.go:34` — `ValidatePDF`
- `internal/corpuscensus/corpuscensus_validate.go:98` — `runWithTimeoutPolicy`

## internal/corpuscluster

Tracked artifacts: 3; kinds: go_source=2, test_only_signal=1

Production Go paths:
- `internal/corpuscluster/corpuscluster_cluster.go`
- `internal/corpuscluster/corpuscluster_unionfind.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/corpuscluster/corpuscluster_cluster.go:42` — `tokenize`
- `internal/corpuscluster/corpuscluster_cluster.go:66` — `norm`
- `internal/corpuscluster/corpuscluster_cluster.go:74` — `cosineSimilarity`
- `internal/corpuscluster/corpuscluster_cluster.go:94` — `BuildTFIDF`
- `internal/corpuscluster/corpuscluster_cluster.go:147` — `BuildClusters`
- `internal/corpuscluster/corpuscluster_cluster.go:204` — `pairKeyOf`
- `internal/corpuscluster/corpuscluster_unionfind.go:13` — `newClusterUnionFind`
- `internal/corpuscluster/corpuscluster_unionfind.go:17` — `(u *clusterUnionFind).find`
- `internal/corpuscluster/corpuscluster_unionfind.go:31` — `(u *clusterUnionFind).union`
- `internal/corpuscluster/corpuscluster_unionfind.go:43` — `clusterIDOf`

## internal/corpuscuration

Tracked artifacts: 12; kinds: go_source=7, test_only_signal=5

Production Go paths:
- `internal/corpuscuration/corpuscuration_adjudication_contract.go`
- `internal/corpuscuration/corpuscuration_domain.go`
- `internal/corpuscuration/corpuscuration_external_import.go`
- `internal/corpuscuration/corpuscuration_gaps.go`
- `internal/corpuscuration/corpuscuration_identity_preflight.go`
- `internal/corpuscuration/corpuscuration_output_contract.go`
- `internal/corpuscuration/corpuscuration_store.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/corpuscuration/corpuscuration_adjudication_contract.go:64` — `(v *AdjudicationOutputContractViolation).Error`
- `internal/corpuscuration/corpuscuration_adjudication_contract.go:107` — `ValidateAdjudicationOutputContract`
- `internal/corpuscuration/corpuscuration_domain.go:41` — `(t Tier).Valid`
- `internal/corpuscuration/corpuscuration_external_import.go:39` — `ValidateExternalResultBatch`
- `internal/corpuscuration/corpuscuration_gaps.go:59` — `BuildDepartmentKnowledgeProfile`
- `internal/corpuscuration/corpuscuration_identity_preflight.go:39` — `normalizeTitleForIdentity`
- `internal/corpuscuration/corpuscuration_identity_preflight.go:92` — `CollapseDuplicateWorksInCluster`
- `internal/corpuscuration/corpuscuration_identity_preflight.go:116` — `CollapseDuplicateWorksInClusterWithIdentifiers`
- `internal/corpuscuration/corpuscuration_identity_preflight.go:181` — `pickCanonical`
- `internal/corpuscuration/corpuscuration_identity_preflight.go:195` — `isBetterCanonical`
- `internal/corpuscuration/corpuscuration_identity_preflight.go:207` — `mergeIdentifiers`
- `internal/corpuscuration/corpuscuration_identity_preflight.go:228` — `sortedKeys`
- `internal/corpuscuration/corpuscuration_output_contract.go:92` — `(v *OutputContractViolation).Error`
- `internal/corpuscuration/corpuscuration_output_contract.go:138` — `ValidateCurationOutputContract`
- `internal/corpuscuration/corpuscuration_store.go:18` — `InputHashOf`
- `internal/corpuscuration/corpuscuration_store.go:43` — `recordKey`
- `internal/corpuscuration/corpuscuration_store.go:45` — `OpenStore`
- `internal/corpuscuration/corpuscuration_store.go:78` — `(s *Store).Valid`
- `internal/corpuscuration/corpuscuration_store.go:93` — `(s *Store).Put`
- `internal/corpuscuration/corpuscuration_store.go:97` — `(s *Store).All`
- `internal/corpuscuration/corpuscuration_store.go:105` — `(s *Store).Flush`

## internal/corpusenrich

Tracked artifacts: 6; kinds: go_source=4, test_only_signal=2

Production Go paths:
- `internal/corpusenrich/corpusenrich_client.go`
- `internal/corpusenrich/corpusenrich_domain.go`
- `internal/corpusenrich/corpusenrich_orchestrator.go`
- `internal/corpusenrich/corpusenrich_store.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/corpusenrich/corpusenrich_client.go:24` — `NewClient`
- `internal/corpusenrich/corpusenrich_client.go:52` — `(c *Client).FetchBatch`
- `internal/corpusenrich/corpusenrich_client.go:114` — `boundedPreview`
- `internal/corpusenrich/corpusenrich_domain.go:30` — `(r AbstractRecord).HasAbstract`
- `internal/corpusenrich/corpusenrich_orchestrator.go:23` — `DefaultOrchestratorConfig`
- `internal/corpusenrich/corpusenrich_orchestrator.go:55` — `(o *Orchestrator).Run`
- `internal/corpusenrich/corpusenrich_orchestrator.go:123` — `(o *Orchestrator).fetchWithBackoff`
- `internal/corpusenrich/corpusenrich_store.go:22` — `OpenStore`
- `internal/corpusenrich/corpusenrich_store.go:48` — `(s *Store).Has`
- `internal/corpusenrich/corpusenrich_store.go:53` — `(s *Store).Put`
- `internal/corpusenrich/corpusenrich_store.go:55` — `(s *Store).All`
- `internal/corpusenrich/corpusenrich_store.go:63` — `(s *Store).Len`
- `internal/corpusenrich/corpusenrich_store.go:65` — `(s *Store).Flush`

## internal/corpussemantic

Tracked artifacts: 7; kinds: go_source=5, test_only_signal=2

Production Go paths:
- `internal/corpussemantic/corpussemantic_cluster.go`
- `internal/corpussemantic/corpussemantic_domain.go`
- `internal/corpussemantic/corpussemantic_embed.go`
- `internal/corpussemantic/corpussemantic_hash.go`
- `internal/corpussemantic/corpussemantic_store.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/corpussemantic/corpussemantic_cluster.go:20` — `dot`
- `internal/corpussemantic/corpussemantic_cluster.go:28` — `norm2`
- `internal/corpussemantic/corpussemantic_cluster.go:36` — `cosineSimilarityMatrix`
- `internal/corpussemantic/corpussemantic_cluster.go:60` — `sqrtApprox`
- `internal/corpussemantic/corpussemantic_cluster.go:78` — `AverageLinkCluster`
- `internal/corpussemantic/corpussemantic_cluster.go:202` — `intraClusterSimilarity`
- `internal/corpussemantic/corpussemantic_cluster.go:222` — `centroidSimilarity`
- `internal/corpussemantic/corpussemantic_cluster.go:259` — `clusterIDOf`
- `internal/corpussemantic/corpussemantic_embed.go:33` — `DefaultEmbedConfig`
- `internal/corpussemantic/corpussemantic_embed.go:51` — `Run`
- `internal/corpussemantic/corpussemantic_hash.go:9` — `hashStrings`
- `internal/corpussemantic/corpussemantic_store.go:15` — `OpenStore`
- `internal/corpussemantic/corpussemantic_store.go:44` — `(s *Store).Valid`
- `internal/corpussemantic/corpussemantic_store.go:52` — `(s *Store).Put`
- `internal/corpussemantic/corpussemantic_store.go:54` — `(s *Store).All`
- `internal/corpussemantic/corpussemantic_store.go:62` — `(s *Store).Flush`

## internal/costledger

Tracked artifacts: 7; kinds: go_source=6, test_only_signal=1

Production Go paths:
- `internal/costledger/doc.go`
- `internal/costledger/errors.go`
- `internal/costledger/ports.go`
- `internal/costledger/postgres/embeddings.go`
- `internal/costledger/postgres/store.go`
- `internal/costledger/types.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/costledger/postgres/embeddings.go:15` — `(s *Store).CreateEmbeddingInvocation`
- `internal/costledger/postgres/embeddings.go:47` — `(s *Store).ReserveEmbedding`
- `internal/costledger/postgres/embeddings.go:94` — `(s *Store).ReconcileEmbedding`
- `internal/costledger/postgres/embeddings.go:142` — `(s *Store).ReleaseEmbedding`
- `internal/costledger/postgres/store.go:23` — `isUniqueViolation`
- `internal/costledger/postgres/store.go:32` — `New`
- `internal/costledger/postgres/store.go:42` — `(s *Store).ListCallBreakdowns`
- `internal/costledger/postgres/store.go:140` — `(s *Store).ListEvents`
- `internal/costledger/postgres/store.go:154` — `(s *Store).ListOrphanedReservations`
- `internal/costledger/postgres/store.go:175` — `scanWalletEvents`
- `internal/costledger/postgres/store.go:193` — `(s *Store).GetWallet`
- `internal/costledger/postgres/store.go:210` — `(s *Store).SetBalance`
- `internal/costledger/postgres/store.go:235` — `(s *Store).Reserve`
- `internal/costledger/postgres/store.go:286` — `(s *Store).Reconcile`
- `internal/costledger/postgres/store.go:334` — `(s *Store).Release`
- `internal/costledger/postgres/store.go:383` — `(s *Store).MarkPendingReconciliation`
- `internal/costledger/postgres/store.go:430` — `(s *Store).RecordSubscriptionConsumption`
- `internal/costledger/postgres/store.go:446` — `lockWalletAndReservation`
- `internal/costledger/types.go:19` — `(w ProviderWallet).Available`
- `internal/costledger/types.go:29` — `(k EventKind).Valid`
- `internal/costledger/types.go:67` — `(o EmbeddingOperation).Valid`

## internal/costledgerfixtures

Tracked artifacts: 4; kinds: test_only_signal=4

Production Go paths:
- `internal/costledgerfixtures/activate.go`
- `internal/costledgerfixtures/runner.go`

## internal/dataclassifier

Tracked artifacts: 2; kinds: go_source=1, test_only_signal=1

Production Go paths:
- `internal/dataclassifier/detect.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/dataclassifier/detect.go:26` — `(f Finding).Any`
- `internal/dataclassifier/detect.go:55` — `joinAlternatives`
- `internal/dataclassifier/detect.go:69` — `Detect`

## internal/decisiongraph

Tracked artifacts: 15; kinds: go_source=12, test_only_signal=3

Production Go paths:
- `internal/decisiongraph/budget.go`
- `internal/decisiongraph/decision.go`
- `internal/decisiongraph/doc.go`
- `internal/decisiongraph/errors.go`
- `internal/decisiongraph/graph.go`
- `internal/decisiongraph/hashing.go`
- `internal/decisiongraph/ports.go`
- `internal/decisiongraph/postgres/store.go`
- `internal/decisiongraph/records.go`
- `internal/decisiongraph/service.go`
- `internal/decisiongraph/transitions.go`
- `internal/decisiongraph/types.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/decisiongraph/budget.go:40` — `(e *BudgetDimensionError).Error`
- `internal/decisiongraph/budget.go:44` — `(e *BudgetDimensionError).Unwrap`
- `internal/decisiongraph/budget.go:46` — `(l BudgetLimits).Validate`
- `internal/decisiongraph/budget.go:59` — `(u BudgetUsage).Validate`
- `internal/decisiongraph/budget.go:72` — `(l BudgetLimits).Reserve`
- `internal/decisiongraph/budget.go:120` — `safeAdd`
- `internal/decisiongraph/decision.go:13` — `(d DecisionRecord).Validate`
- `internal/decisiongraph/decision.go:34` — `validateTypedReferences`
- `internal/decisiongraph/graph.go:13` — `NewGraph`
- `internal/decisiongraph/graph.go:54` — `(g *Graph).Nodes`
- `internal/decisiongraph/graph.go:63` — `(g *Graph).Edges`
- `internal/decisiongraph/graph.go:80` — `(g *Graph).ReadyNodeIDs`
- `internal/decisiongraph/graph.go:123` — `(g *Graph).Depths`
- `internal/decisiongraph/graph.go:184` — `(g *Graph).hasDependencyCycle`
- `internal/decisiongraph/hashing.go:22` — `(g *Graph).CanonicalHash`
- `internal/decisiongraph/ports.go:29` — `(SystemClock).Now`
- `internal/decisiongraph/postgres/store.go:26` — `New`
- `internal/decisiongraph/postgres/store.go:36` — `(s *Store).CreateRun`
- `internal/decisiongraph/postgres/store.go:109` — `(s *Store).AppendGraph`
- `internal/decisiongraph/postgres/store.go:240` — `(s *Store).StartRun`
- `internal/decisiongraph/postgres/store.go:268` — `(s *Store).TransitionBranch`
- `internal/decisiongraph/postgres/store.go:330` — `(s *Store).ClaimReadyNode`
- `internal/decisiongraph/postgres/store.go:468` — `(s *Store).FinishExecution`
- `internal/decisiongraph/postgres/store.go:617` — `(s *Store).RecordObservation`
- `internal/decisiongraph/postgres/store.go:656` — `(s *Store).RecordVerification`
- `internal/decisiongraph/postgres/store.go:772` — `(s *Store).RecordTerminalDecision`
- `internal/decisiongraph/postgres/store.go:948` — `(s *Store).CloseUnselectedRun`
- `internal/decisiongraph/postgres/store.go:1022` — `(s *Store).RecoverExpiredExecutions`
- `internal/decisiongraph/postgres/store.go:1182` — `(s *Store).TraceRef`
- `internal/decisiongraph/postgres/store.go:1250` — `scanRun`
- `internal/decisiongraph/postgres/store.go:1271` — `(s *Store).loadRunByIdempotency`
- `internal/decisiongraph/postgres/store.go:1309` — `sameCreateRequest`
- `internal/decisiongraph/postgres/store.go:1320` — `nullableTerminalTime`
- `internal/decisiongraph/postgres/store.go:1329` — `newClaimToken`
- `internal/decisiongraph/postgres/store.go:1338` — `claimDigest`
- `internal/decisiongraph/postgres/store.go:1343` — `elapsedMilliseconds`
- `internal/decisiongraph/postgres/store.go:1355` — `eventDigest`
- `internal/decisiongraph/postgres/store.go:1364` — `mapNotFound`
- `internal/decisiongraph/records.go:38` — `(r CreateRunRequest).Validate`
- `internal/decisiongraph/records.go:92` — `(r AppendGraphRequest).Validate`
- `internal/decisiongraph/records.go:131` — `(r ClaimNodeRequest).Validate`
- `internal/decisiongraph/records.go:156` — `(r FinishExecutionRequest).Validate`
- `internal/decisiongraph/records.go:191` — `(r ObservationRecord).Validate`
- `internal/decisiongraph/records.go:218` — `(r VerificationRecord).Validate`
- `internal/decisiongraph/records.go:255` — `(r TerminalDecisionRequest).Validate`
- `internal/decisiongraph/records.go:284` — `(r CloseUnselectedRunRequest).Validate`
- `internal/decisiongraph/records.go:312` — `(r BranchTransitionRequest).Validate`
- `internal/decisiongraph/service.go:15` — `NewService`
- `internal/decisiongraph/service.go:25` — `(s *Service).CreateRun`
- `internal/decisiongraph/service.go:38` — `(s *Service).AppendGraph`
- `internal/decisiongraph/service.go:50` — `(s *Service).StartRun`
- `internal/decisiongraph/service.go:60` — `(s *Service).TransitionBranch`
- `internal/decisiongraph/service.go:70` — `(s *Service).ClaimReadyNode`
- `internal/decisiongraph/service.go:81` — `(s *Service).FinishExecution`
- `internal/decisiongraph/service.go:91` — `(s *Service).RecordObservation`
- `internal/decisiongraph/service.go:101` — `(s *Service).RecordVerification`
- `internal/decisiongraph/service.go:111` — `(s *Service).RecordTerminalDecision`
- `internal/decisiongraph/service.go:124` — `(s *Service).CloseUnselectedRun`
- `internal/decisiongraph/service.go:134` — `(s *Service).RecoverExpiredExecutions`
- `internal/decisiongraph/service.go:145` — `(s *Service).TraceRef`
- `internal/decisiongraph/service.go:156` — `postgresTimestamp`
- `internal/decisiongraph/transitions.go:5` — `ValidateBranchTransition`
- `internal/decisiongraph/transitions.go:29` — `ValidateExecutionTransition`
- `internal/decisiongraph/types.go:100` — `(n Node).Validate`
- `internal/decisiongraph/types.go:119` — `(e Edge).Validate`
- `internal/decisiongraph/types.go:126` — `(v NodeType).Valid`
- `internal/decisiongraph/types.go:135` — `(v EdgeType).Valid`
- `internal/decisiongraph/types.go:144` — `(v BranchState).Valid`
- `internal/decisiongraph/types.go:153` — `(v ExecutionState).Valid`
- `internal/decisiongraph/types.go:162` — `(v VerificationLabel).Valid`
- `internal/decisiongraph/types.go:171` — `(v RunStatus).Valid`

## internal/decisiongraphfixtures

Tracked artifacts: 4; kinds: test_only_signal=4

Production Go paths:
- `internal/decisiongraphfixtures/activate.go`
- `internal/decisiongraphfixtures/runner.go`
- `internal/decisiongraphfixtures/scenario.go`

## internal/decisiongraphtrace

Tracked artifacts: 6; kinds: go_source=4, test_only_signal=2

Production Go paths:
- `internal/decisiongraphtrace/doc.go`
- `internal/decisiongraphtrace/errors.go`
- `internal/decisiongraphtrace/store.go`
- `internal/decisiongraphtrace/types.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/decisiongraphtrace/store.go:28` — `New`
- `internal/decisiongraphtrace/store.go:46` — `(s *Store).TraceRefForRun`
- `internal/decisiongraphtrace/store.go:73` — `(s *Store).RunSummary`
- `internal/decisiongraphtrace/store.go:98` — `(s *Store).LoadTrace`
- `internal/decisiongraphtrace/store.go:115` — `(s *Store).loadTrace`
- `internal/decisiongraphtrace/store.go:189` — `(s *Store).loadNodes`
- `internal/decisiongraphtrace/store.go:215` — `(s *Store).loadEdges`
- `internal/decisiongraphtrace/store.go:251` — `(s *Store).loadDecision`

## internal/embeddingruntime

Tracked artifacts: 19; kinds: go_source=15, test_only_signal=4

Production Go paths:
- `internal/embeddingruntime/adapter/bgem3/adapter.go`
- `internal/embeddingruntime/adapter/bgem3/config.go`
- `internal/embeddingruntime/adapter/bgem3/errors.go`
- `internal/embeddingruntime/adapter/bgem3/health.go`
- `internal/embeddingruntime/adapter/bgem3/metrics.go`
- `internal/embeddingruntime/adapter/bgem3/wire.go`
- `internal/embeddingruntime/adapter/gemini/adapter.go`
- `internal/embeddingruntime/adapter/gemini/batch.go`
- `internal/embeddingruntime/adapter/gemini/breaker.go`
- `internal/embeddingruntime/adapter/gemini/config.go`
- `internal/embeddingruntime/adapter/gemini/errors.go`
- `internal/embeddingruntime/adapter/gemini/online.go`
- `internal/embeddingruntime/adapter/gemini/prompt.go`
- `internal/embeddingruntime/errors.go`
- `internal/embeddingruntime/port.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/embeddingruntime/adapter/bgem3/adapter.go:42` — `New`
- `internal/embeddingruntime/adapter/bgem3/adapter.go:46` — `newAdapter`
- `internal/embeddingruntime/adapter/bgem3/adapter.go:76` — `(a *Adapter).Metrics`
- `internal/embeddingruntime/adapter/bgem3/adapter.go:89` — `(a *Adapter).Embed`
- `internal/embeddingruntime/adapter/bgem3/adapter.go:178` — `(a *Adapter).acquire`
- `internal/embeddingruntime/adapter/bgem3/adapter.go:187` — `(a *Adapter).release`
- `internal/embeddingruntime/adapter/bgem3/adapter.go:192` — `(a *Adapter).doJSON`
- `internal/embeddingruntime/adapter/bgem3/adapter.go:236` — `validateVector`
- `internal/embeddingruntime/adapter/bgem3/adapter.go:253` — `inputHash`
- `internal/embeddingruntime/adapter/bgem3/adapter.go:258` — `requestIdempotencyKey`
- `internal/embeddingruntime/adapter/bgem3/adapter.go:266` — `boundedPreview`
- `internal/embeddingruntime/adapter/bgem3/config.go:105` — `LoadConfig`
- `internal/embeddingruntime/adapter/bgem3/config.go:157` — `(c Config).Validate`
- `internal/embeddingruntime/adapter/bgem3/config.go:210` — `validateLoopbackURL`
- `internal/embeddingruntime/adapter/bgem3/config.go:238` — `isLoopbackHost`
- `internal/embeddingruntime/adapter/bgem3/config.go:246` — `defaultHTTPClient`
- `internal/embeddingruntime/adapter/bgem3/config.go:267` — `envBool`
- `internal/embeddingruntime/adapter/bgem3/config.go:279` — `envInt`
- `internal/embeddingruntime/adapter/bgem3/config.go:291` — `envDuration`
- `internal/embeddingruntime/adapter/bgem3/health.go:35` — `(a *Adapter).Healthy`
- `internal/embeddingruntime/adapter/bgem3/metrics.go:30` — `(m *Metrics).recordCall`
- `internal/embeddingruntime/adapter/bgem3/metrics.go:39` — `(m *Metrics).recordQueueRejection`
- `internal/embeddingruntime/adapter/bgem3/metrics.go:43` — `(m *Metrics).Snapshot`
- `internal/embeddingruntime/adapter/bgem3/wire.go:82` — `(h Health).Ready`
- `internal/embeddingruntime/adapter/gemini/adapter.go:35` — `New`
- `internal/embeddingruntime/adapter/gemini/adapter.go:39` — `newAdapter`
- `internal/embeddingruntime/adapter/gemini/adapter.go:75` — `(a *Adapter).token`
- `internal/embeddingruntime/adapter/gemini/adapter.go:86` — `(a *Adapter).doJSON`
- `internal/embeddingruntime/adapter/gemini/adapter.go:168` — `isTextTooLongError`
- `internal/embeddingruntime/adapter/gemini/adapter.go:177` — `boundedPreview`
- `internal/embeddingruntime/adapter/gemini/batch.go:87` — `mapJobState`
- `internal/embeddingruntime/adapter/gemini/batch.go:115` — `(a *Adapter).CreateBatch`
- `internal/embeddingruntime/adapter/gemini/batch.go:171` — `(a *Adapter).GetBatch`
- `internal/embeddingruntime/adapter/gemini/batch.go:194` — `(a *Adapter).CancelBatch`
- `internal/embeddingruntime/adapter/gemini/batch.go:202` — `(a *Adapter).ReadBatchResults`
- `internal/embeddingruntime/adapter/gemini/breaker.go:19` — `newCircuitBreaker`
- `internal/embeddingruntime/adapter/gemini/breaker.go:23` — `(b *circuitBreaker).allow`
- `internal/embeddingruntime/adapter/gemini/breaker.go:37` — `(b *circuitBreaker).success`
- `internal/embeddingruntime/adapter/gemini/breaker.go:44` — `(b *circuitBreaker).failure`
- `internal/embeddingruntime/adapter/gemini/config.go:51` — `LoadConfig`
- `internal/embeddingruntime/adapter/gemini/config.go:81` — `(c Config).Validate`
- `internal/embeddingruntime/adapter/gemini/config.go:110` — `defaultHTTPClient`
- `internal/embeddingruntime/adapter/gemini/config.go:132` — `envBool`
- `internal/embeddingruntime/adapter/gemini/config.go:144` — `envInt`
- `internal/embeddingruntime/adapter/gemini/config.go:156` — `envDuration`
- `internal/embeddingruntime/adapter/gemini/online.go:96` — `(a *Adapter).Embed`
- `internal/embeddingruntime/adapter/gemini/online.go:156` — `validateVector`
- `internal/embeddingruntime/adapter/gemini/prompt.go:12` — `renderPrompt`
- `internal/embeddingruntime/adapter/gemini/prompt.go:32` — `taskTypeField`
- `internal/embeddingruntime/port.go:24` — `(k TaskKind).Valid`
- `internal/embeddingruntime/port.go:57` — `(i EmbedItem).IsMedia`
- `internal/embeddingruntime/port.go:62` — `(i EmbedItem).Valid`
- `internal/embeddingruntime/port.go:124` — `(s BatchJobStatus).Valid`
- `internal/embeddingruntime/port.go:136` — `(s BatchJobStatus).Terminal`

## internal/endtoendfixtures

Tracked artifacts: 6; kinds: test_only_signal=6

Production Go paths:
- `internal/endtoendfixtures/activate.go`
- `internal/endtoendfixtures/research.go`
- `internal/endtoendfixtures/runner.go`
- `internal/endtoendfixtures/support.go`

## internal/evaluation

Tracked artifacts: 16; kinds: go_source=9, test_only_signal=7

Production Go paths:
- `internal/evaluation/comparison.go`
- `internal/evaluation/doc.go`
- `internal/evaluation/errors.go`
- `internal/evaluation/fake.go`
- `internal/evaluation/fixtures/catalog.go`
- `internal/evaluation/fixtures/fixture.go`
- `internal/evaluation/fixtures/runner.go`
- `internal/evaluation/metrics/metrics.go`
- `internal/evaluation/ports.go`
- `internal/evaluation/postgres/store.go`
- `internal/evaluation/service.go`
- `internal/evaluation/types.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/evaluation/comparison.go:31` — `CompareResults`
- `internal/evaluation/comparison.go:93` — `overallVerdict`
- `internal/evaluation/comparison.go:121` — `(r SuiteComparisonResult).Validate`
- `internal/evaluation/comparison.go:144` — `CompareSuite`
- `internal/evaluation/comparison.go:202` — `worseVerdict`
- `internal/evaluation/fake.go:18` — `NewFakeTraceSource`
- `internal/evaluation/fake.go:25` — `(f *FakeTraceSource).Seed`
- `internal/evaluation/fake.go:32` — `cloneBytes`
- `internal/evaluation/fake.go:42` — `(f *FakeTraceSource).SetError`
- `internal/evaluation/fake.go:48` — `(f *FakeTraceSource).LoadTrace`
- `internal/evaluation/fake.go:74` — `NewFakeEvaluator`
- `internal/evaluation/fake.go:76` — `(f *FakeEvaluator).SetScoreFunc`
- `internal/evaluation/fake.go:82` — `(f *FakeEvaluator).Evaluate`
- `internal/evaluation/metrics/metrics.go:15` — `RecallAt`
- `internal/evaluation/metrics/metrics.go:34` — `NDCGAt`
- `internal/evaluation/metrics/metrics.go:65` — `ReciprocalRank`
- `internal/evaluation/metrics/metrics.go:80` — `IdentifierPrecisionAt`
- `internal/evaluation/metrics/metrics.go:100` — `NumericFalsePositiveRate`
- `internal/evaluation/ports.go:27` — `(SystemClock).Now`
- `internal/evaluation/postgres/store.go:29` — `New`
- `internal/evaluation/postgres/store.go:55` — `(s *Store).CreateRun`
- `internal/evaluation/postgres/store.go:77` — `(s *Store).CompleteRun`
- `internal/evaluation/postgres/store.go:91` — `(s *Store).RecordOutcome`
- `internal/evaluation/postgres/store.go:122` — `(s *Store).GetRun`
- `internal/evaluation/postgres/store.go:138` — `(s *Store).ListOutcomes`
- `internal/evaluation/service.go:18` — `NewService`
- `internal/evaluation/service.go:36` — `(s *Service).EvaluateCase`
- `internal/evaluation/service.go:77` — `(s *Service).EvaluateSuite`
- `internal/evaluation/service.go:94` — `(s *Service).RunComparison`
- `internal/evaluation/types.go:21` — `(r TraceRef).Validate`
- `internal/evaluation/types.go:46` — `(t EvaluationTrace).ContentHash`
- `internal/evaluation/types.go:51` — `(t EvaluationTrace).Validate`
- `internal/evaluation/types.go:77` — `(c EvaluationCase).Validate`
- `internal/evaluation/types.go:102` — `(s EvaluationSuite).Validate`
- `internal/evaluation/types.go:128` — `(s EvaluationSuite).caseByID`
- `internal/evaluation/types.go:146` — `(r EvaluationRole).Valid`
- `internal/evaluation/types.go:167` — `(r EvaluationRequest).Validate`
- `internal/evaluation/types.go:203` — `(m Metric).Validate`
- `internal/evaluation/types.go:222` — `(v Verdict).Valid`
- `internal/evaluation/types.go:242` — `(r EvaluationResult).Validate`

## internal/evaluationdb

Tracked artifacts: 2; kinds: go_source=1, test_only_signal=1

Production Go paths:
- `internal/evaluationdb/safety.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/evaluationdb/safety.go:19` — `RequireDisposable`

## internal/executive

Tracked artifacts: 59; kinds: go_source=39, test_only_signal=20

Production Go paths:
- `internal/executive/bootstrap/runtime.go`
- `internal/executive/budget.go`
- `internal/executive/doc.go`
- `internal/executive/errors.go`
- `internal/executive/orchestrator.go`
- `internal/executive/parser.go`
- `internal/executive/ports.go`
- `internal/executive/postrun/doc.go`
- `internal/executive/postrun/ports.go`
- `internal/executive/postrun/service.go`
- `internal/executive/postrun/tasksadapter.go`
- `internal/executive/postrun/types.go`
- `internal/executive/projector.go`
- `internal/executive/recovery.go`
- `internal/executive/runtimeadapter/agentbudgets.go`
- `internal/executive/runtimeadapter/agentmessages.go`
- `internal/executive/runtimeadapter/budget_models.go`
- `internal/executive/runtimeadapter/context.go`
- `internal/executive/runtimeadapter/dag_tasks.go`
- `internal/executive/runtimeadapter/decisions.go`
- `internal/executive/runtimeadapter/evidence_tasks.go`
- `internal/executive/runtimeadapter/models.go`
- `internal/executive/runtimeadapter/registry.go`
- `internal/executive/runtimeadapter/roots.go`
- `internal/executive/runtimeadapter/tasks.go`
- `internal/executive/schemas.go`
- `internal/executive/sleep/candidate.go`
- `internal/executive/sleep/doc.go`
- `internal/executive/sleep/grouping.go`
- `internal/executive/sleep/ports.go`
- `internal/executive/sleep/postgres.go`
- `internal/executive/sleep/service.go`
- `internal/executive/sleep/types.go`
- `internal/executive/smoke/hardening.go`
- `internal/executive/smoke/smoke.go`
- `internal/executive/types.go`
- `internal/executive/validator.go`
- `internal/executive/worker.go`
- `internal/executive/worker_result.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/executive/bootstrap/runtime.go:34` — `Open`
- `internal/executive/budget.go:12` — `(b InvocationBudget).Total`
- `internal/executive/budget.go:13` — `NormalExpectedCalls`
- `internal/executive/budget.go:19` — `(b InvocationBudget).Validate`
- `internal/executive/orchestrator.go:45` — `WithAgentBudgets`
- `internal/executive/orchestrator.go:52` — `WithAgentMessaging`
- `internal/executive/orchestrator.go:56` — `NewOrchestrator`
- `internal/executive/orchestrator.go:77` — `(o *Orchestrator).Submit`
- `internal/executive/orchestrator.go:142` — `(o *Orchestrator).Status`
- `internal/executive/orchestrator.go:165` — `(o *Orchestrator).Resume`
- `internal/executive/orchestrator.go:311` — `(o *Orchestrator).createCEOPlanTask`
- `internal/executive/orchestrator.go:329` — `(o *Orchestrator).createLeaderPlanTask`
- `internal/executive/orchestrator.go:355` — `(o *Orchestrator).attachChildCoordination`
- `internal/executive/orchestrator.go:391` — `(o *Orchestrator).driveInProgress`
- `internal/executive/orchestrator.go:396` — `(o *Orchestrator).driveDepartments`
- `internal/executive/orchestrator.go:543` — `(o *Orchestrator).materializeWorkerTasks`
- `internal/executive/orchestrator.go:569` — `appendResultRequirement`
- `internal/executive/orchestrator.go:579` — `(o *Orchestrator).createReviewTask`
- `internal/executive/orchestrator.go:595` — `(o *Orchestrator).createClosureTask`
- `internal/executive/orchestrator.go:607` — `(o *Orchestrator).driveTypedTask`
- `internal/executive/orchestrator.go:729` — `(o *Orchestrator).gatedComplete`
- `internal/executive/orchestrator.go:778` — `(o *Orchestrator).ReconcileGatedCompletions`
- `internal/executive/orchestrator.go:811` — `(o *Orchestrator).completeRoot`
- `internal/executive/orchestrator.go:836` — `(o *Orchestrator).validateRunCompletionEvidence`
- `internal/executive/orchestrator.go:861` — `(o *Orchestrator).resultForCompletedTask`
- `internal/executive/orchestrator.go:877` — `(o *Orchestrator).handlePhaseError`
- `internal/executive/orchestrator.go:895` — `(o *Orchestrator).blockRoot`
- `internal/executive/orchestrator.go:903` — `(o *Orchestrator).anyProvisionedLeasedTask`
- `internal/executive/orchestrator.go:919` — `(o *Orchestrator).localLease`
- `internal/executive/orchestrator.go:925` — `(o *Orchestrator).rememberLease`
- `internal/executive/orchestrator.go:930` — `(o *Orchestrator).forgetLease`
- `internal/executive/orchestrator.go:936` — `correlationID`
- `internal/executive/orchestrator.go:941` — `childKey`
- `internal/executive/orchestrator.go:944` — `taskCausation`
- `internal/executive/orchestrator.go:945` — `attemptCausation`
- `internal/executive/orchestrator.go:948` — `withoutRoot`
- `internal/executive/orchestrator.go:957` — `findTaskByMarker`
- `internal/executive/orchestrator.go:965` — `findTaskByKey`
- `internal/executive/orchestrator.go:973` — `isTerminalTask`
- `internal/executive/orchestrator.go:981` — `latestFinishedAttemptID`
- `internal/executive/orchestrator.go:992` — `resultRequirementID`
- `internal/executive/orchestrator.go:1000` — `truncate`
- `internal/executive/orchestrator.go:1006` — `boundedJSON`
- `internal/executive/orchestrator.go:1014` — `departmentWorkerTasks`
- `internal/executive/orchestrator.go:1024` — `allDepartmentWorkersTerminal`
- `internal/executive/orchestrator.go:1036` — `latestReviewTask`
- `internal/executive/orchestrator.go:1054` — `reviewReplanOrdinal`
- `internal/executive/orchestrator.go:1064` — `boundedDepartmentSummary`
- `internal/executive/orchestrator.go:1083` — `boundedClosureSummary`
- `internal/executive/parser.go:22` — `ParseExecutivePlan`
- `internal/executive/parser.go:36` — `ParseDepartmentPlan`
- `internal/executive/parser.go:50` — `ParseDepartmentReview`
- `internal/executive/parser.go:81` — `ParseExecutiveClosure`
- `internal/executive/parser.go:105` — `decodeStrictModelJSON`
- `internal/executive/parser.go:140` — `findForbiddenModelKey`
- `internal/executive/parser.go:167` — `validateExecutivePlanShape`
- `internal/executive/parser.go:203` — `validateDepartmentPlanShape`
- `internal/executive/parser.go:224` — `validateWorkerTaskShape`
- `internal/executive/parser.go:259` — `validRequirementType`
- `internal/executive/parser.go:267` — `validateStrings`
- `internal/executive/parser.go:278` — `validateRequiredString`
- `internal/executive/ports.go:175` — `(f ClockFunc).Now`
- `internal/executive/postrun/service.go:31` — `NewService`
- `internal/executive/postrun/service.go:43` — `(s *Service).ProcessRun`
- `internal/executive/postrun/service.go:116` — `problemFromObligations`
- `internal/executive/postrun/tasksadapter.go:16` — `(a TaskRoleResolver).AssignedRoleID`
- `internal/executive/projector.go:13` — `ProjectRun`
- `internal/executive/recovery.go:17` — `(o *Orchestrator).ResumeDurable`
- `internal/executive/recovery.go:112` — `(o *Orchestrator).findOrphanedSucceededInvocation`
- `internal/executive/runtimeadapter/agentbudgets.go:20` — `(a AgentBudgets).CreateRootBudget`
- `internal/executive/runtimeadapter/agentbudgets.go:25` — `(a AgentBudgets).InheritForChild`
- `internal/executive/runtimeadapter/agentmessages.go:66` — `(a AgentMessages).resolveOrProvisionPrincipalForRole`
- `internal/executive/runtimeadapter/agentmessages.go:102` — `validateSenderRoleWithPrincipal`
- `internal/executive/runtimeadapter/agentmessages.go:110` — `(a AgentMessages).SendDelegation`
- `internal/executive/runtimeadapter/agentmessages.go:151` — `(a AgentMessages).SendCompletion`
- `internal/executive/runtimeadapter/budget_models.go:21` — `(b BudgetModels).EnsureInvocation`
- `internal/executive/runtimeadapter/budget_models.go:35` — `(b BudgetModels).GetInvocation`
- `internal/executive/runtimeadapter/budget_models.go:39` — `(b BudgetModels).FindTaskAttemptInvocations`
- `internal/executive/runtimeadapter/budget_models.go:43` — `(b BudgetModels).GetResult`
- `internal/executive/runtimeadapter/budget_models.go:47` — `(b BudgetModels).validateCorrelationBudget`
- `internal/executive/runtimeadapter/budget_models.go:94` — `incrementInvocationBudget`
- `internal/executive/runtimeadapter/budget_models.go:107` — `suffixAfter`
- `internal/executive/runtimeadapter/context.go:15` — `(a Context).Build`
- `internal/executive/runtimeadapter/dag_tasks.go:22` — `(d DAGTasks).CreateTask`
- `internal/executive/runtimeadapter/dag_tasks.go:31` — `sourceTaskID`
- `internal/executive/runtimeadapter/dag_tasks.go:44` — `containsTaskID`
- `internal/executive/runtimeadapter/decisions.go:52` — `(a DecisionGraph).RecordAttemptDecision`
- `internal/executive/runtimeadapter/decisions.go:191` — `(a DecisionGraph).reasoningPolicy`
- `internal/executive/runtimeadapter/decisions.go:204` — `verdictBranchState`
- `internal/executive/runtimeadapter/decisions.go:215` — `verificationLabel`
- `internal/executive/runtimeadapter/decisions.go:226` — `digestString`
- `internal/executive/runtimeadapter/evidence_tasks.go:28` — `(e EvidenceTasks).CreateTask`
- `internal/executive/runtimeadapter/evidence_tasks.go:91` — `(e EvidenceTasks).attachDepartmentBundle`
- `internal/executive/runtimeadapter/evidence_tasks.go:122` — `(e EvidenceTasks).projectDepartmentPlan`
- `internal/executive/runtimeadapter/evidence_tasks.go:156` — `(e EvidenceTasks).projectWorker`
- `internal/executive/runtimeadapter/evidence_tasks.go:191` — `(e EvidenceTasks).attachClosureBundle`
- `internal/executive/runtimeadapter/evidence_tasks.go:227` — `(e EvidenceTasks).projectReview`
- `internal/executive/runtimeadapter/evidence_tasks.go:262` — `(e EvidenceTasks).recordBundle`
- `internal/executive/runtimeadapter/evidence_tasks.go:282` — `(e EvidenceTasks).effectiveLimits`
- `internal/executive/runtimeadapter/evidence_tasks.go:289` — `latestFinishedAttempt`
- `internal/executive/runtimeadapter/evidence_tasks.go:301` — `taskEvidenceRefs`
- `internal/executive/runtimeadapter/evidence_tasks.go:312` — `boundedRefs`
- `internal/executive/runtimeadapter/evidence_tasks.go:319` — `truncateBundleString`
- `internal/executive/runtimeadapter/evidence_tasks.go:326` — `hasExecutiveBundle`
- `internal/executive/runtimeadapter/models.go:15` — `(a Models).EnsureInvocation`
- `internal/executive/runtimeadapter/models.go:38` — `(a Models).GetInvocation`
- `internal/executive/runtimeadapter/models.go:46` — `(a Models).FindTaskAttemptInvocations`
- `internal/executive/runtimeadapter/models.go:58` — `(a Models).GetResult`
- `internal/executive/runtimeadapter/models.go:73` — `mapInvocation`
- `internal/executive/runtimeadapter/registry.go:19` — `(a Registry).CurrentRevision`
- `internal/executive/runtimeadapter/registry.go:30` — `(a Registry).GetUnit`
- `internal/executive/runtimeadapter/registry.go:48` — `(a Registry).GetRole`
- `internal/executive/runtimeadapter/registry.go:56` — `(a Registry).GetLeader`
- `internal/executive/runtimeadapter/registry.go:64` — `mapRole`
- `internal/executive/runtimeadapter/registry.go:81` — `(a Assignment).ResolveAssignment`
- `internal/executive/runtimeadapter/registry.go:100` — `(a Completion).Verify`
- `internal/executive/runtimeadapter/registry.go:122` — `(a Authorization).Evaluate`
- `internal/executive/runtimeadapter/registry.go:143` — `ValidateStaticDependencies`
- `internal/executive/runtimeadapter/roots.go:10` — `(a Tasks).ListExecutableRoots`
- `internal/executive/runtimeadapter/roots.go:44` — `isExecutiveRoot`
- `internal/executive/runtimeadapter/tasks.go:18` — `(a Tasks).CreateTask`
- `internal/executive/runtimeadapter/tasks.go:61` — `(a Tasks).AddDependency`
- `internal/executive/runtimeadapter/tasks.go:70` — `(a Tasks).GetTask`
- `internal/executive/runtimeadapter/tasks.go:78` — `(a Tasks).ListByCorrelation`
- `internal/executive/runtimeadapter/tasks.go:98` — `(a Tasks).ListAwaitingGating`
- `internal/executive/runtimeadapter/tasks.go:121` — `(a Tasks).ClaimTask`
- `internal/executive/runtimeadapter/tasks.go:144` — `(a Tasks).StartAttempt`
- `internal/executive/runtimeadapter/tasks.go:157` — `(a Tasks).Heartbeat`
- `internal/executive/runtimeadapter/tasks.go:177` — `(a Tasks).RecordAttemptSucceeded`
- `internal/executive/runtimeadapter/tasks.go:193` — `(a Tasks).RecordAttemptFailed`
- `internal/executive/runtimeadapter/tasks.go:213` — `(a Tasks).RecordEvidence`
- `internal/executive/runtimeadapter/tasks.go:232` — `(a Tasks).FinalizeCompleted`
- `internal/executive/runtimeadapter/tasks.go:240` — `(a Tasks).FinalizeFailed`
- `internal/executive/runtimeadapter/tasks.go:248` — `(a Tasks).BlockTask`
- `internal/executive/runtimeadapter/tasks.go:256` — `(a Tasks).UnblockTask`
- `internal/executive/runtimeadapter/tasks.go:264` — `(a Tasks).Reconcile`
- `internal/executive/runtimeadapter/tasks.go:269` — `mapTaskDetail`
- `internal/executive/runtimeadapter/tasks.go:353` — `mapAttempt`
- `internal/executive/sleep/candidate.go:73` — `BuildCandidate`
- `internal/executive/sleep/candidate.go:186` — `evidenceSetHash`
- `internal/executive/sleep/candidate.go:198` — `sha256Hex`
- `internal/executive/sleep/candidate.go:203` — `compactTitle`
- `internal/executive/sleep/grouping.go:13` — `GroupExperiences`
- `internal/executive/sleep/grouping.go:34` — `AnalyzeGroup`
- `internal/executive/sleep/grouping.go:60` — `RecurringGroups`
- `internal/executive/sleep/grouping.go:70` — `PortabilityFor`
- `internal/executive/sleep/grouping.go:125` — `passBand`
- `internal/executive/sleep/grouping.go:136` — `Confidence`
- `internal/executive/sleep/grouping.go:155` — `dedupeExperiences`
- `internal/executive/sleep/grouping.go:168` — `round6`
- `internal/executive/sleep/grouping.go:169` — `minInt`
- `internal/executive/sleep/grouping.go:175` — `maxInt`
- `internal/executive/sleep/ports.go:33` — `(f ClockFunc).Now`
- `internal/executive/sleep/postgres.go:30` — `NewPostgresReader`
- `internal/executive/sleep/postgres.go:40` — `(r *PostgresReader).ListEligible`
- `internal/executive/sleep/service.go:18` — `NewService`
- `internal/executive/sleep/service.go:34` — `(s *Service).RunCycle`
- `internal/executive/sleep/types.go:38` — `(e Experience).Validate`
- `internal/executive/sleep/types.go:64` — `validVerificationLabel`
- `internal/executive/sleep/types.go:73` — `successfulLabel`
- `internal/executive/sleep/types.go:84` — `(k GroupKey).String`
- `internal/executive/sleep/types.go:93` — `(g Group).Sorted`
- `internal/executive/sleep/types.go:174` — `DefaultConfig`
- `internal/executive/sleep/types.go:181` — `(c Config).Validate`
- `internal/executive/smoke/hardening.go:70` — `WireToolkit`
- `internal/executive/smoke/hardening.go:124` — `Preflight`
- `internal/executive/smoke/hardening.go:184` — `Cleanup`
- `internal/executive/smoke/hardening.go:318` — `Execute`
- `internal/executive/smoke/smoke.go:65` — `Wire`
- `internal/executive/smoke/smoke.go:123` — `NewCorrelationID`
- `internal/executive/smoke/smoke.go:152` — `precheckRoleInboxesQuiescent`
- `internal/executive/smoke/smoke.go:186` — `Run`
- `internal/executive/smoke/smoke.go:246` — `principalAlreadyActive`
- `internal/executive/smoke/smoke.go:268` — `createSupportTask`
- `internal/executive/smoke/smoke.go:312` — `sha256Hex`
- `internal/executive/smoke/smoke.go:360` — `Verify`
- `internal/executive/smoke/smoke.go:478` — `Deliver`
- `internal/executive/types.go:36` — `(s RunState).Terminal`
- `internal/executive/types.go:141` — `DefaultLimits`
- `internal/executive/validator.go:18` — `NewValidator`
- `internal/executive/validator.go:28` — `(v *Validator).ValidateExecutivePlan`
- `internal/executive/validator.go:72` — `(v *Validator).ValidateDepartmentPlan`
- `internal/executive/validator.go:130` — `(v *Validator).ValidateFollowups`
- `internal/executive/validator.go:134` — `roleAssignable`
- `internal/executive/validator.go:136` — `dependencyCycle`
- `internal/executive/validator.go:172` — `actionDigest`
- `internal/executive/worker.go:19` — `DefaultWorkerConfig`
- `internal/executive/worker.go:29` — `NewWorker`
- `internal/executive/worker.go:45` — `(w *Worker).RunOnce`
- `internal/executive/worker.go:67` — `(w *Worker).Run`
- `internal/executive/worker.go:84` — `sleepContext`
- `internal/executive/worker_result.go:13` — `ParseWorkerResult`

## internal/identifiers

Tracked artifacts: 2; kinds: go_source=1, test_only_signal=1

Production Go paths:
- `internal/identifiers/identifiers.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/identifiers/identifiers.go:28` — `ExtractDigitRuns`

## internal/improvement

Tracked artifacts: 11; kinds: go_source=9, test_only_signal=2

Production Go paths:
- `internal/improvement/doc.go`
- `internal/improvement/errors.go`
- `internal/improvement/fake.go`
- `internal/improvement/hashing.go`
- `internal/improvement/ports.go`
- `internal/improvement/postgres/store.go`
- `internal/improvement/service.go`
- `internal/improvement/transitions.go`
- `internal/improvement/types.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/improvement/fake.go:16` — `NewFakeApprovalGate`
- `internal/improvement/fake.go:18` — `(f *FakeApprovalGate).SetDecide`
- `internal/improvement/fake.go:24` — `(f *FakeApprovalGate).AuthorizePromotion`
- `internal/improvement/fake.go:49` — `NewFakeClock`
- `internal/improvement/fake.go:51` — `(f *FakeClock).Set`
- `internal/improvement/fake.go:57` — `(f *FakeClock).Now`
- `internal/improvement/hashing.go:18` — `(c Candidate).CanonicalHash`
- `internal/improvement/ports.go:47` — `(SystemClock).Now`
- `internal/improvement/postgres/store.go:28` — `New`
- `internal/improvement/postgres/store.go:41` — `(s *Store).ProposeCandidate`
- `internal/improvement/postgres/store.go:74` — `(s *Store).GetCandidate`
- `internal/improvement/postgres/store.go:97` — `(s *Store).SaveCandidate`
- `internal/improvement/postgres/store.go:134` — `(s *Store).RecordPromotionDecision`
- `internal/improvement/postgres/store.go:175` — `scanCandidate`
- `internal/improvement/postgres/store.go:216` — `stringOrEmpty`
- `internal/improvement/postgres/store.go:223` — `nullableString`
- `internal/improvement/postgres/store.go:230` — `isUniqueViolation`
- `internal/improvement/service.go:19` — `NewService`
- `internal/improvement/service.go:30` — `(s *Service).ProposeCandidate`
- `internal/improvement/service.go:60` — `(s *Service).transition`
- `internal/improvement/service.go:78` — `(s *Service).ValidateCandidate`
- `internal/improvement/service.go:83` — `(s *Service).BeginEvaluation`
- `internal/improvement/service.go:90` — `(s *Service).RecordEvaluationVerdict`
- `internal/improvement/service.go:110` — `(s *Service).requestPromotion`
- `internal/improvement/service.go:155` — `(s *Service).PromoteToCanary`
- `internal/improvement/service.go:160` — `(s *Service).PromoteToActive`
- `internal/improvement/service.go:166` — `(s *Service).Deprecate`
- `internal/improvement/service.go:172` — `(s *Service).RollBack`
- `internal/improvement/transitions.go:35` — `ValidateCandidateTransition`
- `internal/improvement/types.go:19` — `(a ArtifactRef).Validate`
- `internal/improvement/types.go:40` — `(l Lineage).Validate`
- `internal/improvement/types.go:50` — `(l Lineage).IsRoot`
- `internal/improvement/types.go:69` — `(s CandidateState).Valid`
- `internal/improvement/types.go:81` — `(s CandidateState).Terminal`
- `internal/improvement/types.go:100` — `(r RollbackTarget).Validate`
- `internal/improvement/types.go:126` — `(c Candidate).Validate`
- `internal/improvement/types.go:168` — `(k PromotionKind).Valid`
- `internal/improvement/types.go:179` — `(k PromotionKind).expectedTransition`
- `internal/improvement/types.go:207` — `(r PromotionRequest).Validate`
- `internal/improvement/types.go:244` — `(o PromotionOutcome).Valid`
- `internal/improvement/types.go:263` — `(d PromotionDecision).Validate`

## internal/logicir

Tracked artifacts: 2; kinds: go_source=1, test_only_signal=1

Production Go paths:
- `internal/logicir/logicir.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/logicir/logicir.go:66` — `(f Fact).validate`
- `internal/logicir/logicir.go:93` — `(r Rule).validate`
- `internal/logicir/logicir.go:123` — `(p Program).Validate`
- `internal/logicir/logicir.go:164` — `DefaultLimits`
- `internal/logicir/logicir.go:168` — `(l Limits).Validate`
- `internal/logicir/logicir.go:199` — `(e ComparisonEvent).Validate`
- `internal/logicir/logicir.go:234` — `NewDivergence`

## internal/memory

Tracked artifacts: 25; kinds: go_source=17, test_only_signal=8

Production Go paths:
- `internal/memory/authz/gate.go`
- `internal/memory/bootstrap/bootstrap.go`
- `internal/memory/contextprovider/provider.go`
- `internal/memory/doc.go`
- `internal/memory/errors.go`
- `internal/memory/hashing.go`
- `internal/memory/manager.go`
- `internal/memory/ports.go`
- `internal/memory/postgres/backfill.go`
- `internal/memory/postgres/embeddings.go`
- `internal/memory/postgres/embeddings_bge_m3.go`
- `internal/memory/postgres/search.go`
- `internal/memory/postgres/store.go`
- `internal/memory/semantic.go`
- `internal/memory/service.go`
- `internal/memory/transitions.go`
- `internal/memory/types.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/memory/authz/gate.go:28` — `New`
- `internal/memory/authz/gate.go:42` — `(g *Gate).Authorize`
- `internal/memory/bootstrap/bootstrap.go:52` — `activeEmbeddingProfile`
- `internal/memory/bootstrap/bootstrap.go:73` — `Open`
- `internal/memory/bootstrap/bootstrap.go:118` — `openSemanticSearch`
- `internal/memory/bootstrap/bootstrap.go:131` — `sharedSpendControls`
- `internal/memory/bootstrap/bootstrap.go:151` — `openGeminiSemanticSearch`
- `internal/memory/bootstrap/bootstrap.go:191` — `openBGEM3SemanticSearch`
- `internal/memory/contextprovider/provider.go:23` — `New`
- `internal/memory/contextprovider/provider.go:48` — `(p *Provider).ListApproved`
- `internal/memory/contextprovider/provider.go:98` — `(p *Provider).ValidateVersion`
- `internal/memory/contextprovider/provider.go:136` — `sourceRecord`
- `internal/memory/contextprovider/provider.go:167` — `mapDataClass`
- `internal/memory/hashing.go:16` — `(e Entry).CanonicalHash`
- `internal/memory/manager.go:44` — `NewManager`
- `internal/memory/manager.go:78` — `(m *Manager).Propose`
- `internal/memory/manager.go:103` — `(m *Manager).Review`
- `internal/memory/manager.go:133` — `(m *Manager).Deprecate`
- `internal/memory/manager.go:148` — `(m *Manager).Archive`
- `internal/memory/manager.go:165` — `(m *Manager).Get`
- `internal/memory/manager.go:200` — `(m *Manager).List`
- `internal/memory/manager.go:222` — `(m *Manager).GetForRevalidation`
- `internal/memory/manager.go:272` — `(m *Manager).Search`
- `internal/memory/manager.go:337` — `backfillDigest`
- `internal/memory/manager.go:353` — `(m *Manager).BackfillEmbeddings`
- `internal/memory/manager.go:438` — `(m *Manager).ListApproved`
- `internal/memory/manager.go:451` — `(m *Manager).loadMutationTarget`
- `internal/memory/manager.go:471` — `(m *Manager).authorizeMutation`
- `internal/memory/manager.go:486` — `mutationDigest`
- `internal/memory/ports.go:115` — `(id EmbeddingIdentity).Validate`
- `internal/memory/postgres/backfill.go:22` — `(s *Store).PendingEntryEmbeddings`
- `internal/memory/postgres/embeddings.go:23` — `encodeVector`
- `internal/memory/postgres/embeddings.go:39` — `(s *Store).InsertEntryEmbedding`
- `internal/memory/postgres/embeddings.go:70` — `(s *Store).NearestEntries`
- `internal/memory/postgres/embeddings_bge_m3.go:19` — `encodeVectorBGEM3`
- `internal/memory/postgres/embeddings_bge_m3.go:35` — `(s *Store).InsertBGEM3EntryEmbedding`
- `internal/memory/postgres/embeddings_bge_m3.go:68` — `(s *Store).NearestBGEM3Entries`
- `internal/memory/postgres/search.go:23` — `vectorChannelClause`
- `internal/memory/postgres/search.go:63` — `rrfCandidatePoolSize`
- `internal/memory/postgres/search.go:81` — `(s *Store).Search`
- `internal/memory/postgres/store.go:24` — `New`
- `internal/memory/postgres/store.go:37` — `(s *Store).CreateCandidate`
- `internal/memory/postgres/store.go:129` — `insertVersion`
- `internal/memory/postgres/store.go:141` — `(s *Store).Get`
- `internal/memory/postgres/store.go:153` — `(s *Store).Save`
- `internal/memory/postgres/store.go:212` — `(s *Store).List`
- `internal/memory/postgres/store.go:251` — `(s *Store).ListApproved`
- `internal/memory/postgres/store.go:265` — `getEntry`
- `internal/memory/postgres/store.go:312` — `lookupIdempotency`
- `internal/memory/postgres/store.go:323` — `insertIdempotency`
- `internal/memory/postgres/store.go:340` — `normalizeLimit`
- `internal/memory/postgres/store.go:349` — `nullableString`
- `internal/memory/postgres/store.go:356` — `stringOrEmpty`
- `internal/memory/postgres/store.go:362` — `mapError`
- `internal/memory/semantic.go:50` — `(d *SemanticSearchDeps).validate`
- `internal/memory/semantic.go:80` — `(m *Manager).embed`
- `internal/memory/semantic.go:184` — `(m *Manager).embedApprovedEntry`
- `internal/memory/service.go:15` — `(SystemClock).Now`
- `internal/memory/service.go:19` — `NewService`
- `internal/memory/service.go:41` — `(s *Service).Propose`
- `internal/memory/service.go:90` — `(s *Service).transition`
- `internal/memory/service.go:106` — `(s *Service).Review`
- `internal/memory/service.go:132` — `(s *Service).Deprecate`
- `internal/memory/service.go:143` — `(s *Service).Archive`
- `internal/memory/transitions.go:22` — `ValidateTransition`
- `internal/memory/types.go:21` — `(s Status).Valid`
- `internal/memory/types.go:40` — `(c DataClass).Valid`
- `internal/memory/types.go:49` — `(c DataClass).AllowedInOrganizationalMemory`
- `internal/memory/types.go:70` — `(k SourceKind).Valid`
- `internal/memory/types.go:84` — `(e EvidenceRef).Validate`
- `internal/memory/types.go:108` — `(a AdmissionAttestation).Validate`
- `internal/memory/types.go:157` — `(e Entry).Validate`
- `internal/memory/types.go:232` — `(o ReviewOutcome).Valid`
- `internal/memory/types.go:239` — `(r Review).Validate`

## internal/modeldispatch

Tracked artifacts: 20; kinds: go_source=15, test_only_signal=5

Production Go paths:
- `internal/modeldispatch/assignment_service.go`
- `internal/modeldispatch/bootstrap/runtime.go`
- `internal/modeldispatch/config.go`
- `internal/modeldispatch/domain.go`
- `internal/modeldispatch/errors.go`
- `internal/modeldispatch/events.go`
- `internal/modeldispatch/hashing.go`
- `internal/modeldispatch/interfaces.go`
- `internal/modeldispatch/postgres/assignments.go`
- `internal/modeldispatch/postgres/errors.go`
- `internal/modeldispatch/postgres/principals.go`
- `internal/modeldispatch/postgres/scans.go`
- `internal/modeldispatch/postgres/store.go`
- `internal/modeldispatch/principal_service.go`
- `internal/modeldispatch/validation.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/modeldispatch/assignment_service.go:22` — `NewAssignmentService`
- `internal/modeldispatch/assignment_service.go:35` — `(s *AssignmentService).Create`
- `internal/modeldispatch/assignment_service.go:103` — `(s *AssignmentService).Get`
- `internal/modeldispatch/assignment_service.go:110` — `(s *AssignmentService).List`
- `internal/modeldispatch/assignment_service.go:123` — `(s *AssignmentService).Revoke`
- `internal/modeldispatch/assignment_service.go:139` — `(s *AssignmentService).Expire`
- `internal/modeldispatch/bootstrap/runtime.go:25` — `Open`
- `internal/modeldispatch/bootstrap/runtime.go:60` — `(a catalogAdapter).CurrentRevision`
- `internal/modeldispatch/bootstrap/runtime.go:71` — `(a catalogAdapter).GetRole`
- `internal/modeldispatch/bootstrap/runtime.go:81` — `(a taskAdapter).GetTaskAttempt`
- `internal/modeldispatch/config.go:25` — `LoadDispatchConfig`
- `internal/modeldispatch/config.go:46` — `(c DispatchConfig).Validate`
- `internal/modeldispatch/config.go:62` — `envInt`
- `internal/modeldispatch/config.go:74` — `envDuration`
- `internal/modeldispatch/domain.go:28` — `(s AssignmentStatus).Terminal`
- `internal/modeldispatch/hashing.go:10` — `sha256Hex`
- `internal/modeldispatch/hashing.go:15` — `formatTime`
- `internal/modeldispatch/hashing.go:17` — `PrincipalRequestHash`
- `internal/modeldispatch/hashing.go:31` — `AssignmentRequestHash`
- `internal/modeldispatch/hashing.go:55` — `AssignmentScopeHash`
- `internal/modeldispatch/hashing.go:74` — `UsageHash`
- `internal/modeldispatch/interfaces.go:11` — `(f ClockFunc).Now`
- `internal/modeldispatch/postgres/assignments.go:14` — `insertAssignmentAudit`
- `internal/modeldispatch/postgres/assignments.go:39` — `(s *Store).CreateAssignment`
- `internal/modeldispatch/postgres/assignments.go:76` — `(s *Store).GetAssignment`
- `internal/modeldispatch/postgres/assignments.go:80` — `(s *Store).ListAssignments`
- `internal/modeldispatch/postgres/assignments.go:97` — `(s *Store).RevokeAssignment`
- `internal/modeldispatch/postgres/assignments.go:125` — `(s *Store).ExpireAssignments`
- `internal/modeldispatch/postgres/assignments.go:167` — `(s *Store).ResolveActive`
- `internal/modeldispatch/postgres/assignments.go:179` — `(s *Store).GetByID`
- `internal/modeldispatch/postgres/assignments.go:187` — `(s *Store).withPrincipal`
- `internal/modeldispatch/postgres/errors.go:14` — `mapError`
- `internal/modeldispatch/postgres/principals.go:13` — `insertPrincipalAudit`
- `internal/modeldispatch/postgres/principals.go:32` — `(s *Store).RegisterPrincipal`
- `internal/modeldispatch/postgres/principals.go:67` — `(s *Store).GetPrincipal`
- `internal/modeldispatch/postgres/principals.go:71` — `(s *Store).ListPrincipals`
- `internal/modeldispatch/postgres/principals.go:88` — `(s *Store).DisablePrincipal`
- `internal/modeldispatch/postgres/principals.go:112` — `(s *Store).ResolveByKey`
- `internal/modeldispatch/postgres/principals.go:134` — `(s *Store).ResolveActiveForRole`
- `internal/modeldispatch/postgres/scans.go:11` — `scanPrincipal`
- `internal/modeldispatch/postgres/scans.go:29` — `scanAssignment`
- `internal/modeldispatch/postgres/store.go:15` — `New`
- `internal/modeldispatch/principal_service.go:25` — `NewPrincipalService`
- `internal/modeldispatch/principal_service.go:35` — `(s *PrincipalService).Register`
- `internal/modeldispatch/principal_service.go:72` — `(s *PrincipalService).Get`
- `internal/modeldispatch/principal_service.go:79` — `(s *PrincipalService).List`
- `internal/modeldispatch/principal_service.go:92` — `(s *PrincipalService).Disable`
- `internal/modeldispatch/validation.go:18` — `validPrincipalKind`
- `internal/modeldispatch/validation.go:27` — `validReasonCode`
- `internal/modeldispatch/validation.go:31` — `PrepareRegisterCommand`
- `internal/modeldispatch/validation.go:54` — `PrepareCreateAssignmentCommand`
- `internal/modeldispatch/validation.go:99` — `validateTaskAttemptForAssignment`
- `internal/modeldispatch/validation.go:121` — `eligibleDispatchActorRole`

## internal/modelegress

Tracked artifacts: 18; kinds: go_source=12, test_only_signal=6

Production Go paths:
- `internal/modelegress/bootstrap/runtime.go`
- `internal/modelegress/canonical_policy.go`
- `internal/modelegress/domain.go`
- `internal/modelegress/errors.go`
- `internal/modelegress/evaluator.go`
- `internal/modelegress/events.go`
- `internal/modelegress/executive_scope.go`
- `internal/modelegress/hashing.go`
- `internal/modelegress/interfaces.go`
- `internal/modelegress/postgres/store.go`
- `internal/modelegress/service.go`
- `internal/modelegress/validation.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/modelegress/bootstrap/runtime.go:22` — `Open`
- `internal/modelegress/bootstrap/runtime.go:43` — `(a organizationAdapter).CurrentOrganization`
- `internal/modelegress/bootstrap/runtime.go:60` — `(providerAdapter).ProviderIDs`
- `internal/modelegress/canonical_policy.go:52` — `ProductiveLoadOptions`
- `internal/modelegress/canonical_policy.go:77` — `LoadCanonicalPolicy`
- `internal/modelegress/canonical_policy.go:117` — `decodeStrictPolicyYAML`
- `internal/modelegress/canonical_policy.go:151` — `validatePolicyYAMLNode`
- `internal/modelegress/canonical_policy.go:195` — `policyRulesToDomain`
- `internal/modelegress/canonical_policy.go:203` — `normalizePolicyDocument`
- `internal/modelegress/canonical_policy.go:228` — `validatePolicyDocument`
- `internal/modelegress/canonical_policy.go:298` — `productiveAllowApproved`
- `internal/modelegress/canonical_policy.go:311` — `validDeclaredClassification`
- `internal/modelegress/canonical_policy.go:320` — `validReasonCode`
- `internal/modelegress/evaluator.go:10` — `NewEvaluator`
- `internal/modelegress/evaluator.go:12` — `(e *Evaluator).Evaluate`
- `internal/modelegress/evaluator.go:92` — `validRuntimeClassification`
- `internal/modelegress/executive_scope.go:14` — `ExecutiveScopeMarker`
- `internal/modelegress/executive_scope.go:39` — `scopedDepartmentRole`
- `internal/modelegress/executive_scope.go:47` — `scopeRequired`
- `internal/modelegress/executive_scope.go:61` — `scopeAllows`
- `internal/modelegress/executive_scope.go:82` — `scopeVerifiedReason`
- `internal/modelegress/executive_scope.go:99` — `ValidateExecutiveScope`
- `internal/modelegress/hashing.go:12` — `SHA256Bytes`
- `internal/modelegress/hashing.go:17` — `InvocationActionDigest`
- `internal/modelegress/hashing.go:34` — `NormalizeClassifications`
- `internal/modelegress/hashing.go:57` — `NormalizeReasonCodes`
- `internal/modelegress/hashing.go:73` — `DecisionHash`
- `internal/modelegress/postgres/store.go:24` — `expectedMaterializedRules`
- `internal/modelegress/postgres/store.go:41` — `materializedRules`
- `internal/modelegress/postgres/store.go:62` — `equalRules`
- `internal/modelegress/postgres/store.go:74` — `New`
- `internal/modelegress/postgres/store.go:81` — `mapError`
- `internal/modelegress/postgres/store.go:124` — `(s *Store).RecordValidated`
- `internal/modelegress/postgres/store.go:132` — `(s *Store).Status`
- `internal/modelegress/postgres/store.go:172` — `(s *Store).Apply`
- `internal/modelegress/postgres/store.go:309` — `(s *Store).ResolveForRevision`
- `internal/modelegress/service.go:17` — `NewService`
- `internal/modelegress/service.go:24` — `(s *Service).load`
- `internal/modelegress/service.go:44` — `(s *Service).Validate`
- `internal/modelegress/service.go:55` — `(s *Service).Diff`
- `internal/modelegress/service.go:63` — `(s *Service).Status`
- `internal/modelegress/service.go:71` — `(s *Service).Sync`
- `internal/modelegress/validation.go:11` — `ValidateRegistryPlan`
- `internal/modelegress/validation.go:72` — `ValidatePreSendEvaluation`
- `internal/modelegress/validation.go:142` — `ValidatePersistAllowCommand`
- `internal/modelegress/validation.go:165` — `ValidatePersistFailureCommand`
- `internal/modelegress/validation.go:181` — `validProviderTransport`
- `internal/modelegress/validation.go:190` — `equalStrings`

## internal/modelidentity

Tracked artifacts: 21; kinds: go_source=17, test_only_signal=4

Production Go paths:
- `internal/modelidentity/bootstrap/runtime.go`
- `internal/modelidentity/canonical_policy.go`
- `internal/modelidentity/challenge_service.go`
- `internal/modelidentity/crypto.go`
- `internal/modelidentity/domain.go`
- `internal/modelidentity/errors.go`
- `internal/modelidentity/events.go`
- `internal/modelidentity/hashing.go`
- `internal/modelidentity/interfaces.go`
- `internal/modelidentity/key_service.go`
- `internal/modelidentity/policy_service.go`
- `internal/modelidentity/postgres/audit.go`
- `internal/modelidentity/postgres/challenges.go`
- `internal/modelidentity/postgres/keys.go`
- `internal/modelidentity/postgres/policy.go`
- `internal/modelidentity/postgres/scans.go`
- `internal/modelidentity/postgres/store.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/modelidentity/bootstrap/runtime.go:25` — `Open`
- `internal/modelidentity/bootstrap/runtime.go:63` — `(a catalogAdapter).CurrentRevision`
- `internal/modelidentity/bootstrap/runtime.go:74` — `(a catalogAdapter).GetRole`
- `internal/modelidentity/canonical_policy.go:39` — `LoadCanonicalPolicy`
- `internal/modelidentity/canonical_policy.go:91` — `validateYAML`
- `internal/modelidentity/canonical_policy.go:101` — `validateNode`
- `internal/modelidentity/challenge_service.go:15` — `NewChallengeService`
- `internal/modelidentity/challenge_service.go:25` — `(s *ChallengeService).ResolvePolicy`
- `internal/modelidentity/challenge_service.go:29` — `(s *ChallengeService).ResolvePolicyByID`
- `internal/modelidentity/challenge_service.go:33` — `(s *ChallengeService).ResolveActiveKeyByFingerprint`
- `internal/modelidentity/challenge_service.go:37` — `(s *ChallengeService).Issue`
- `internal/modelidentity/challenge_service.go:66` — `(s *ChallengeService).Verify`
- `internal/modelidentity/crypto.go:12` — `DecodePublicKey`
- `internal/modelidentity/crypto.go:23` — `NewNonce`
- `internal/modelidentity/crypto.go:31` — `VerifyAssertion`
- `internal/modelidentity/hashing.go:10` — `SHA256Bytes`
- `internal/modelidentity/hashing.go:15` — `PublicKeyFingerprint`
- `internal/modelidentity/hashing.go:17` — `KeyRequestHash`
- `internal/modelidentity/hashing.go:32` — `BuildAssertionPayload`
- `internal/modelidentity/hashing.go:54` — `canonicalAssertionTime`
- `internal/modelidentity/hashing.go:61` — `AssertionHash`
- `internal/modelidentity/hashing.go:69` — `formatOptionalTime`
- `internal/modelidentity/interfaces.go:13` — `(f ClockFunc).Now`
- `internal/modelidentity/key_service.go:20` — `validOpaqueSecretRef`
- `internal/modelidentity/key_service.go:36` — `validIdempotencyKey`
- `internal/modelidentity/key_service.go:57` — `NewKeyService`
- `internal/modelidentity/key_service.go:67` — `(s *KeyService).prepare`
- `internal/modelidentity/key_service.go:120` — `(s *KeyService).Register`
- `internal/modelidentity/key_service.go:128` — `(s *KeyService).Rotate`
- `internal/modelidentity/key_service.go:136` — `(s *KeyService).Get`
- `internal/modelidentity/key_service.go:143` — `(s *KeyService).List`
- `internal/modelidentity/key_service.go:153` — `(s *KeyService).Retire`
- `internal/modelidentity/key_service.go:168` — `(s *KeyService).Revoke`
- `internal/modelidentity/policy_service.go:14` — `NewPolicyService`
- `internal/modelidentity/policy_service.go:21` — `(s *PolicyService).Validate`
- `internal/modelidentity/policy_service.go:25` — `(s *PolicyService).Status`
- `internal/modelidentity/policy_service.go:33` — `(s *PolicyService).Diff`
- `internal/modelidentity/policy_service.go:35` — `(s *PolicyService).Sync`
- `internal/modelidentity/postgres/audit.go:11` — `insertAudit`
- `internal/modelidentity/postgres/audit.go:20` — `subjectID`
- `internal/modelidentity/postgres/challenges.go:10` — `(s *Store).CreateChallenge`
- `internal/modelidentity/postgres/challenges.go:37` — `(s *Store).GetChallenge`
- `internal/modelidentity/postgres/keys.go:11` — `insertKeyAudit`
- `internal/modelidentity/postgres/keys.go:19` — `(s *Store).RegisterKey`
- `internal/modelidentity/postgres/keys.go:49` — `(s *Store).RotateKey`
- `internal/modelidentity/postgres/keys.go:85` — `(s *Store).GetKey`
- `internal/modelidentity/postgres/keys.go:89` — `(s *Store).ListKeys`
- `internal/modelidentity/postgres/keys.go:115` — `(s *Store).RetireKey`
- `internal/modelidentity/postgres/keys.go:138` — `(s *Store).RevokeKey`
- `internal/modelidentity/postgres/keys.go:161` — `(s *Store).ResolveActiveKeyByFingerprint`
- `internal/modelidentity/postgres/policy.go:11` — `(s *Store).Status`
- `internal/modelidentity/postgres/policy.go:26` — `(s *Store).Apply`
- `internal/modelidentity/postgres/policy.go:67` — `(s *Store).ResolveActive`
- `internal/modelidentity/postgres/policy.go:75` — `(s *Store).ResolveByID`
- `internal/modelidentity/postgres/scans.go:16` — `scanPolicy`
- `internal/modelidentity/postgres/scans.go:28` — `scanKey`
- `internal/modelidentity/postgres/scans.go:47` — `scanChallenge`
- `internal/modelidentity/postgres/store.go:18` — `New`
- `internal/modelidentity/postgres/store.go:25` — `mapError`

## internal/modelpricing

Tracked artifacts: 8; kinds: go_source=6, test_only_signal=2

Production Go paths:
- `internal/modelpricing/doc.go`
- `internal/modelpricing/errors.go`
- `internal/modelpricing/ports.go`
- `internal/modelpricing/postgres/store.go`
- `internal/modelpricing/resolve.go`
- `internal/modelpricing/types.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/modelpricing/postgres/store.go:18` — `New`
- `internal/modelpricing/postgres/store.go:27` — `(s *Store).ListTiers`
- `internal/modelpricing/postgres/store.go:74` — `(s *Store).Upsert`
- `internal/modelpricing/resolve.go:29` — `Resolve`
- `internal/modelpricing/resolve.go:62` — `NewService`
- `internal/modelpricing/resolve.go:69` — `(s *Service).Resolve`
- `internal/modelpricing/resolve.go:77` — `(s *Service).Upsert`
- `internal/modelpricing/types.go:33` — `(m BillingMode).Valid`
- `internal/modelpricing/types.go:44` — `(n USDNanos).USD`
- `internal/modelpricing/types.go:49` — `USDFromDollars`
- `internal/modelpricing/types.go:53` — `(n USDNanos).String`
- `internal/modelpricing/types.go:74` — `(t PriceTier).Validate`
- `internal/modelpricing/types.go:107` — `(t PriceTier).EstimateCost`
- `internal/modelpricing/types.go:144` — `scaleNanos`

## internal/modelruntime

Tracked artifacts: 84; kinds: go_source=57, test_only_signal=27

Production Go paths:
- `internal/modelruntime/adapter/alibabaclaude/adapter.go`
- `internal/modelruntime/adapter/alibabaclaude/config.go`
- `internal/modelruntime/adapter/alibabaclaude/host_config.go`
- `internal/modelruntime/adapter/alibabaclaude/preflight.go`
- `internal/modelruntime/adapter/alibabaclaude/process_unix.go`
- `internal/modelruntime/adapter/alibabaclaude/response.go`
- `internal/modelruntime/adapter/alibabaclaude/settings.go`
- `internal/modelruntime/adapter/deepseek/adapter.go`
- `internal/modelruntime/adapter/deepseek/breaker.go`
- `internal/modelruntime/adapter/deepseek/config.go`
- `internal/modelruntime/adapter/fake.go`
- `internal/modelruntime/adapter/gemini/adapter.go`
- `internal/modelruntime/adapter/gemini/breaker.go`
- `internal/modelruntime/adapter/gemini/config.go`
- `internal/modelruntime/adapter/mimo/adapter.go`
- `internal/modelruntime/adapter/mimo/breaker.go`
- `internal/modelruntime/adapter/mimo/config.go`
- `internal/modelruntime/adapter/openaicompat/adapter.go`
- `internal/modelruntime/adapter/openaicompat/breaker.go`
- `internal/modelruntime/adapter/openaicompat/config.go`
- `internal/modelruntime/adapter/openairesponses/adapter.go`
- `internal/modelruntime/adapter/openairesponses/breaker.go`
- `internal/modelruntime/adapter/openairesponses/config.go`
- `internal/modelruntime/adapter/registry.go`
- `internal/modelruntime/bootstrap/coordinator.go`
- `internal/modelruntime/bootstrap/runtime.go`
- `internal/modelruntime/canonical_routing.go`
- `internal/modelruntime/compiled_availability_r21.go`
- `internal/modelruntime/config.go`
- `internal/modelruntime/costgate.go`
- `internal/modelruntime/costgate/gate.go`
- `internal/modelruntime/dispatch_service.go`
- `internal/modelruntime/domain.go`
- `internal/modelruntime/errors.go`
- `internal/modelruntime/events.go`
- `internal/modelruntime/hashing.go`
- `internal/modelruntime/interfaces.go`
- `internal/modelruntime/invocation_service.go`
- `internal/modelruntime/normalizer.go`
- `internal/modelruntime/postgres/cancellation.go`
- `internal/modelruntime/postgres/claims.go`
- `internal/modelruntime/postgres/errors.go`
- `internal/modelruntime/postgres/events.go`
- `internal/modelruntime/postgres/invocations.go`
- `internal/modelruntime/postgres/presend.go`
- `internal/modelruntime/postgres/reconcile.go`
- `internal/modelruntime/postgres/registry.go`
- `internal/modelruntime/postgres/render_telemetry.go`
- `internal/modelruntime/postgres/result_reader.go`
- `internal/modelruntime/postgres/results.go`
- `internal/modelruntime/postgres/scans.go`
- `internal/modelruntime/postgres/store.go`
- `internal/modelruntime/provider_adapter.go`
- `internal/modelruntime/provider_request.go`
- `internal/modelruntime/registry_service.go`
- `internal/modelruntime/result_reader.go`
- `internal/modelruntime/validation.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/modelruntime/adapter/alibabaclaude/adapter.go:37` — `New`
- `internal/modelruntime/adapter/alibabaclaude/adapter.go:39` — `newAdapter`
- `internal/modelruntime/adapter/alibabaclaude/adapter.go:66` — `(*Adapter).ProviderID`
- `internal/modelruntime/adapter/alibabaclaude/adapter.go:67` — `(a *Adapter).Descriptor`
- `internal/modelruntime/adapter/alibabaclaude/adapter.go:69` — `(a *Adapter).Preflight`
- `internal/modelruntime/adapter/alibabaclaude/adapter.go:85` — `(a *Adapter).Dispatch`
- `internal/modelruntime/adapter/alibabaclaude/adapter.go:156` — `(a *Adapter).arguments`
- `internal/modelruntime/adapter/alibabaclaude/adapter.go:183` — `parseCLIResponse`
- `internal/modelruntime/adapter/alibabaclaude/adapter.go:216` — `(a *Adapter).beforeRequest`
- `internal/modelruntime/adapter/alibabaclaude/adapter.go:223` — `(a *Adapter).ambiguous`
- `internal/modelruntime/adapter/alibabaclaude/adapter.go:232` — `(a *Adapter).effectiveTimeout`
- `internal/modelruntime/adapter/alibabaclaude/adapter.go:243` — `validEffort`
- `internal/modelruntime/adapter/alibabaclaude/adapter.go:252` — `classifyCLIError`
- `internal/modelruntime/adapter/alibabaclaude/config.go:47` — `LoadConfig`
- `internal/modelruntime/adapter/alibabaclaude/config.go:95` — `(c Config).Validate`
- `internal/modelruntime/adapter/alibabaclaude/config.go:142` — `validateRuntimePath`
- `internal/modelruntime/adapter/alibabaclaude/config.go:156` — `validSHA256`
- `internal/modelruntime/adapter/alibabaclaude/config.go:168` — `envBool`
- `internal/modelruntime/adapter/alibabaclaude/config.go:180` — `envInt`
- `internal/modelruntime/adapter/alibabaclaude/config.go:192` — `envDuration`
- `internal/modelruntime/adapter/alibabaclaude/host_config.go:24` — `validateClaudeGlobalConfig`
- `internal/modelruntime/adapter/alibabaclaude/preflight.go:20` — `validateInstallation`
- `internal/modelruntime/adapter/alibabaclaude/preflight.go:78` — `validateWorkDir`
- `internal/modelruntime/adapter/alibabaclaude/preflight.go:92` — `childEnvironment`
- `internal/modelruntime/adapter/alibabaclaude/process_unix.go:35` — `newBoundedBuffer`
- `internal/modelruntime/adapter/alibabaclaude/process_unix.go:46` — `(b *boundedBuffer).Write`
- `internal/modelruntime/adapter/alibabaclaude/process_unix.go:67` — `(b *boundedBuffer).Bytes`
- `internal/modelruntime/adapter/alibabaclaude/process_unix.go:73` — `(b *boundedBuffer).Overflow`
- `internal/modelruntime/adapter/alibabaclaude/process_unix.go:84` — `runCLI`
- `internal/modelruntime/adapter/alibabaclaude/process_unix.go:132` — `stopProcessGroup`
- `internal/modelruntime/adapter/alibabaclaude/process_unix.go:148` — `processExitCode`
- `internal/modelruntime/adapter/alibabaclaude/response.go:10` — `(r *cliJSONResponse).UnmarshalJSON`
- `internal/modelruntime/adapter/alibabaclaude/settings.go:39` — `validateSettingsFile`
- `internal/modelruntime/adapter/alibabaclaude/settings.go:95` — `validOpaqueTokenJSON`
- `internal/modelruntime/adapter/alibabaclaude/settings.go:108` — `validModelID`
- `internal/modelruntime/adapter/deepseek/adapter.go:84` — `validateCacheTokens`
- `internal/modelruntime/adapter/deepseek/adapter.go:130` — `requestTelemetry`
- `internal/modelruntime/adapter/deepseek/adapter.go:139` — `(t failureTelemetry).withDuration`
- `internal/modelruntime/adapter/deepseek/adapter.go:144` — `(t failureTelemetry).withResponseBytes`
- `internal/modelruntime/adapter/deepseek/adapter.go:149` — `(t failureTelemetry).withFinishReason`
- `internal/modelruntime/adapter/deepseek/adapter.go:159` — `(t failureTelemetry).withUsage`
- `internal/modelruntime/adapter/deepseek/adapter.go:174` — `(t failureTelemetry).withJSONDecodeFailure`
- `internal/modelruntime/adapter/deepseek/adapter.go:184` — `classifyJSONError`
- `internal/modelruntime/adapter/deepseek/adapter.go:199` — `jsonBoundaryFlags`
- `internal/modelruntime/adapter/deepseek/adapter.go:207` — `New`
- `internal/modelruntime/adapter/deepseek/adapter.go:211` — `newAdapter`
- `internal/modelruntime/adapter/deepseek/adapter.go:249` — `(*Adapter).ProviderID`
- `internal/modelruntime/adapter/deepseek/adapter.go:250` — `(a *Adapter).Descriptor`
- `internal/modelruntime/adapter/deepseek/adapter.go:252` — `(a *Adapter).Preflight`
- `internal/modelruntime/adapter/deepseek/adapter.go:273` — `(a *Adapter).Dispatch`
- `internal/modelruntime/adapter/deepseek/adapter.go:417` — `encodeRequest`
- `internal/modelruntime/adapter/deepseek/adapter.go:472` — `jsonObjectModeInstruction`
- `internal/modelruntime/adapter/deepseek/adapter.go:480` — `decodeContent`
- `internal/modelruntime/adapter/deepseek/adapter.go:504` — `readBounded`
- `internal/modelruntime/adapter/deepseek/adapter.go:515` — `parseProviderError`
- `internal/modelruntime/adapter/deepseek/adapter.go:531` — `normalizeProviderToken`
- `internal/modelruntime/adapter/deepseek/adapter.go:554` — `responseErrorOutcome`
- `internal/modelruntime/adapter/deepseek/adapter.go:570` — `(a *Adapter).notSentOutcome`
- `internal/modelruntime/adapter/deepseek/adapter.go:578` — `retryableStatus`
- `internal/modelruntime/adapter/deepseek/adapter.go:582` — `classifyTransportError`
- `internal/modelruntime/adapter/deepseek/adapter.go:592` — `bound`
- `internal/modelruntime/adapter/deepseek/breaker.go:16` — `newCircuitBreaker`
- `internal/modelruntime/adapter/deepseek/breaker.go:20` — `(b *circuitBreaker).allow`
- `internal/modelruntime/adapter/deepseek/breaker.go:34` — `(b *circuitBreaker).success`
- `internal/modelruntime/adapter/deepseek/breaker.go:41` — `(b *circuitBreaker).failure`
- `internal/modelruntime/adapter/deepseek/config.go:40` — `LoadConfig`
- `internal/modelruntime/adapter/deepseek/config.go:70` — `(c Config).Validate`
- `internal/modelruntime/adapter/deepseek/config.go:105` — `defaultHTTPClient`
- `internal/modelruntime/adapter/deepseek/config.go:127` — `envBool`
- `internal/modelruntime/adapter/deepseek/config.go:139` — `envInt`
- `internal/modelruntime/adapter/deepseek/config.go:151` — `envDuration`
- `internal/modelruntime/adapter/fake.go:14` — `NewFake`
- `internal/modelruntime/adapter/fake.go:15` — `(*Fake).ProviderID`
- `internal/modelruntime/adapter/fake.go:16` — `(*Fake).Descriptor`
- `internal/modelruntime/adapter/fake.go:25` — `(*Fake).Preflight`
- `internal/modelruntime/adapter/fake.go:34` — `(*Fake).Dispatch`
- `internal/modelruntime/adapter/gemini/adapter.go:79` — `New`
- `internal/modelruntime/adapter/gemini/adapter.go:83` — `newAdapter`
- `internal/modelruntime/adapter/gemini/adapter.go:121` — `(*Adapter).ProviderID`
- `internal/modelruntime/adapter/gemini/adapter.go:122` — `(a *Adapter).Descriptor`
- `internal/modelruntime/adapter/gemini/adapter.go:124` — `(a *Adapter).Preflight`
- `internal/modelruntime/adapter/gemini/adapter.go:145` — `(a *Adapter).Dispatch`
- `internal/modelruntime/adapter/gemini/adapter.go:256` — `encodeRequest`
- `internal/modelruntime/adapter/gemini/adapter.go:298` — `decodeContent`
- `internal/modelruntime/adapter/gemini/adapter.go:322` — `readBounded`
- `internal/modelruntime/adapter/gemini/adapter.go:333` — `parseProviderError`
- `internal/modelruntime/adapter/gemini/adapter.go:349` — `normalizeProviderToken`
- `internal/modelruntime/adapter/gemini/adapter.go:372` — `responseErrorOutcome`
- `internal/modelruntime/adapter/gemini/adapter.go:381` — `(a *Adapter).notSentOutcome`
- `internal/modelruntime/adapter/gemini/adapter.go:388` — `retryableStatus`
- `internal/modelruntime/adapter/gemini/adapter.go:392` — `classifyTransportError`
- `internal/modelruntime/adapter/gemini/adapter.go:402` — `bound`
- `internal/modelruntime/adapter/gemini/breaker.go:16` — `newCircuitBreaker`
- `internal/modelruntime/adapter/gemini/breaker.go:20` — `(b *circuitBreaker).allow`
- `internal/modelruntime/adapter/gemini/breaker.go:34` — `(b *circuitBreaker).success`
- `internal/modelruntime/adapter/gemini/breaker.go:41` — `(b *circuitBreaker).failure`
- `internal/modelruntime/adapter/gemini/config.go:40` — `LoadConfig`
- `internal/modelruntime/adapter/gemini/config.go:70` — `(c Config).Validate`
- `internal/modelruntime/adapter/gemini/config.go:106` — `defaultHTTPClient`
- `internal/modelruntime/adapter/gemini/config.go:128` — `envBool`
- `internal/modelruntime/adapter/gemini/config.go:140` — `envInt`
- `internal/modelruntime/adapter/gemini/config.go:152` — `envDuration`
- `internal/modelruntime/adapter/mimo/adapter.go:110` — `cacheTokens`
- `internal/modelruntime/adapter/mimo/adapter.go:154` — `requestTelemetry`
- `internal/modelruntime/adapter/mimo/adapter.go:163` — `(t failureTelemetry).withDuration`
- `internal/modelruntime/adapter/mimo/adapter.go:168` — `(t failureTelemetry).withResponseBytes`
- `internal/modelruntime/adapter/mimo/adapter.go:173` — `(t failureTelemetry).withFinishReason`
- `internal/modelruntime/adapter/mimo/adapter.go:182` — `(t failureTelemetry).withUsage`
- `internal/modelruntime/adapter/mimo/adapter.go:192` — `(t failureTelemetry).withJSONDecodeFailure`
- `internal/modelruntime/adapter/mimo/adapter.go:202` — `classifyJSONError`
- `internal/modelruntime/adapter/mimo/adapter.go:217` — `jsonBoundaryFlags`
- `internal/modelruntime/adapter/mimo/adapter.go:225` — `New`
- `internal/modelruntime/adapter/mimo/adapter.go:229` — `newAdapter`
- `internal/modelruntime/adapter/mimo/adapter.go:267` — `(*Adapter).ProviderID`
- `internal/modelruntime/adapter/mimo/adapter.go:268` — `(a *Adapter).Descriptor`
- `internal/modelruntime/adapter/mimo/adapter.go:270` — `(a *Adapter).Preflight`
- `internal/modelruntime/adapter/mimo/adapter.go:291` — `(a *Adapter).Dispatch`
- `internal/modelruntime/adapter/mimo/adapter.go:429` — `encodeRequest`
- `internal/modelruntime/adapter/mimo/adapter.go:478` — `jsonObjectModeInstruction`
- `internal/modelruntime/adapter/mimo/adapter.go:500` — `decodeContent`
- `internal/modelruntime/adapter/mimo/adapter.go:524` — `stripMarkdownFence`
- `internal/modelruntime/adapter/mimo/adapter.go:547` — `readBounded`
- `internal/modelruntime/adapter/mimo/adapter.go:558` — `parseProviderError`
- `internal/modelruntime/adapter/mimo/adapter.go:578` — `normalizeProviderToken`
- `internal/modelruntime/adapter/mimo/adapter.go:601` — `responseErrorOutcome`
- `internal/modelruntime/adapter/mimo/adapter.go:617` — `(a *Adapter).notSentOutcome`
- `internal/modelruntime/adapter/mimo/adapter.go:625` — `retryableStatus`
- `internal/modelruntime/adapter/mimo/adapter.go:629` — `classifyTransportError`
- `internal/modelruntime/adapter/mimo/adapter.go:639` — `bound`
- `internal/modelruntime/adapter/mimo/breaker.go:16` — `newCircuitBreaker`
- `internal/modelruntime/adapter/mimo/breaker.go:20` — `(b *circuitBreaker).allow`
- `internal/modelruntime/adapter/mimo/breaker.go:34` — `(b *circuitBreaker).success`
- `internal/modelruntime/adapter/mimo/breaker.go:41` — `(b *circuitBreaker).failure`
- `internal/modelruntime/adapter/mimo/config.go:40` — `LoadConfig`
- `internal/modelruntime/adapter/mimo/config.go:75` — `(c Config).Validate`
- `internal/modelruntime/adapter/mimo/config.go:111` — `defaultHTTPClient`
- `internal/modelruntime/adapter/mimo/config.go:133` — `envBool`
- `internal/modelruntime/adapter/mimo/config.go:145` — `envInt`
- `internal/modelruntime/adapter/mimo/config.go:157` — `envDuration`
- `internal/modelruntime/adapter/openaicompat/adapter.go:79` — `New`
- `internal/modelruntime/adapter/openaicompat/adapter.go:83` — `newAdapter`
- `internal/modelruntime/adapter/openaicompat/adapter.go:121` — `(*Adapter).ProviderID`
- `internal/modelruntime/adapter/openaicompat/adapter.go:122` — `(a *Adapter).Descriptor`
- `internal/modelruntime/adapter/openaicompat/adapter.go:124` — `(a *Adapter).Preflight`
- `internal/modelruntime/adapter/openaicompat/adapter.go:145` — `(a *Adapter).Dispatch`
- `internal/modelruntime/adapter/openaicompat/adapter.go:256` — `encodeRequest`
- `internal/modelruntime/adapter/openaicompat/adapter.go:294` — `decodeContent`
- `internal/modelruntime/adapter/openaicompat/adapter.go:318` — `readBounded`
- `internal/modelruntime/adapter/openaicompat/adapter.go:329` — `parseProviderError`
- `internal/modelruntime/adapter/openaicompat/adapter.go:345` — `normalizeProviderToken`
- `internal/modelruntime/adapter/openaicompat/adapter.go:368` — `responseErrorOutcome`
- `internal/modelruntime/adapter/openaicompat/adapter.go:377` — `(a *Adapter).notSentOutcome`
- `internal/modelruntime/adapter/openaicompat/adapter.go:384` — `retryableStatus`
- `internal/modelruntime/adapter/openaicompat/adapter.go:388` — `classifyTransportError`
- `internal/modelruntime/adapter/openaicompat/adapter.go:398` — `bound`
- `internal/modelruntime/adapter/openaicompat/breaker.go:16` — `newCircuitBreaker`
- `internal/modelruntime/adapter/openaicompat/breaker.go:20` — `(b *circuitBreaker).allow`
- `internal/modelruntime/adapter/openaicompat/breaker.go:34` — `(b *circuitBreaker).success`
- `internal/modelruntime/adapter/openaicompat/breaker.go:41` — `(b *circuitBreaker).failure`
- `internal/modelruntime/adapter/openaicompat/config.go:40` — `LoadConfig`
- `internal/modelruntime/adapter/openaicompat/config.go:70` — `(c Config).Validate`
- `internal/modelruntime/adapter/openaicompat/config.go:102` — `defaultHTTPClient`
- `internal/modelruntime/adapter/openaicompat/config.go:124` — `envBool`
- `internal/modelruntime/adapter/openaicompat/config.go:136` — `envInt`
- `internal/modelruntime/adapter/openaicompat/config.go:148` — `envDuration`
- `internal/modelruntime/adapter/openairesponses/adapter.go:108` — `New`
- `internal/modelruntime/adapter/openairesponses/adapter.go:112` — `newAdapter`
- `internal/modelruntime/adapter/openairesponses/adapter.go:150` — `(*Adapter).ProviderID`
- `internal/modelruntime/adapter/openairesponses/adapter.go:151` — `(a *Adapter).Descriptor`
- `internal/modelruntime/adapter/openairesponses/adapter.go:153` — `(a *Adapter).Preflight`
- `internal/modelruntime/adapter/openairesponses/adapter.go:174` — `(a *Adapter).Dispatch`
- `internal/modelruntime/adapter/openairesponses/adapter.go:291` — `encodeRequest`
- `internal/modelruntime/adapter/openairesponses/adapter.go:329` — `decodeOutput`
- `internal/modelruntime/adapter/openairesponses/adapter.go:352` — `readBounded`
- `internal/modelruntime/adapter/openairesponses/adapter.go:363` — `parseProviderError`
- `internal/modelruntime/adapter/openairesponses/adapter.go:379` — `normalizeProviderToken`
- `internal/modelruntime/adapter/openairesponses/adapter.go:402` — `responseErrorOutcome`
- `internal/modelruntime/adapter/openairesponses/adapter.go:411` — `(a *Adapter).notSentOutcome`
- `internal/modelruntime/adapter/openairesponses/adapter.go:418` — `retryableStatus`
- `internal/modelruntime/adapter/openairesponses/adapter.go:422` — `classifyTransportError`
- `internal/modelruntime/adapter/openairesponses/adapter.go:432` — `bound`
- `internal/modelruntime/adapter/openairesponses/breaker.go:16` — `newCircuitBreaker`
- `internal/modelruntime/adapter/openairesponses/breaker.go:20` — `(b *circuitBreaker).allow`
- `internal/modelruntime/adapter/openairesponses/breaker.go:34` — `(b *circuitBreaker).success`
- `internal/modelruntime/adapter/openairesponses/breaker.go:41` — `(b *circuitBreaker).failure`
- `internal/modelruntime/adapter/openairesponses/config.go:46` — `LoadConfig`
- `internal/modelruntime/adapter/openairesponses/config.go:76` — `(c Config).Validate`
- `internal/modelruntime/adapter/openairesponses/config.go:108` — `defaultHTTPClient`
- `internal/modelruntime/adapter/openairesponses/config.go:130` — `envBool`
- `internal/modelruntime/adapter/openairesponses/config.go:142` — `envInt`
- `internal/modelruntime/adapter/openairesponses/config.go:154` — `envDuration`
- `internal/modelruntime/adapter/registry.go:13` — `NewRegistry`
- `internal/modelruntime/adapter/registry.go:22` — `(r *Registry).Get`
- `internal/modelruntime/bootstrap/coordinator.go:34` — `OpenCoordinator`
- `internal/modelruntime/bootstrap/runtime.go:47` — `OpenRegistry`
- `internal/modelruntime/bootstrap/runtime.go:81` — `Open`
- `internal/modelruntime/bootstrap/runtime.go:231` — `(a catalogAdapter).CurrentOrganization`
- `internal/modelruntime/bootstrap/runtime.go:246` — `(a catalogAdapter).GetRole`
- `internal/modelruntime/bootstrap/runtime.go:258` — `(a catalogAdapter).ListRoles`
- `internal/modelruntime/bootstrap/runtime.go:276` — `(a taskAdapter).GetTaskAttempt`
- `internal/modelruntime/bootstrap/runtime.go:326` — `resolveRender`
- `internal/modelruntime/bootstrap/runtime.go:350` — `(a contextAdapter).GetContextSnapshot`
- `internal/modelruntime/bootstrap/runtime.go:384` — `(a contextAdapter).ValidateContextSnapshot`
- `internal/modelruntime/bootstrap/runtime.go:395` — `(a contextAdapter).RenderContextSnapshot`
- `internal/modelruntime/bootstrap/runtime.go:425` — `(a contextAdapter).GetProviderRenderTelemetry`
- `internal/modelruntime/bootstrap/runtime.go:451` — `(a authorizationAdapter).EvaluateDispatch`
- `internal/modelruntime/canonical_routing.go:109` — `loadCanonicalRoutingDocument`
- `internal/modelruntime/canonical_routing.go:151` — `LoadCanonicalRouting`
- `internal/modelruntime/canonical_routing.go:177` — `decodeStrictRouting`
- `internal/modelruntime/canonical_routing.go:354` — `splitYAMLScalar`
- `internal/modelruntime/canonical_routing.go:367` — `parseYAMLScalar`
- `internal/modelruntime/canonical_routing.go:390` — `validateRouting`
- `internal/modelruntime/canonical_routing.go:427` — `normalizeRouting`
- `internal/modelruntime/canonical_routing.go:450` — `normalizeCapabilities`
- `internal/modelruntime/canonical_routing.go:468` — `profileIDForPolicy`
- `internal/modelruntime/canonical_routing.go:480` — `compiledAdapterAvailability`
- `internal/modelruntime/canonical_routing.go:500` — `BuildRegistryPlan`
- `internal/modelruntime/compiled_availability_r21.go:11` — `applyR21CompiledAvailability`
- `internal/modelruntime/config.go:37` — `LoadRuntimeConfig`
- `internal/modelruntime/config.go:79` — `(c RuntimeConfig).Validate`
- `internal/modelruntime/config.go:104` — `envBool`
- `internal/modelruntime/config.go:115` — `envInt`
- `internal/modelruntime/config.go:126` — `envDuration`
- `internal/modelruntime/costgate/gate.go:33` — `New`
- `internal/modelruntime/costgate/gate.go:47` — `(g *Gate).Reserve`
- `internal/modelruntime/costgate/gate.go:135` — `(g *Gate).RecordSubscriptionConsumption`
- `internal/modelruntime/costgate/gate.go:152` — `(g *Gate).Reconcile`
- `internal/modelruntime/costgate/gate.go:167` — `(g *Gate).Release`
- `internal/modelruntime/costgate/gate.go:181` — `(g *Gate).MarkPendingReconciliation`
- `internal/modelruntime/dispatch_service.go:48` — `WithCostBudgetGate`
- `internal/modelruntime/dispatch_service.go:54` — `(fileExecutionPrivateKeyLoader).LoadExecutionPrivateKey`
- `internal/modelruntime/dispatch_service.go:58` — `NewDispatchService`
- `internal/modelruntime/dispatch_service.go:87` — `(s *DispatchService).Dispatch`
- `internal/modelruntime/dispatch_service.go:796` — `(s *DispatchService).settleNonSuccessReservation`
- `internal/modelruntime/dispatch_service.go:845` — `recoveredUsage`
- `internal/modelruntime/dispatch_service.go:864` — `estimateTokenCount`
- `internal/modelruntime/domain.go:61` — `(s InvocationStatus).Terminal`
- `internal/modelruntime/hashing.go:18` — `SHA256Bytes`
- `internal/modelruntime/hashing.go:19` — `CanonicalJSON`
- `internal/modelruntime/hashing.go:32` — `encodeCanonical`
- `internal/modelruntime/hashing.go:78` — `CanonicalizeRawJSON`
- `internal/modelruntime/hashing.go:96` — `canonicalNumberJSON`
- `internal/modelruntime/hashing.go:145` — `invocationRequestHash`
- `internal/modelruntime/hashing.go:186` — `ActionDigest`
- `internal/modelruntime/interfaces.go:236` — `(f ClockFunc).Now`
- `internal/modelruntime/invocation_service.go:27` — `NewInvocationService`
- `internal/modelruntime/invocation_service.go:37` — `(s *InvocationService).Create`
- `internal/modelruntime/invocation_service.go:127` — `(s *InvocationService).Get`
- `internal/modelruntime/invocation_service.go:134` — `(s *InvocationService).List`
- `internal/modelruntime/invocation_service.go:144` — `(s *InvocationService).Cancel`
- `internal/modelruntime/invocation_service.go:163` — `(s *InvocationService).Reconcile`
- `internal/modelruntime/normalizer.go:19` — `(n Normalizer).Normalize`
- `internal/modelruntime/normalizer.go:152` — `decodeJSONWithNumbers`
- `internal/modelruntime/postgres/cancellation.go:13` — `(s *Store).RequestCancellation`
- `internal/modelruntime/postgres/cancellation.go:71` — `(s *Store).CancellationRequested`
- `internal/modelruntime/postgres/cancellation.go:79` — `(s *Store).WatchCancellation`
- `internal/modelruntime/postgres/claims.go:20` — `newClaimToken`
- `internal/modelruntime/postgres/claims.go:34` — `(s *Store).ClaimInvocation`
- `internal/modelruntime/postgres/claims.go:51` — `(s *Store).ClaimInvocationAuthenticated`
- `internal/modelruntime/postgres/claims.go:201` — `isIdentityClaimDenial`
- `internal/modelruntime/postgres/claims.go:209` — `identityClaimReasonCode`
- `internal/modelruntime/postgres/claims.go:226` — `lockedClaimableInvocation`
- `internal/modelruntime/postgres/claims.go:253` — `createClaimAttempt`
- `internal/modelruntime/postgres/claims.go:281` — `verifyToken`
- `internal/modelruntime/postgres/errors.go:15` — `mapError`
- `internal/modelruntime/postgres/events.go:13` — `appendInvocationEvent`
- `internal/modelruntime/postgres/invocations.go:13` — `(s *Store).CreateInvocation`
- `internal/modelruntime/postgres/invocations.go:44` — `(s *Store).GetInvocation`
- `internal/modelruntime/postgres/invocations.go:47` — `(s *Store).ListInvocations`
- `internal/modelruntime/postgres/presend.go:38` — `verifyClaim`
- `internal/modelruntime/postgres/presend.go:166` — `insertEvaluation`
- `internal/modelruntime/postgres/presend.go:205` — `insertProviderRequest`
- `internal/modelruntime/postgres/presend.go:226` — `requireOneRow`
- `internal/modelruntime/postgres/presend.go:233` — `auditPayload`
- `internal/modelruntime/postgres/presend.go:259` — `insertAudit`
- `internal/modelruntime/postgres/presend.go:273` — `lockAssignmentForConsume`
- `internal/modelruntime/postgres/presend.go:282` — `insertAssignmentConsumedAudit`
- `internal/modelruntime/postgres/presend.go:299` — `(s *Store).PersistPreSendAllowAndMarkSendStarted`
- `internal/modelruntime/postgres/presend.go:408` — `(s *Store).PersistPreSendDenyAndFail`
- `internal/modelruntime/postgres/reconcile.go:10` — `(s *Store).Reconcile`
- `internal/modelruntime/postgres/registry.go:15` — `(s *Store).RecordRegistryValidated`
- `internal/modelruntime/postgres/registry.go:20` — `(s *Store).RegistryStatus`
- `internal/modelruntime/postgres/registry.go:65` — `(s *Store).ApplyRegistry`
- `internal/modelruntime/postgres/registry.go:255` — `(s *Store).GetBinding`
- `internal/modelruntime/postgres/registry.go:289` — `intString`
- `internal/modelruntime/postgres/render_telemetry.go:17` — `(s *Store).RecordProviderRenderTelemetry`
- `internal/modelruntime/postgres/render_telemetry.go:32` — `nullIfEmpty`
- `internal/modelruntime/postgres/result_reader.go:10` — `(s *Store).GetInvocationResult`
- `internal/modelruntime/postgres/result_reader.go:38` — `(s *Store).FindInvocationsByTaskAttempt`
- `internal/modelruntime/postgres/results.go:22` — `insertProviderOutcome`
- `internal/modelruntime/postgres/results.go:74` — `insertRecoveredUsage`
- `internal/modelruntime/postgres/results.go:92` — `(s *Store).MarkResponseReceived`
- `internal/modelruntime/postgres/results.go:131` — `(s *Store).RejectProviderResponse`
- `internal/modelruntime/postgres/results.go:182` — `(s *Store).FailCommittedBeforeRequest`
- `internal/modelruntime/postgres/results.go:227` — `(s *Store).CompleteInvocation`
- `internal/modelruntime/postgres/results.go:338` — `(s *Store).FailBeforeSend`
- `internal/modelruntime/postgres/results.go:382` — `(s *Store).FailAfterResponse`
- `internal/modelruntime/postgres/results.go:422` — `(s *Store).MarkAmbiguous`
- `internal/modelruntime/postgres/results.go:467` — `(s *Store).MarkCancelled`
- `internal/modelruntime/postgres/scans.go:13` — `scanInvocation`
- `internal/modelruntime/postgres/scans.go:35` — `scanAttempt`
- `internal/modelruntime/postgres/scans.go:48` — `nullableString`
- `internal/modelruntime/postgres/store.go:17` — `New`
- `internal/modelruntime/postgres/store.go:24` — `lockInvocation`
- `internal/modelruntime/postgres/store.go:31` — `tryLockInvocation`
- `internal/modelruntime/postgres/store.go:64` — `rollback`
- `internal/modelruntime/provider_adapter.go:32` — `(d AdapterDescriptor).Validate`
- `internal/modelruntime/provider_adapter.go:119` — `(o ProviderOutcome).effectiveTransport`
- `internal/modelruntime/provider_adapter.go:126` — `(o ProviderOutcome).Validate`
- `internal/modelruntime/provider_adapter.go:246` — `(e *AdapterError).Error`
- `internal/modelruntime/provider_adapter.go:260` — `(e *AdapterError).Unwrap`
- `internal/modelruntime/provider_adapter.go:267` — `AsAdapterError`
- `internal/modelruntime/provider_adapter.go:275` — `validProviderSHA256`
- `internal/modelruntime/provider_adapter.go:287` — `validOpaqueProviderRequestID`
- `internal/modelruntime/provider_request.go:12` — `BuildProviderRequestEvidence`
- `internal/modelruntime/registry_service.go:15` — `NewRegistryService`
- `internal/modelruntime/registry_service.go:24` — `(s *RegistryService).Validate`
- `internal/modelruntime/registry_service.go:47` — `(s *RegistryService).Plan`
- `internal/modelruntime/registry_service.go:66` — `(s *RegistryService).Diff`
- `internal/modelruntime/registry_service.go:78` — `(s *RegistryService).Sync`
- `internal/modelruntime/registry_service.go:95` — `(s *RegistryService).Status`
- `internal/modelruntime/result_reader.go:13` — `(s *InvocationService).Result`
- `internal/modelruntime/result_reader.go:24` — `(s *InvocationService).FindTaskAttempt`
- `internal/modelruntime/validation.go:21` — `PrepareCreateCommand`
- `internal/modelruntime/validation.go:97` — `validateTaskAttempt`
- `internal/modelruntime/validation.go:118` — `validateContext`
- `internal/modelruntime/validation.go:133` — `validateResolvedEgressPolicy`
- `internal/modelruntime/validation.go:150` — `validateAssignmentForCreation`
- `internal/modelruntime/validation.go:170` — `capabilitiesSatisfy`
- `internal/modelruntime/validation.go:183` — `validateSchemaDefinition`
- `internal/modelruntime/validation.go:250` — `validSchemaType`
- `internal/modelruntime/validation.go:257` — `validateAgainstSchema`
- `internal/modelruntime/validation.go:315` — `matchesType`
- `internal/modelruntime/validation.go:351` — `sortedCapabilities`

Non-Go artifacts:
- `internal/modelruntime/testdata/canonical/model-routing.yaml` (test_only_signal)

## internal/objectstorage

Tracked artifacts: 8; kinds: go_source=4, test_only_signal=4

Production Go paths:
- `internal/objectstorage/client.go`
- `internal/objectstorage/config.go`
- `internal/objectstorage/keys.go`
- `internal/objectstorage/signer.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/objectstorage/client.go:48` — `New`
- `internal/objectstorage/client.go:75` — `(c *Client).ListObjects`
- `internal/objectstorage/client.go:106` — `(c *Client).GetObject`
- `internal/objectstorage/client.go:116` — `(c *Client).PutObject`
- `internal/objectstorage/client.go:181` — `(c *Client).PutObjectIfAbsent`
- `internal/objectstorage/client.go:201` — `(c *Client).reconcileExisting`
- `internal/objectstorage/client.go:231` — `localSummary`
- `internal/objectstorage/client.go:244` — `(c *Client).DeleteObject`
- `internal/objectstorage/client.go:254` — `(c *Client).HeadObject`
- `internal/objectstorage/client.go:269` — `(c *Client).objectsURL`
- `internal/objectstorage/client.go:278` — `(c *Client).objectURL`
- `internal/objectstorage/client.go:287` — `(c *Client).baseURL`
- `internal/objectstorage/client.go:298` — `(c *Client).overrideBaseURLForTest`
- `internal/objectstorage/client.go:308` — `(c *Client).do`
- `internal/objectstorage/client.go:317` — `(c *Client).doRequest`
- `internal/objectstorage/client.go:388` — `sanitizeOCIErrorBody`
- `internal/objectstorage/client.go:396` — `sanitizeErrorField`
- `internal/objectstorage/client.go:418` — `(c *Client).DebugRequestNamespace`
- `internal/objectstorage/config.go:48` — `LoadConfig`
- `internal/objectstorage/config.go:73` — `(c Config).Validate`
- `internal/objectstorage/config.go:104` — `(c Config).Host`
- `internal/objectstorage/config.go:108` — `(c Config).BaseURL`
- `internal/objectstorage/config.go:112` — `(c Config).KeyID`
- `internal/objectstorage/config.go:116` — `defaultHTTPClient`
- `internal/objectstorage/config.go:138` — `envString`
- `internal/objectstorage/config.go:146` — `envBool`
- `internal/objectstorage/config.go:158` — `envDuration`
- `internal/objectstorage/keys.go:24` — `SourceObjectKey`
- `internal/objectstorage/keys.go:45` — `ParserRunManifestKey`
- `internal/objectstorage/keys.go:67` — `PageObjectKey`
- `internal/objectstorage/keys.go:86` — `validateSHA256Hex`
- `internal/objectstorage/keys.go:93` — `validateParserIdentity`
- `internal/objectstorage/signer.go:30` — `newSigner`
- `internal/objectstorage/signer.go:46` — `parseRSAPrivateKey`
- `internal/objectstorage/signer.go:66` — `(s *signer).sign`

## internal/organization

Tracked artifacts: 17; kinds: go_source=10, test_only_signal=7

Production Go paths:
- `internal/organization/registry/canonical_types.go`
- `internal/organization/registry/diff.go`
- `internal/organization/registry/domain.go`
- `internal/organization/registry/hash.go`
- `internal/organization/registry/identifier.go`
- `internal/organization/registry/parser.go`
- `internal/organization/registry/postgres_repository.go`
- `internal/organization/registry/repository.go`
- `internal/organization/registry/service.go`
- `internal/organization/registry/validation.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/organization/registry/diff.go:33` — `roleForHash`
- `internal/organization/registry/diff.go:45` — `reportingKey`
- `internal/organization/registry/diff.go:59` — `CompareSnapshots`
- `internal/organization/registry/diff.go:79` — `compareEntities`
- `internal/organization/registry/diff.go:102` — `jsonEqual`
- `internal/organization/registry/diff.go:108` — `organizationComparable`
- `internal/organization/registry/diff.go:115` — `unitComparableMap`
- `internal/organization/registry/diff.go:127` — `roleComparableMap`
- `internal/organization/registry/diff.go:135` — `reportingComparableMap`
- `internal/organization/registry/diff.go:144` — `unitIDs`
- `internal/organization/registry/diff.go:152` — `roleIDs`
- `internal/organization/registry/diff.go:160` — `reportingIDs`
- `internal/organization/registry/domain.go:135` — `(d Diff).HasChanges`
- `internal/organization/registry/domain.go:142` — `(d Diff).Totals`
- `internal/organization/registry/domain.go:160` — `(r ValidationReport).Valid`
- `internal/organization/registry/domain.go:164` — `(e *ValidationError).Error`
- `internal/organization/registry/hash.go:12` — `hashDocuments`
- `internal/organization/registry/hash.go:44` — `countSnapshot`
- `internal/organization/registry/hash.go:64` — `schemaVersions`
- `internal/organization/registry/hash.go:74` — `documentHashes`
- `internal/organization/registry/hash.go:82` — `sortedKeys`
- `internal/organization/registry/identifier.go:12` — `ValidateUnitID`
- `internal/organization/registry/identifier.go:13` — `ValidateRoleID`
- `internal/organization/registry/identifier.go:15` — `validateIdentifier`
- `internal/organization/registry/identifier.go:34` — `ValidateReferencePath`
- `internal/organization/registry/parser.go:43` — `NewLoader`
- `internal/organization/registry/parser.go:50` — `(l *Loader).Directory`
- `internal/organization/registry/parser.go:52` — `(l *Loader).Load`
- `internal/organization/registry/parser.go:76` — `(l *Loader).readDocuments`
- `internal/organization/registry/parser.go:120` — `(l *Loader).readFile`
- `internal/organization/registry/parser.go:144` — `decodeStrictYAML`
- `internal/organization/registry/parser.go:171` — `validateYAMLNode`
- `internal/organization/registry/parser.go:216` — `normalizeDocuments`
- `internal/organization/registry/parser.go:306` — `stableSourceReference`
- `internal/organization/registry/parser.go:314` — `materialize`
- `internal/organization/registry/parser.go:385` — `cloneString`
- `internal/organization/registry/parser.go:393` — `displayNameFromID`
- `internal/organization/registry/parser.go:405` — `hashValue`
- `internal/organization/registry/postgres_repository.go:30` — `NewPostgresRepository`
- `internal/organization/registry/postgres_repository.go:37` — `(r *PostgresRepository).GetOrganization`
- `internal/organization/registry/postgres_repository.go:41` — `getOrganization`
- `internal/organization/registry/postgres_repository.go:49` — `(r *PostgresRepository).ListUnits`
- `internal/organization/registry/postgres_repository.go:53` — `listUnits`
- `internal/organization/registry/postgres_repository.go:70` — `(r *PostgresRepository).GetUnit`
- `internal/organization/registry/postgres_repository.go:74` — `getUnit`
- `internal/organization/registry/postgres_repository.go:82` — `scanRole`
- `internal/organization/registry/postgres_repository.go:88` — `(r *PostgresRepository).GetRole`
- `internal/organization/registry/postgres_repository.go:92` — `getRole`
- `internal/organization/registry/postgres_repository.go:96` — `(r *PostgresRepository).ListRoles`
- `internal/organization/registry/postgres_repository.go:100` — `listRoles`
- `internal/organization/registry/postgres_repository.go:127` — `(r *PostgresRepository).GetLeader`
- `internal/organization/registry/postgres_repository.go:131` — `getLeader`
- `internal/organization/registry/postgres_repository.go:135` — `(r *PostgresRepository).ListWorkers`
- `internal/organization/registry/postgres_repository.go:139` — `listWorkers`
- `internal/organization/registry/postgres_repository.go:156` — `(r *PostgresRepository).GetCurrentRevision`
- `internal/organization/registry/postgres_repository.go:160` — `getCurrentRevision`
- `internal/organization/registry/postgres_repository.go:172` — `readRevision`
- `internal/organization/registry/postgres_repository.go:201` — `(r *PostgresRepository).LoadCurrentSnapshot`
- `internal/organization/registry/postgres_repository.go:205` — `loadCurrentSnapshot`
- `internal/organization/registry/postgres_repository.go:259` — `(r *PostgresRepository).Apply`
- `internal/organization/registry/postgres_repository.go:367` — `nullableInt64`
- `internal/organization/registry/postgres_repository.go:374` — `nullString`
- `internal/organization/registry/postgres_repository.go:381` — `mapQueryError`
- `internal/organization/registry/postgres_repository.go:401` — `IsDatabaseUnavailable`
- `internal/organization/registry/service.go:16` — `NewService`
- `internal/organization/registry/service.go:29` — `(s *Service).ValidateCanonical`
- `internal/organization/registry/service.go:31` — `(s *Service).CompareCanonical`
- `internal/organization/registry/service.go:59` — `(s *Service).SynchronizeCanonical`
- `internal/organization/registry/service.go:83` — `(s *Service).GetOrganization`
- `internal/organization/registry/service.go:89` — `(s *Service).ListUnits`
- `internal/organization/registry/service.go:95` — `(s *Service).GetUnit`
- `internal/organization/registry/service.go:101` — `(s *Service).GetRole`
- `internal/organization/registry/service.go:107` — `(s *Service).ListRoles`
- `internal/organization/registry/service.go:113` — `(s *Service).GetLeader`
- `internal/organization/registry/service.go:119` — `(s *Service).ListWorkers`
- `internal/organization/registry/service.go:125` — `(s *Service).GetCurrentRevision`
- `internal/organization/registry/validation.go:29` — `productiveEgressAllowApproved`
- `internal/organization/registry/validation.go:38` — `validateDocuments`
- `internal/organization/registry/validation.go:50` — `(v *validator).run`
- `internal/organization/registry/validation.go:65` — `(v *validator).addError`
- `internal/organization/registry/validation.go:69` — `(v *validator).addWarning`
- `internal/organization/registry/validation.go:73` — `(v *validator).validateDocumentMetadata`
- `internal/organization/registry/validation.go:96` — `(v *validator).validateOrganization`
- `internal/organization/registry/validation.go:131` — `(v *validator).validateIdentifiersAndPaths`
- `internal/organization/registry/validation.go:172` — `(v *validator).validateRoleReferences`
- `internal/organization/registry/validation.go:222` — `(v *validator).validateLeaderWorkerMap`
- `internal/organization/registry/validation.go:272` — `(v *validator).validateReportingGraph`
- `internal/organization/registry/validation.go:314` — `(v *validator).validateModelsAndAuthorities`
- `internal/organization/registry/validation.go:335` — `(v *validator).validateCounts`
- `internal/organization/registry/validation.go:349` — `(v *validator).validateProposedRoles`
- `internal/organization/registry/validation.go:357` — `(v *validator).validateSourceManifest`
- `internal/organization/registry/validation.go:378` — `(v *validator).validateModelEgressPolicy`
- `internal/organization/registry/validation.go:439` — `(v *validator).validateModelInvokeCapability`
- `internal/organization/registry/validation.go:472` — `containsString`

## internal/pdfingest

Tracked artifacts: 7; kinds: go_source=2, test_only_signal=5

Production Go paths:
- `internal/pdfingest/poppler/poppler.go`
- `internal/pdfingest/processor.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/pdfingest/poppler/poppler.go:60` — `DefaultConfig`
- `internal/pdfingest/poppler/poppler.go:89` — `New`
- `internal/pdfingest/poppler/poppler.go:114` — `resolveGsVersion`
- `internal/pdfingest/poppler/poppler.go:130` — `resolveVersion`
- `internal/pdfingest/poppler/poppler.go:152` — `classifyFailure`
- `internal/pdfingest/poppler/poppler.go:163` — `firstLine`
- `internal/pdfingest/poppler/poppler.go:170` — `(p *Processor).run`
- `internal/pdfingest/poppler/poppler.go:192` — `(p *Processor).Process`
- `internal/pdfingest/poppler/poppler.go:314` — `isPageAmplified`
- `internal/pdfingest/poppler/poppler.go:333` — `(p *Processor).rebuildPageWithGhostscript`
- `internal/pdfingest/poppler/poppler.go:358` — `(p *Processor).extractText`
- `internal/pdfingest/poppler/poppler.go:385` — `stripNULBytes`
- `internal/pdfingest/poppler/poppler.go:394` — `pageNumberOf`
- `internal/pdfingest/processor.go:29` — `(s TextExtractionStatus).Valid`
- `internal/pdfingest/processor.go:87` — `(r QuarantineReason).Valid`
- `internal/pdfingest/processor.go:105` — `(e *QuarantineError).Error`

Non-Go artifacts:
- `internal/pdfingest/poppler/testdata/encrypted.pdf` (test_only_signal)
- `internal/pdfingest/poppler/testdata/malformed.pdf` (test_only_signal)
- `internal/pdfingest/poppler/testdata/no-text.pdf` (test_only_signal)
- `internal/pdfingest/poppler/testdata/two-page.pdf` (test_only_signal)

## internal/platform

Tracked artifacts: 11; kinds: go_source=6, test_only_signal=5

Production Go paths:
- `internal/platform/buildinfo/info.go`
- `internal/platform/httpserver/server.go`
- `internal/platform/logging/logging.go`
- `internal/platform/migrations/runner.go`
- `internal/platform/postgres/store.go`
- `internal/platform/postgres/unit_of_work.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/platform/buildinfo/info.go:17` — `(i Info).String`
- `internal/platform/buildinfo/info.go:26` — `valueOr`
- `internal/platform/httpserver/server.go:30` — `New`
- `internal/platform/httpserver/server.go:53` — `(s *Server).Start`
- `internal/platform/httpserver/server.go:76` — `(s *Server).Shutdown`
- `internal/platform/httpserver/server.go:83` — `(s *Server).Addr`
- `internal/platform/httpserver/server.go:92` — `(s *Server).handleHealth`
- `internal/platform/httpserver/server.go:96` — `(s *Server).handleReady`
- `internal/platform/httpserver/server.go:111` — `(s *Server).handleVersion`
- `internal/platform/httpserver/server.go:115` — `(s *Server).logRequest`
- `internal/platform/httpserver/server.go:123` — `(s *Server).recoverPanic`
- `internal/platform/httpserver/server.go:135` — `writeJSON`
- `internal/platform/logging/logging.go:8` — `New`
- `internal/platform/migrations/runner.go:52` — `New`
- `internal/platform/migrations/runner.go:63` — `Load`
- `internal/platform/migrations/runner.go:151` — `(r *Runner).Up`
- `internal/platform/migrations/runner.go:199` — `(r *Runner).Status`
- `internal/platform/migrations/runner.go:247` — `(r *Runner).validateApplied`
- `internal/platform/migrations/runner.go:295` — `(r *Runner).Tip`
- `internal/platform/migrations/runner.go:318` — `ensureSchemaTable`
- `internal/platform/migrations/runner.go:334` — `schemaTableExists`
- `internal/platform/migrations/runner.go:342` — `readApplied`
- `internal/platform/migrations/runner.go:363` — `rollbackMigration`
- `internal/platform/postgres/store.go:18` — `Open`
- `internal/platform/postgres/store.go:41` — `(s *Store).Ping`
- `internal/platform/postgres/store.go:51` — `(s *Store).Close`
- `internal/platform/postgres/store.go:57` — `(s *Store).Pool`
- `internal/platform/postgres/store.go:64` — `(s *Store).UnitOfWork`
- `internal/platform/postgres/store.go:71` — `PingWithTimeout`
- `internal/platform/postgres/unit_of_work.go:23` — `NewUnitOfWork`
- `internal/platform/postgres/unit_of_work.go:27` — `(u *unitOfWork).WithinTransaction`
- `internal/platform/postgres/unit_of_work.go:58` — `rollback`

## internal/questionidentity

Tracked artifacts: 6; kinds: go_source=3, test_only_signal=3

Production Go paths:
- `internal/questionidentity/controller_binding.go`
- `internal/questionidentity/execution.go`
- `internal/questionidentity/gate.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/questionidentity/controller_binding.go:56` — `BindControllerPayload`
- `internal/questionidentity/controller_binding.go:109` — `decodeControllerRequest`
- `internal/questionidentity/controller_binding.go:126` — `authorizedTask`
- `internal/questionidentity/execution.go:25` — `CanonicalEnvelope`
- `internal/questionidentity/execution.go:72` — `NewExecutionPath`
- `internal/questionidentity/execution.go:85` — `(path *ExecutionPath).RunNext`
- `internal/questionidentity/execution.go:121` — `EvaluatePayload`
- `internal/questionidentity/execution.go:149` — `decodeEnvelope`
- `internal/questionidentity/execution.go:166` — `rejectFields`
- `internal/questionidentity/gate.go:144` — `(d Decision).Accepted`
- `internal/questionidentity/gate.go:149` — `CanonicalContract`
- `internal/questionidentity/gate.go:161` — `Evaluate`
- `internal/questionidentity/gate.go:213` — `verifyCanonicalContract`
- `internal/questionidentity/gate.go:233` — `exactSetReason`
- `internal/questionidentity/gate.go:248` — `exactRequiredOutputReason`
- `internal/questionidentity/gate.go:263` — `duplicateOrBlank`
- `internal/questionidentity/gate.go:277` — `validateSupplements`
- `internal/questionidentity/gate.go:294` — `validatePredicates`
- `internal/questionidentity/gate.go:312` — `orderedChangedFields`
- `internal/questionidentity/gate.go:331` — `normalize`
- `internal/questionidentity/gate.go:354` — `marshalCanonicalJSON`

## internal/rag

Tracked artifacts: 32; kinds: go_source=21, test_only_signal=11

Production Go paths:
- `internal/rag/authz/gate.go`
- `internal/rag/bootstrap/bootstrap.go`
- `internal/rag/canonical_time.go`
- `internal/rag/chunking.go`
- `internal/rag/contextprovider/provider.go`
- `internal/rag/domain.go`
- `internal/rag/errors.go`
- `internal/rag/events.go`
- `internal/rag/hashing.go`
- `internal/rag/interfaces.go`
- `internal/rag/manager.go`
- `internal/rag/postgres/backfill.go`
- `internal/rag/postgres/embeddings.go`
- `internal/rag/postgres/embeddings_bge_m3.go`
- `internal/rag/postgres/hybrid_query.go`
- `internal/rag/postgres/store.go`
- `internal/rag/roles/resolver.go`
- `internal/rag/semantic.go`
- `internal/rag/service.go`
- `internal/rag/transitions.go`
- `internal/rag/validation.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/rag/authz/gate.go:28` — `New`
- `internal/rag/authz/gate.go:44` — `(g *Gate).Authorize`
- `internal/rag/bootstrap/bootstrap.go:56` — `activeEmbeddingProfile`
- `internal/rag/bootstrap/bootstrap.go:78` — `Open`
- `internal/rag/bootstrap/bootstrap.go:140` — `openSemanticSearch`
- `internal/rag/bootstrap/bootstrap.go:160` — `(f *objectStorageMediaFetcher).FetchMedia`
- `internal/rag/bootstrap/bootstrap.go:170` — `openMediaFetcher`
- `internal/rag/bootstrap/bootstrap.go:185` — `sharedSpendControls`
- `internal/rag/bootstrap/bootstrap.go:205` — `openGeminiSemanticSearch`
- `internal/rag/bootstrap/bootstrap.go:246` — `openBGEM3SemanticSearch`
- `internal/rag/canonical_time.go:15` — `canonicalPersistenceTime`
- `internal/rag/chunking.go:17` — `ChunkBody`
- `internal/rag/chunking.go:104` — `splitParagraphs`
- `internal/rag/chunking.go:126` — `splitBytes`
- `internal/rag/contextprovider/provider.go:24` — `New`
- `internal/rag/contextprovider/provider.go:40` — `(p *Provider).ListApprovedEvidence`
- `internal/rag/contextprovider/provider.go:82` — `(p *Provider).ValidateVersion`
- `internal/rag/contextprovider/provider.go:137` — `encodeVersion`
- `internal/rag/contextprovider/provider.go:141` — `parseVersion`
- `internal/rag/contextprovider/provider.go:149` — `sourceRecord`
- `internal/rag/contextprovider/provider.go:180` — `mapDataClass`
- `internal/rag/domain.go:15` — `(c DataClass).Valid`
- `internal/rag/domain.go:24` — `(c DataClass).AllowedInApprovedKnowledge`
- `internal/rag/domain.go:41` — `(k SourceKind).Valid`
- `internal/rag/domain.go:57` — `(k NamespaceKind).Valid`
- `internal/rag/domain.go:69` — `(l Lifecycle).Valid`
- `internal/rag/domain.go:183` — `(s TextExtractionStatus).Valid`
- `internal/rag/domain.go:194` — `(c Chunk).IsMedia`
- `internal/rag/domain.go:204` — `(s GenerationStatus).Valid`
- `internal/rag/hashing.go:10` — `ContentHash`
- `internal/rag/hashing.go:15` — `(v KnowledgeVersion).ComputeCanonicalHash`
- `internal/rag/interfaces.go:80` — `(id EmbeddingIdentity).Validate`
- `internal/rag/manager.go:34` — `NewManager`
- `internal/rag/manager.go:58` — `(m *Manager).Propose`
- `internal/rag/manager.go:93` — `(m *Manager).Review`
- `internal/rag/manager.go:108` — `(m *Manager).Deprecate`
- `internal/rag/manager.go:123` — `(m *Manager).Archive`
- `internal/rag/manager.go:138` — `(m *Manager).Get`
- `internal/rag/manager.go:161` — `(m *Manager).GetForRevalidation`
- `internal/rag/manager.go:170` — `(m *Manager).List`
- `internal/rag/manager.go:187` — `(m *Manager).authorizeNamespaceRead`
- `internal/rag/manager.go:206` — `(m *Manager).loadMutationTarget`
- `internal/rag/manager.go:226` — `(m *Manager).authorizeMutation`
- `internal/rag/manager.go:237` — `mutationDigest`
- `internal/rag/manager.go:263` — `(m *Manager).Reindex`
- `internal/rag/manager.go:350` — `(m *Manager).BackfillEmbeddings`
- `internal/rag/manager.go:461` — `(m *Manager).Query`
- `internal/rag/manager.go:503` — `(m *Manager).ActiveGeneration`
- `internal/rag/manager.go:514` — `(m *Manager).ExistingEvidenceReferences`
- `internal/rag/postgres/backfill.go:24` — `(s *Store).PendingChunkEmbeddings`
- `internal/rag/postgres/embeddings.go:26` — `encodeVector`
- `internal/rag/postgres/embeddings.go:42` — `(s *Store).InsertChunkEmbedding`
- `internal/rag/postgres/embeddings.go:76` — `(s *Store).NearestChunks`
- `internal/rag/postgres/embeddings.go:113` — `(s *Store).CreateEmbeddingBatchJob`
- `internal/rag/postgres/embeddings.go:173` — `(s *Store).RecordEmbeddingBatchJobItemResult`
- `internal/rag/postgres/embeddings.go:213` — `(s *Store).CompleteEmbeddingBatchJob`
- `internal/rag/postgres/embeddings_bge_m3.go:20` — `encodeVectorBGEM3`
- `internal/rag/postgres/embeddings_bge_m3.go:36` — `(s *Store).InsertBGEM3ChunkEmbedding`
- `internal/rag/postgres/embeddings_bge_m3.go:71` — `(s *Store).NearestBGEM3Chunks`
- `internal/rag/postgres/hybrid_query.go:34` — `vectorChannelClause`
- `internal/rag/postgres/hybrid_query.go:86` — `rrfCandidatePoolSize`
- `internal/rag/postgres/hybrid_query.go:117` — `(s *Store).runHybridQuery`
- `internal/rag/postgres/store.go:24` — `New`
- `internal/rag/postgres/store.go:37` — `(s *Store).CreateCandidate`
- `internal/rag/postgres/store.go:120` — `insertVersion`
- `internal/rag/postgres/store.go:136` — `(s *Store).Get`
- `internal/rag/postgres/store.go:147` — `(s *Store).Save`
- `internal/rag/postgres/store.go:205` — `(s *Store).List`
- `internal/rag/postgres/store.go:248` — `(s *Store).ApprovedForNamespace`
- `internal/rag/postgres/store.go:314` — `(s *Store).Reindex`
- `internal/rag/postgres/store.go:461` — `(s *Store).Query`
- `internal/rag/postgres/store.go:542` — `(s *Store).ActiveGeneration`
- `internal/rag/postgres/store.go:572` — `(s *Store).ExistingEvidenceReferences`
- `internal/rag/postgres/store.go:597` — `escapeLikePrefix`
- `internal/rag/postgres/store.go:607` — `getVersion`
- `internal/rag/postgres/store.go:650` — `listEvidenceRefs`
- `internal/rag/postgres/store.go:672` — `lookupIdempotency`
- `internal/rag/postgres/store.go:684` — `insertIdempotency`
- `internal/rag/postgres/store.go:702` — `normalizeLimit`
- `internal/rag/postgres/store.go:712` — `nullableString`
- `internal/rag/postgres/store.go:720` — `nullableInt`
- `internal/rag/postgres/store.go:727` — `stringOrEmpty`
- `internal/rag/postgres/store.go:734` — `mapError`
- `internal/rag/roles/resolver.go:23` — `New`
- `internal/rag/roles/resolver.go:32` — `(r *Resolver).ResolveNamespace`
- `internal/rag/semantic.go:52` — `(d *SemanticSearchDeps).validate`
- `internal/rag/semantic.go:78` — `(m *Manager).embedQuery`
- `internal/rag/semantic.go:86` — `(m *Manager).embed`
- `internal/rag/semantic.go:101` — `(m *Manager).embedMedia`
- `internal/rag/semantic.go:111` — `estimateTextTokens`
- `internal/rag/semantic.go:123` — `estimateMediaTokens`
- `internal/rag/semantic.go:148` — `(m *Manager).embedItem`
- `internal/rag/service.go:14` — `(systemClock).Now`
- `internal/rag/service.go:20` — `NewService`
- `internal/rag/service.go:45` — `(s *Service).Propose`
- `internal/rag/service.go:86` — `(o ReviewOutcome).Valid`
- `internal/rag/service.go:88` — `(s *Service).Review`
- `internal/rag/service.go:106` — `(s *Service).Deprecate`
- `internal/rag/service.go:110` — `(s *Service).Archive`
- `internal/rag/service.go:114` — `(s *Service).transition`
- `internal/rag/transitions.go:22` — `ValidateTransition`
- `internal/rag/validation.go:18` — `(d KnowledgeDocument).Validate`
- `internal/rag/validation.go:34` — `(a AdmissionAttestation).Validate`
- `internal/rag/validation.go:69` — `(v KnowledgeVersion).Validate`

## internal/retrievalfixtures

Tracked artifacts: 5; kinds: test_only_signal=5

Production Go paths:
- `internal/retrievalfixtures/activate.go`
- `internal/retrievalfixtures/runner.go`
- `internal/retrievalfixtures/support.go`

## internal/secrets

Tracked artifacts: 5; kinds: go_source=3, test_only_signal=2

Production Go paths:
- `internal/secrets/file_resolver.go`
- `internal/secrets/ref.go`
- `internal/secrets/token_file.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/secrets/file_resolver.go:18` — `LoadEd25519PrivateKey`
- `internal/secrets/token_file.go:17` — `LoadBearerToken`
- `internal/secrets/token_file.go:51` — `Zero`
- `internal/secrets/token_file.go:53` — `zero`

## internal/secretscan

Tracked artifacts: 2; kinds: go_source=1, test_only_signal=1

Production Go paths:
- `internal/secretscan/secretscan.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/secretscan/secretscan.go:67` — `(f Finding).String`
- `internal/secretscan/secretscan.go:131` — `isPlaceholder`
- `internal/secretscan/secretscan.go:142` — `Scan`
- `internal/secretscan/secretscan.go:163` — `collapse`
- `internal/secretscan/secretscan.go:189` — `Kinds`
- `internal/secretscan/secretscan.go:207` — `Redact`

## internal/securityaudit

Tracked artifacts: 3; kinds: go_source=1, test_only_signal=2

Production Go paths:
- `internal/securityaudit/covert_channels.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/securityaudit/covert_channels.go:39` — `Catalog`
- `internal/securityaudit/covert_channels.go:362` — `Rules`
- `internal/securityaudit/covert_channels.go:503` — `CheckViolations`
- `internal/securityaudit/covert_channels.go:531` — `formatSize`
- `internal/securityaudit/covert_channels.go:549` — `formatBool`

## internal/shadowverifier

Tracked artifacts: 12; kinds: go_source=8, test_only_signal=4

Production Go paths:
- `internal/shadowverifier/canon.go`
- `internal/shadowverifier/derive.go`
- `internal/shadowverifier/errors.go`
- `internal/shadowverifier/matrix.go`
- `internal/shadowverifier/ports.go`
- `internal/shadowverifier/postgres/store.go`
- `internal/shadowverifier/service.go`
- `internal/shadowverifier/types.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/shadowverifier/canon.go:32` — `LoadLeaderWorkerMap`
- `internal/shadowverifier/derive.go:18` — `RoleExists`
- `internal/shadowverifier/derive.go:26` — `DepartmentExists`
- `internal/shadowverifier/derive.go:40` — `LeaderOf`
- `internal/shadowverifier/derive.go:67` — `reportingEdge`
- `internal/shadowverifier/derive.go:89` — `MayDelegate`
- `internal/shadowverifier/derive.go:122` — `MayMessage`
- `internal/shadowverifier/derive.go:171` — `CheckDependencyClosed`
- `internal/shadowverifier/derive.go:205` — `detectCycles`
- `internal/shadowverifier/derive.go:260` — `EvaluateCapability`
- `internal/shadowverifier/matrix.go:93` — `LoadMatrix`
- `internal/shadowverifier/matrix.go:169` — `(m MatrixIndex).CapabilityIDs`
- `internal/shadowverifier/matrix.go:178` — `containsString`
- `internal/shadowverifier/ports.go:70` — `(f ClockFunc).Now`
- `internal/shadowverifier/postgres/store.go:28` — `New`
- `internal/shadowverifier/postgres/store.go:49` — `(s *Store).LoadSnapshot`
- `internal/shadowverifier/postgres/store.go:141` — `(s *Store).RecordedRequests`
- `internal/shadowverifier/postgres/store.go:164` — `(s *Store).StartRun`
- `internal/shadowverifier/postgres/store.go:174` — `(s *Store).FinishRun`
- `internal/shadowverifier/postgres/store.go:190` — `(s *Store).RecordFindings`
- `internal/shadowverifier/postgres/store.go:206` — `(s *Store).GetRun`
- `internal/shadowverifier/postgres/store.go:230` — `(s *Store).ListRuns`
- `internal/shadowverifier/postgres/store.go:256` — `(s *Store).RunFindings`
- `internal/shadowverifier/service.go:36` — `NewService`
- `internal/shadowverifier/service.go:74` — `(s *Service).OrganizationID`
- `internal/shadowverifier/service.go:80` — `(s *Service).VerifyExhaustive`
- `internal/shadowverifier/service.go:113` — `(s *Service).ReplayRecorded`
- `internal/shadowverifier/service.go:168` — `(s *Service).loadAndCheckSnapshot`
- `internal/shadowverifier/service.go:188` — `(s *Service).checkExistenceFacts`
- `internal/shadowverifier/service.go:237` — `(s *Service).checkLeaderFacts`
- `internal/shadowverifier/service.go:288` — `(s *Service).checkCapabilityFacts`
- `internal/shadowverifier/service.go:336` — `compareCapabilityVerdicts`
- `internal/shadowverifier/service.go:352` — `(s *Service).checkDelegateCanonCrossCheck`
- `internal/shadowverifier/service.go:435` — `(s *Service).checkMessageObserverClause`
- `internal/shadowverifier/service.go:467` — `(s *Service).checkDependencyClosed`
- `internal/shadowverifier/service.go:505` — `(s *Service).finishRun`
- `internal/shadowverifier/service.go:530` — `(t *tally).add`
- `internal/shadowverifier/service.go:550` — `(t *tally).abort`
- `internal/shadowverifier/service.go:556` — `compareBool`
- `internal/shadowverifier/service.go:569` — `compareBoolUnit`
- `internal/shadowverifier/service.go:573` — `verdictFromBool`
- `internal/shadowverifier/service.go:583` — `sampled`
- `internal/shadowverifier/types.go:44` — `(f FactID).Valid`
- `internal/shadowverifier/types.go:67` — `(k FindingKind).Valid`
- `internal/shadowverifier/types.go:189` — `(s Snapshot).RoleIndex`
- `internal/shadowverifier/types.go:198` — `(s Snapshot).UnitIndex`
- `internal/shadowverifier/types.go:228` — `(m RunMode).Valid`
- `internal/shadowverifier/types.go:289` — `FindingFrom`
- `internal/shadowverifier/types.go:314` — `(r RunReport).Divergences`

## internal/skillregistry

Tracked artifacts: 16; kinds: go_source=12, test_only_signal=4

Production Go paths:
- `internal/skillregistry/authz/gate.go`
- `internal/skillregistry/bootstrap/bootstrap.go`
- `internal/skillregistry/domain.go`
- `internal/skillregistry/errors.go`
- `internal/skillregistry/events.go`
- `internal/skillregistry/hashing.go`
- `internal/skillregistry/interfaces.go`
- `internal/skillregistry/manager.go`
- `internal/skillregistry/postgres/store.go`
- `internal/skillregistry/service.go`
- `internal/skillregistry/transitions.go`
- `internal/skillregistry/validation.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/skillregistry/authz/gate.go:29` — `(systemClock).Now`
- `internal/skillregistry/authz/gate.go:38` — `New`
- `internal/skillregistry/authz/gate.go:54` — `(g *Gate).AuthorizeProposal`
- `internal/skillregistry/authz/gate.go:58` — `(g *Gate).AuthorizeLifecycleChange`
- `internal/skillregistry/authz/gate.go:67` — `(g *Gate).AuthorizeAssignmentChange`
- `internal/skillregistry/authz/gate.go:72` — `(g *Gate).authorize`
- `internal/skillregistry/authz/gate.go:113` — `actionDigest`
- `internal/skillregistry/bootstrap/bootstrap.go:26` — `Open`
- `internal/skillregistry/domain.go:16` — `(v Lifecycle).Valid`
- `internal/skillregistry/domain.go:32` — `(v AssignmentStatus).Valid`
- `internal/skillregistry/domain.go:41` — `(v OriginKind).Valid`
- `internal/skillregistry/hashing.go:10` — `HashManifest`
- `internal/skillregistry/hashing.go:20` — `HashVersionIdentity`
- `internal/skillregistry/manager.go:21` — `NewManager`
- `internal/skillregistry/manager.go:39` — `(m *Manager).Propose`
- `internal/skillregistry/manager.go:62` — `(m *Manager).HumanApprove`
- `internal/skillregistry/manager.go:68` — `(m *Manager).QualifyCandidate`
- `internal/skillregistry/manager.go:74` — `(m *Manager).Activate`
- `internal/skillregistry/manager.go:80` — `(m *Manager).Suspend`
- `internal/skillregistry/manager.go:84` — `(m *Manager).Retire`
- `internal/skillregistry/manager.go:88` — `(m *Manager).transition`
- `internal/skillregistry/manager.go:132` — `(m *Manager).Assign`
- `internal/skillregistry/manager.go:173` — `(m *Manager).RevokeAssignment`
- `internal/skillregistry/manager.go:213` — `(m *Manager).GetSkill`
- `internal/skillregistry/manager.go:221` — `(m *Manager).GetVersion`
- `internal/skillregistry/manager.go:229` — `(m *Manager).ListVersions`
- `internal/skillregistry/manager.go:237` — `(m *Manager).GetAssignment`
- `internal/skillregistry/manager.go:245` — `(m *Manager).ListActiveAssignmentsForRole`
- `internal/skillregistry/postgres/store.go:27` — `New`
- `internal/skillregistry/postgres/store.go:40` — `(s *Store).CreateSkill`
- `internal/skillregistry/postgres/store.go:117` — `insertVersion`
- `internal/skillregistry/postgres/store.go:140` — `(s *Store).GetSkill`
- `internal/skillregistry/postgres/store.go:151` — `(s *Store).GetVersion`
- `internal/skillregistry/postgres/store.go:162` — `(s *Store).ListVersions`
- `internal/skillregistry/postgres/store.go:194` — `(s *Store).SaveVersion`
- `internal/skillregistry/postgres/store.go:260` — `(s *Store).CreateAssignment`
- `internal/skillregistry/postgres/store.go:317` — `(s *Store).GetAssignment`
- `internal/skillregistry/postgres/store.go:328` — `(s *Store).ListActiveAssignmentsForRole`
- `internal/skillregistry/postgres/store.go:360` — `(s *Store).SaveAssignment`
- `internal/skillregistry/postgres/store.go:414` — `getSkill`
- `internal/skillregistry/postgres/store.go:426` — `getVersion`
- `internal/skillregistry/postgres/store.go:476` — `getSkillAndVersion`
- `internal/skillregistry/postgres/store.go:488` — `getAssignment`
- `internal/skillregistry/postgres/store.go:516` — `lookupSkillIdempotency`
- `internal/skillregistry/postgres/store.go:528` — `insertSkillIdempotency`
- `internal/skillregistry/postgres/store.go:546` — `assignmentIdentityHash`
- `internal/skillregistry/postgres/store.go:552` — `marshalEvidence`
- `internal/skillregistry/postgres/store.go:563` — `marshalValidation`
- `internal/skillregistry/postgres/store.go:574` — `nullableString`
- `internal/skillregistry/postgres/store.go:582` — `stringOrEmpty`
- `internal/skillregistry/postgres/store.go:589` — `mapErrorAssignmentConflict`
- `internal/skillregistry/postgres/store.go:597` — `mapError`
- `internal/skillregistry/service.go:12` — `(systemClock).Now`
- `internal/skillregistry/service.go:18` — `NewService`
- `internal/skillregistry/service.go:37` — `(s *Service).CreateDraft`
- `internal/skillregistry/service.go:82` — `(s *Service).HumanApprove`
- `internal/skillregistry/service.go:89` — `(s *Service).QualifyCandidate`
- `internal/skillregistry/service.go:96` — `(s *Service).Activate`
- `internal/skillregistry/service.go:109` — `(s *Service).Suspend`
- `internal/skillregistry/service.go:113` — `(s *Service).Retire`
- `internal/skillregistry/service.go:117` — `(s *Service).transition`
- `internal/skillregistry/service.go:153` — `(s *Service).Assign`
- `internal/skillregistry/service.go:187` — `(s *Service).RevokeAssignment`
- `internal/skillregistry/transitions.go:29` — `ValidateTransition`
- `internal/skillregistry/validation.go:21` — `(s Skill).Validate`
- `internal/skillregistry/validation.go:34` — `(m Manifest).Validate`
- `internal/skillregistry/validation.go:73` — `(s SourceRecord).Validate`
- `internal/skillregistry/validation.go:108` — `(v SkillVersion).Validate`
- `internal/skillregistry/validation.go:187` — `validateApproval`
- `internal/skillregistry/validation.go:194` — `validateValidation`
- `internal/skillregistry/validation.go:210` — `(a SkillAssignment).Validate`
- `internal/skillregistry/validation.go:232` — `NormalizeCapabilities`

## internal/staging

Tracked artifacts: 19; kinds: go_source=15, test_only_signal=4

Production Go paths:
- `internal/staging/artifactfs/store.go`
- `internal/staging/bootstrap/bootstrap.go`
- `internal/staging/catalog.go`
- `internal/staging/domain.go`
- `internal/staging/errors.go`
- `internal/staging/gitexec/backend.go`
- `internal/staging/postgres/helpers.go`
- `internal/staging/postgres/promotions.go`
- `internal/staging/postgres/store.go`
- `internal/staging/postgres/workspaces.go`
- `internal/staging/reconcile.go`
- `internal/staging/repository.go`
- `internal/staging/service.go`
- `internal/staging/state_machine.go`
- `internal/staging/validation.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/staging/artifactfs/store.go:22` — `New`
- `internal/staging/artifactfs/store.go:33` — `(s *Store).Put`
- `internal/staging/artifactfs/store.go:119` — `(s *Store).Stat`
- `internal/staging/artifactfs/store.go:134` — `(s *Store).Open`
- `internal/staging/artifactfs/store.go:155` — `(s *Store).Verify`
- `internal/staging/artifactfs/store.go:163` — `(s *Store).resolve`
- `internal/staging/artifactfs/store.go:179` — `stored`
- `internal/staging/artifactfs/store.go:183` — `verifyFile`
- `internal/staging/artifactfs/store.go:206` — `syncDirectory`
- `internal/staging/artifactfs/store.go:220` — `(r *contextReader).Read`
- `internal/staging/bootstrap/bootstrap.go:25` — `Open`
- `internal/staging/bootstrap/bootstrap.go:87` — `(a stagingAuthorizationAdapter).Authorize`
- `internal/staging/catalog.go:25` — `LoadRepositoryCatalog`
- `internal/staging/catalog.go:98` — `(c *FileRepositoryCatalog).List`
- `internal/staging/catalog.go:107` — `(c *FileRepositoryCatalog).Get`
- `internal/staging/catalog.go:116` — `(c *FileRepositoryCatalog).Validate`
- `internal/staging/catalog.go:127` — `(c *FileRepositoryCatalog).ValidateRootSeparation`
- `internal/staging/catalog.go:137` — `TargetAllowed`
- `internal/staging/domain.go:21` — `(s WorkspaceStatus).Valid`
- `internal/staging/domain.go:30` — `(s WorkspaceStatus).Terminal`
- `internal/staging/domain.go:47` — `(s PromotionStatus).Valid`
- `internal/staging/domain.go:56` — `(s PromotionStatus).Terminal`
- `internal/staging/gitexec/backend.go:28` — `New`
- `internal/staging/gitexec/backend.go:52` — `(b *Backend).ValidateRepository`
- `internal/staging/gitexec/backend.go:102` — `(b *Backend).ResolveCommit`
- `internal/staging/gitexec/backend.go:117` — `(b *Backend).CreateWorktree`
- `internal/staging/gitexec/backend.go:144` — `(b *Backend).InspectWorktree`
- `internal/staging/gitexec/backend.go:173` — `(b *Backend).SealWorktree`
- `internal/staging/gitexec/backend.go:264` — `(b *Backend).RemoveWorktree`
- `internal/staging/gitexec/backend.go:288` — `(b *Backend).ReadTarget`
- `internal/staging/gitexec/backend.go:299` — `(b *Backend).PromoteRef`
- `internal/staging/gitexec/backend.go:332` — `(b *Backend).validateWorkspaceRef`
- `internal/staging/gitexec/backend.go:345` — `(b *Backend).ensureTargetNotCheckedOut`
- `internal/staging/gitexec/backend.go:358` — `(b *Backend).changedFiles`
- `internal/staging/gitexec/backend.go:366` — `(b *Backend).changedFilesCached`
- `internal/staging/gitexec/backend.go:374` — `parseRawDiff`
- `internal/staging/gitexec/backend.go:408` — `marshalManifest`
- `internal/staging/gitexec/backend.go:417` — `newCanonicalJSONEncoder`
- `internal/staging/gitexec/backend.go:435` — `validateChangedSymlinks`
- `internal/staging/gitexec/backend.go:458` — `inspectAttributes`
- `internal/staging/gitexec/backend.go:484` — `rejectNestedRepositories`
- `internal/staging/gitexec/backend.go:505` — `(b *Backend).output`
- `internal/staging/gitexec/backend.go:510` — `(b *Backend).outputEnv`
- `internal/staging/gitexec/backend.go:515` — `(b *Backend).outputBytes`
- `internal/staging/gitexec/backend.go:519` — `(b *Backend).outputBytesLimited`
- `internal/staging/gitexec/backend.go:526` — `(b *Backend).outputAllowExitOne`
- `internal/staging/gitexec/backend.go:537` — `(b *Backend).run`
- `internal/staging/gitexec/backend.go:566` — `(w *boundedBuffer).Write`
- `internal/staging/gitexec/backend.go:585` — `(b *Backend).runLimited`
- `internal/staging/gitexec/backend.go:610` — `(b *Backend).safeArgs`
- `internal/staging/gitexec/backend.go:620` — `(b *Backend).safeEnv`
- `internal/staging/gitexec/backend.go:650` — `(b *Backend).VerifySealedRevision`
- `internal/staging/postgres/helpers.go:19` — `scanWorkspace`
- `internal/staging/postgres/helpers.go:27` — `scanPromotion`
- `internal/staging/postgres/helpers.go:58` — `rollback`
- `internal/staging/postgres/helpers.go:64` — `lockWorkspace`
- `internal/staging/postgres/helpers.go:67` — `lockPromotion`
- `internal/staging/postgres/helpers.go:75` — `appendEvent`
- `internal/staging/postgres/helpers.go:119` — `insertArtifact`
- `internal/staging/postgres/helpers.go:133` — `recordTaskRequirement`
- `internal/staging/postgres/promotions.go:11` — `(s *Store).RecordCheck`
- `internal/staging/postgres/promotions.go:54` — `(s *Store).RequestPromotion`
- `internal/staging/postgres/promotions.go:93` — `(s *Store).SubmitReview`
- `internal/staging/postgres/promotions.go:160` — `maybeApprovePromotionForWorkspace`
- `internal/staging/postgres/promotions.go:193` — `(s *Store).GetPromotion`
- `internal/staging/postgres/promotions.go:196` — `(s *Store).ListPromotions`
- `internal/staging/postgres/promotions.go:217` — `(s *Store).GetPromotionApplyContext`
- `internal/staging/postgres/promotions.go:272` — `(s *Store).MarkPromotionApplied`
- `internal/staging/postgres/promotions.go:275` — `(s *Store).MarkPromotionConflicted`
- `internal/staging/postgres/promotions.go:278` — `(s *Store).MarkPromotionFailed`
- `internal/staging/postgres/promotions.go:281` — `(s *Store).CancelPromotion`
- `internal/staging/postgres/promotions.go:285` — `(s *Store).transitionPromotion`
- `internal/staging/postgres/promotions.go:312` — `(s *Store).ListRecoverablePromotions`
- `internal/staging/postgres/store.go:22` — `New`
- `internal/staging/postgres/store.go:39` — `mapError`
- `internal/staging/postgres/workspaces.go:13` — `(s *Store).CreateProvisioning`
- `internal/staging/postgres/workspaces.go:45` — `(s *Store).ActivateWorkspace`
- `internal/staging/postgres/workspaces.go:68` — `(s *Store).FailWorkspace`
- `internal/staging/postgres/workspaces.go:91` — `(s *Store).SealWorkspace`
- `internal/staging/postgres/workspaces.go:133` — `(s *Store).AbandonWorkspace`
- `internal/staging/postgres/workspaces.go:156` — `(s *Store).BeginCleanup`
- `internal/staging/postgres/workspaces.go:186` — `(s *Store).CompleteCleanup`
- `internal/staging/postgres/workspaces.go:209` — `(s *Store).GetWorkspace`
- `internal/staging/postgres/workspaces.go:212` — `(s *Store).ListWorkspaces`
- `internal/staging/postgres/workspaces.go:233` — `(s *Store).ListStaleWorkspaces`
- `internal/staging/postgres/workspaces.go:252` — `(s *Store).ListWorkspaceKeys`
- `internal/staging/postgres/workspaces.go:268` — `(s *Store).ListCleanupPending`
- `internal/staging/reconcile.go:13` — `(s *Service).Reconcile`
- `internal/staging/reconcile.go:122` — `(r ReconcileResult).Changed`
- `internal/staging/reconcile.go:125` — `(r ReconcileResult).String`
- `internal/staging/service.go:39` — `NewService`
- `internal/staging/service.go:59` — `PrepareRoots`
- `internal/staging/service.go:74` — `(s *Service).CreateWorkspace`
- `internal/staging/service.go:150` — `(s *Service).InspectWorkspace`
- `internal/staging/service.go:158` — `(s *Service).SealWorkspace`
- `internal/staging/service.go:208` — `(s *Service).AbandonWorkspace`
- `internal/staging/service.go:221` — `(s *Service).CleanupWorkspace`
- `internal/staging/service.go:245` — `(s *Service).RecordCheck`
- `internal/staging/service.go:266` — `(s *Service).RequestPromotion`
- `internal/staging/service.go:279` — `(s *Service).SubmitReview`
- `internal/staging/service.go:308` — `(s *Service).ApplyPromotion`
- `internal/staging/service.go:363` — `(s *Service).verifyPromotionArtifacts`
- `internal/staging/service.go:415` — `(s *Service).CancelPromotion`
- `internal/staging/service.go:435` — `(s *Service).VerifyArtifact`
- `internal/staging/service.go:442` — `(s *Service).GetWorkspace`
- `internal/staging/service.go:445` — `(s *Service).ListWorkspaces`
- `internal/staging/service.go:448` — `(s *Service).GetPromotion`
- `internal/staging/service.go:451` — `(s *Service).ListPromotions`
- `internal/staging/service.go:454` — `(s *Service).workspaceRef`
- `internal/staging/service.go:457` — `(s *Service).authorize`
- `internal/staging/service.go:466` — `safeReason`
- `internal/staging/state_machine.go:5` — `ValidateWorkspaceTransition`
- `internal/staging/state_machine.go:23` — `ValidatePromotionTransition`
- `internal/staging/validation.go:20` — `ValidateRepositoryID`
- `internal/staging/validation.go:27` — `ValidateWorkspaceKey`
- `internal/staging/validation.go:34` — `ValidateCommit`
- `internal/staging/validation.go:41` — `ValidateDigest`
- `internal/staging/validation.go:48` — `ValidateTargetRef`
- `internal/staging/validation.go:55` — `ValidateOpaqueReference`
- `internal/staging/validation.go:63` — `CanonicalRoot`
- `internal/staging/validation.go:84` — `ValidateSeparateRoots`
- `internal/staging/validation.go:103` — `PathWithin`
- `internal/staging/validation.go:118` — `pathContains`
- `internal/staging/validation.go:123` — `IsNotExist`

## internal/tasks

Tracked artifacts: 29; kinds: go_source=21, test_only_signal=8

Production Go paths:
- `internal/tasks/backoff.go`
- `internal/tasks/contextprovider/provider.go`
- `internal/tasks/domain.go`
- `internal/tasks/errors.go`
- `internal/tasks/interfaces.go`
- `internal/tasks/postgres/create.go`
- `internal/tasks/postgres/helpers.go`
- `internal/tasks/postgres/lease_verification.go`
- `internal/tasks/postgres/mutate.go`
- `internal/tasks/postgres/outbox.go`
- `internal/tasks/postgres/queue.go`
- `internal/tasks/postgres/read.go`
- `internal/tasks/postgres/reconcile.go`
- `internal/tasks/postgres/requirement_verification.go`
- `internal/tasks/postgres/specific_claim.go`
- `internal/tasks/postgres/store.go`
- `internal/tasks/registryadapter/adapter.go`
- `internal/tasks/service.go`
- `internal/tasks/specific_claim.go`
- `internal/tasks/state_machine.go`
- `internal/tasks/validation.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/tasks/backoff.go:10` — `(p RetryPolicy).Delay`
- `internal/tasks/contextprovider/provider.go:17` — `New`
- `internal/tasks/contextprovider/provider.go:24` — `(p *Provider).GetTaskContext`
- `internal/tasks/contextprovider/provider.go:72` — `(p *Provider).ValidateVersion`
- `internal/tasks/contextprovider/provider.go:97` — `parseRef`
- `internal/tasks/contextprovider/provider.go:145` — `sourceRecord`
- `internal/tasks/domain.go:33` — `(s Status).Valid`
- `internal/tasks/domain.go:35` — `(s Status).Terminal`
- `internal/tasks/domain.go:54` — `(t RequirementType).Valid`
- `internal/tasks/postgres/create.go:18` — `(s *Store).Create`
- `internal/tasks/postgres/helpers.go:25` — `qualifyColumns`
- `internal/tasks/postgres/helpers.go:33` — `scanTask`
- `internal/tasks/postgres/helpers.go:79` — `rollback`
- `internal/tasks/postgres/helpers.go:85` — `lockTask`
- `internal/tasks/postgres/helpers.go:89` — `transitionTask`
- `internal/tasks/postgres/helpers.go:133` — `appendTaskEvent`
- `internal/tasks/postgres/helpers.go:217` — `statusValue`
- `internal/tasks/postgres/helpers.go:224` — `newToken`
- `internal/tasks/postgres/helpers.go:234` — `hashToken`
- `internal/tasks/postgres/helpers.go:239` — `nullableString`
- `internal/tasks/postgres/helpers.go:246` — `requiredValue`
- `internal/tasks/postgres/lease_verification.go:12` — `(s *Store).VerifyActiveExecutionLease`
- `internal/tasks/postgres/mutate.go:14` — `(s *Store).AddDependency`
- `internal/tasks/postgres/mutate.go:71` — `(s *Store).AddRequirement`
- `internal/tasks/postgres/mutate.go:101` — `(s *Store).RecordEvidence`
- `internal/tasks/postgres/mutate.go:167` — `(s *Store).Finalize`
- `internal/tasks/postgres/mutate.go:219` — `(s *Store).Block`
- `internal/tasks/postgres/mutate.go:249` — `(s *Store).Unblock`
- `internal/tasks/postgres/mutate.go:279` — `(s *Store).Cancel`
- `internal/tasks/postgres/mutate.go:306` — `touchTask`
- `internal/tasks/postgres/mutate.go:324` — `inspectDependencies`
- `internal/tasks/postgres/mutate.go:338` — `activeLeaseAttempt`
- `internal/tasks/postgres/mutate.go:350` — `revokeLease`
- `internal/tasks/postgres/outbox.go:14` — `(s *Store).ClaimOutbox`
- `internal/tasks/postgres/outbox.go:62` — `(s *Store).AckOutbox`
- `internal/tasks/postgres/outbox.go:88` — `(s *Store).NackOutbox`
- `internal/tasks/postgres/outbox.go:119` — `(s *Store).RecoverOutbox`
- `internal/tasks/postgres/outbox.go:125` — `recoverOutboxTx`
- `internal/tasks/postgres/outbox.go:169` — `(s *Store).OutboxStats`
- `internal/tasks/postgres/outbox.go:181` — `scanOutbox`
- `internal/tasks/postgres/outbox.go:196` — `lockOutbox`
- `internal/tasks/postgres/outbox.go:219` — `verifyOutboxClaim`
- `internal/tasks/postgres/queue.go:23` — `(s *Store).Claim`
- `internal/tasks/postgres/queue.go:75` — `claimOne`
- `internal/tasks/postgres/queue.go:108` — `(s *Store).StartAttempt`
- `internal/tasks/postgres/queue.go:138` — `(s *Store).Heartbeat`
- `internal/tasks/postgres/queue.go:167` — `(s *Store).RecordAttemptResult`
- `internal/tasks/postgres/queue.go:223` — `verifyLease`
- `internal/tasks/postgres/queue.go:249` — `finishAttempt`
- `internal/tasks/postgres/queue.go:274` — `failureReason`
- `internal/tasks/postgres/read.go:12` — `(s *Store).GetTask`
- `internal/tasks/postgres/read.go:42` — `(s *Store).ListTasks`
- `internal/tasks/postgres/read.go:83` — `(s *Store).ListEvents`
- `internal/tasks/postgres/read.go:107` — `(s *Store).ListAttempts`
- `internal/tasks/postgres/read.go:127` — `(s *Store).ListDeadLetters`
- `internal/tasks/postgres/read.go:147` — `(s *Store).GetDeadLetter`
- `internal/tasks/postgres/read.go:154` — `listDependencyTasks`
- `internal/tasks/postgres/read.go:175` — `listRequirements`
- `internal/tasks/postgres/read.go:195` — `listEvidence`
- `internal/tasks/postgres/read.go:215` — `scanRequirement`
- `internal/tasks/postgres/read.go:221` — `scanEvidence`
- `internal/tasks/postgres/read.go:234` — `scanAttempt`
- `internal/tasks/postgres/read.go:240` — `scanLease`
- `internal/tasks/postgres/read.go:246` — `scanDeadLetter`
- `internal/tasks/postgres/read.go:252` — `statusesText`
- `internal/tasks/postgres/reconcile.go:12` — `(s *Store).Reconcile`
- `internal/tasks/postgres/reconcile.go:36` — `reconcileExpiredLeases`
- `internal/tasks/postgres/reconcile.go:108` — `reconcileTaskReadiness`
- `internal/tasks/postgres/reconcile.go:205` — `updateBlockedReason`
- `internal/tasks/postgres/requirement_verification.go:12` — `(s *Store).RecordRequirementVerification`
- `internal/tasks/postgres/specific_claim.go:11` — `(s *Store).ClaimSpecific`
- `internal/tasks/postgres/store.go:21` — `New`
- `internal/tasks/postgres/store.go:35` — `mapError`
- `internal/tasks/registryadapter/adapter.go:16` — `New`
- `internal/tasks/registryadapter/adapter.go:23` — `(a *Adapter).CurrentRevision`
- `internal/tasks/registryadapter/adapter.go:34` — `(a *Adapter).GetRole`
- `internal/tasks/registryadapter/adapter.go:45` — `mapError`
- `internal/tasks/service.go:21` — `(cfg Config).Validate`
- `internal/tasks/service.go:49` — `NewService`
- `internal/tasks/service.go:62` — `(s *Service).GetTask`
- `internal/tasks/service.go:85` — `(s *Service).ListTasks`
- `internal/tasks/service.go:119` — `(s *Service).ListEvents`
- `internal/tasks/service.go:132` — `(s *Service).ListAttempts`
- `internal/tasks/service.go:139` — `(s *Service).ListDeadLetters`
- `internal/tasks/service.go:167` — `(s *Service).GetDeadLetter`
- `internal/tasks/service.go:174` — `(s *Service).CreateTask`
- `internal/tasks/service.go:225` — `(s *Service).AddDependency`
- `internal/tasks/service.go:239` — `(s *Service).AddRequirement`
- `internal/tasks/service.go:255` — `(s *Service).RecordEvidence`
- `internal/tasks/service.go:268` — `(s *Service).RecordRequirementVerification`
- `internal/tasks/service.go:290` — `(s *Service).ClaimTasks`
- `internal/tasks/service.go:329` — `(s *Service).StartAttempt`
- `internal/tasks/service.go:336` — `(s *Service).Heartbeat`
- `internal/tasks/service.go:346` — `(s *Service).RecordAttemptResult`
- `internal/tasks/service.go:371` — `(s *Service).FinalizeTask`
- `internal/tasks/service.go:393` — `(s *Service).BlockTask`
- `internal/tasks/service.go:406` — `(s *Service).UnblockTask`
- `internal/tasks/service.go:425` — `(s *Service).CancelTask`
- `internal/tasks/service.go:438` — `(s *Service).Reconcile`
- `internal/tasks/service.go:448` — `(s *Service).ClaimOutbox`
- `internal/tasks/service.go:468` — `(s *Service).AckOutbox`
- `internal/tasks/service.go:476` — `(s *Service).NackOutbox`
- `internal/tasks/service.go:485` — `(s *Service).RecoverOutbox`
- `internal/tasks/service.go:495` — `(s *Service).OutboxStats`
- `internal/tasks/service.go:499` — `(s *Service).validateLeaseCommand`
- `internal/tasks/service.go:515` — `(s *Service).validateAssignee`
- `internal/tasks/service.go:533` — `roleAssignable`
- `internal/tasks/service.go:537` — `normalizeActor`
- `internal/tasks/service.go:546` — `validateActor`
- `internal/tasks/service.go:556` — `mapCatalogError`
- `internal/tasks/service.go:566` — `validateOutboxDisposition`
- `internal/tasks/specific_claim.go:13` — `(s *Service).ClaimTaskByID`
- `internal/tasks/state_machine.go:29` — `CanTransition`
- `internal/tasks/state_machine.go:37` — `ValidateTransition`
- `internal/tasks/validation.go:33` — `DecodeCreateRequest`
- `internal/tasks/validation.go:41` — `DecodeAttemptResult`
- `internal/tasks/validation.go:49` — `DecodeRequirementSpec`
- `internal/tasks/validation.go:57` — `decodeStrict`
- `internal/tasks/validation.go:97` — `findForbiddenKey`
- `internal/tasks/validation.go:124` — `NormalizeCreateRequest`
- `internal/tasks/validation.go:147` — `ValidateCreateRequest`
- `internal/tasks/validation.go:212` — `ValidateRequirementSpec`
- `internal/tasks/validation.go:225` — `ValidateEvidence`
- `internal/tasks/validation.go:257` — `HashCreateRequest`
- `internal/tasks/validation.go:266` — `NormalizeAvailableAt`
- `internal/tasks/validation.go:283` — `rejectSecrets`

## internal/testdbguard

Tracked artifacts: 2; kinds: go_source=1, test_only_signal=1

Production Go paths:
- `internal/testdbguard/testdbguard.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/testdbguard/testdbguard.go:57` — `RequireTestDatabase`
- `internal/testdbguard/testdbguard.go:84` — `RequireDestructive`

## internal/webevidence

Tracked artifacts: 11; kinds: go_source=6, test_only_signal=5

Production Go paths:
- `internal/webevidence/ingest.go`
- `internal/webevidence/postgres/store.go`
- `internal/webevidence/rank.go`
- `internal/webevidence/sanitize.go`
- `internal/webevidence/store.go`
- `internal/webevidence/types.go`

Function/method locators (mechanical index, not positive decisions):
- `internal/webevidence/ingest.go:33` — `Ingest`
- `internal/webevidence/postgres/store.go:24` — `New`
- `internal/webevidence/postgres/store.go:37` — `(s *Store).Save`
- `internal/webevidence/postgres/store.go:71` — `(s *Store).Get`
- `internal/webevidence/postgres/store.go:88` — `(s *Store).ListForTask`
- `internal/webevidence/postgres/store.go:114` — `(s *Store).Reap`
- `internal/webevidence/postgres/store.go:132` — `scanEvidence`
- `internal/webevidence/rank.go:23` — `tokenize`
- `internal/webevidence/rank.go:31` — `keywordOverlapScore`
- `internal/webevidence/rank.go:45` — `cosineSimilarity`
- `internal/webevidence/rank.go:70` — `RankChunks`
- `internal/webevidence/sanitize.go:45` — `Sanitize`
- `internal/webevidence/sanitize.go:58` — `chunkText`
- `internal/webevidence/sanitize.go:84` — `detectInjection`
- `internal/webevidence/types.go:46` — `(p RawPage).contentHash`
- `internal/webevidence/types.go:88` — `(e Evidence).Validate`
- `internal/webevidence/types.go:133` — `(e Evidence).Expired`
- `internal/webevidence/types.go:153` — `NewCitation`

## internal/webevidencefixtures

Tracked artifacts: 5; kinds: test_only_signal=5

Production Go paths:
- `internal/webevidencefixtures/activate.go`
- `internal/webevidencefixtures/render.go`
- `internal/webevidencefixtures/runner.go`

## investigacion

Tracked artifacts: 4; kinds: configuration_or_declaration=4

Non-Go artifacts:
- `investigacion/AGENT.md` (configuration_or_declaration)
- `investigacion/auditor_cerebro_empresa/PERFIL.md` (configuration_or_declaration)
- `investigacion/research_worker_hourly/PERFIL.md` (configuration_or_declaration)
- `investigacion/research_worker_hourly_mimo_canary/PERFIL.md` (configuration_or_declaration)

## migrations

Tracked artifacts: 99; kinds: go_source=1, migration=96, test_only_signal=2

Production Go paths:
- `migrations/embed.go`

Non-Go artifacts:
- `migrations/000001_create_audit_events.down.sql` (migration)
- `migrations/000001_create_audit_events.up.sql` (migration)
- `migrations/000002_create_organization_registry.down.sql` (migration)
- `migrations/000002_create_organization_registry.up.sql` (migration)
- `migrations/000003_create_durable_task_engine.down.sql` (migration)
- `migrations/000003_create_durable_task_engine.up.sql` (migration)
- `migrations/000004_create_staging_promotion_engine.down.sql` (migration)
- `migrations/000004_create_staging_promotion_engine.up.sql` (migration)
- `migrations/000005_create_capability_policy_engine.down.sql` (migration)
- `migrations/000005_create_capability_policy_engine.up.sql` (migration)
- `migrations/000006_create_context_engine.down.sql` (migration)
- `migrations/000006_create_context_engine.up.sql` (migration)
- `migrations/000007_create_model_runtime_gateway.down.sql` (migration)
- `migrations/000007_create_model_runtime_gateway.up.sql` (migration)
- `migrations/000008_create_model_egress_authorization.down.sql` (migration)
- `migrations/000008_create_model_egress_authorization.up.sql` (migration)
- `migrations/000009_create_model_dispatcher_assignments.down.sql` (migration)
- `migrations/000009_create_model_dispatcher_assignments.up.sql` (migration)
- `migrations/000010_create_model_execution_identity.down.sql` (migration)
- `migrations/000010_create_model_execution_identity.up.sql` (migration)
- `migrations/000011_create_model_provider_adapter.down.sql` (migration)
- `migrations/000011_create_model_provider_adapter.up.sql` (migration)
- `migrations/000012_create_durable_decision_graph.down.sql` (migration)
- `migrations/000012_create_durable_decision_graph.up.sql` (migration)
- `migrations/000013_create_bounded_self_improvement.down.sql` (migration)
- `migrations/000013_create_bounded_self_improvement.up.sql` (migration)
- `migrations/000014_create_shadow_verifier.down.sql` (migration)
- `migrations/000014_create_shadow_verifier.up.sql` (migration)
- `migrations/000015_create_organizational_memory.down.sql` (migration)
- `migrations/000015_create_organizational_memory.up.sql` (migration)
- `migrations/000016_create_skill_registry.down.sql` (migration)
- `migrations/000016_create_skill_registry.up.sql` (migration)
- `migrations/000017_create_approved_knowledge_rag.down.sql` (migration)
- `migrations/000017_create_approved_knowledge_rag.up.sql` (migration)
- `migrations/000018_make_provider_outcomes_transport_aware.down.sql` (migration)
- `migrations/000018_make_provider_outcomes_transport_aware.up.sql` (migration)
- `migrations/000019_enforce_rag_document_version_namespace_match.down.sql` (migration)
- `migrations/000019_enforce_rag_document_version_namespace_match.up.sql` (migration)
- `migrations/000020_create_model_pricing.down.sql` (migration)
- `migrations/000020_create_model_pricing.up.sql` (migration)
- `migrations/000021_create_provider_wallets.down.sql` (migration)
- `migrations/000021_create_provider_wallets.up.sql` (migration)
- `migrations/000022_create_agent_budgets.down.sql` (migration)
- `migrations/000022_create_agent_budgets.up.sql` (migration)
- `migrations/000023_create_task_budgets.down.sql` (migration)
- `migrations/000023_create_task_budgets.up.sql` (migration)
- `migrations/000024_create_agent_messaging.down.sql` (migration)
- `migrations/000024_create_agent_messaging.up.sql` (migration)
- `migrations/000025_enforce_wallet_single_terminal.down.sql` (migration)
- `migrations/000025_enforce_wallet_single_terminal.up.sql` (migration)
- `migrations/000026_add_gemini_flash_pricing.down.sql` (migration)
- `migrations/000026_add_gemini_flash_pricing.up.sql` (migration)
- `migrations/000027_add_model_pricing_billing_mode.down.sql` (migration)
- `migrations/000027_add_model_pricing_billing_mode.up.sql` (migration)
- `migrations/000028_create_embedding_derived_tables.down.sql` (migration)
- `migrations/000028_create_embedding_derived_tables.up.sql` (migration)
- `migrations/000029_add_identifier_token_channel.down.sql` (migration)
- `migrations/000029_add_identifier_token_channel.up.sql` (migration)
- `migrations/000030_extend_wallet_for_embedding_invocations.down.sql` (migration)
- `migrations/000030_extend_wallet_for_embedding_invocations.up.sql` (migration)
- `migrations/000031_create_evaluation_runs.down.sql` (migration)
- `migrations/000031_create_evaluation_runs.up.sql` (migration)
- `migrations/000032_create_bge_m3_embedding_tables.down.sql` (migration)
- `migrations/000032_create_bge_m3_embedding_tables.up.sql` (migration)
- `migrations/000033_create_web_evidence.down.sql` (migration)
- `migrations/000033_create_web_evidence.up.sql` (migration)
- `migrations/000034_add_memory_backfill_embedding_operation.down.sql` (migration)
- `migrations/000034_add_memory_backfill_embedding_operation.up.sql` (migration)
- `migrations/000035_add_chunk_media_source.down.sql` (migration)
- `migrations/000035_add_chunk_media_source.up.sql` (migration)
- `migrations/000036_add_chunk_provenance.down.sql` (migration)
- `migrations/000036_add_chunk_provenance.up.sql` (migration)
- `migrations/000037_add_cost_settlement_provenance.down.sql` (migration)
- `migrations/000037_add_cost_settlement_provenance.up.sql` (migration)
- `migrations/000038_add_provider_failure_telemetry.down.sql` (migration)
- `migrations/000038_add_provider_failure_telemetry.up.sql` (migration)
- `migrations/000039_add_subscription_billing_provenance.down.sql` (migration)
- `migrations/000039_add_subscription_billing_provenance.up.sql` (migration)
- `migrations/000040_add_provider_render_telemetry.down.sql` (migration)
- `migrations/000040_add_provider_render_telemetry.up.sql` (migration)
- `migrations/000041_harden_rag_knowledge_version_immutability.down.sql` (migration)
- `migrations/000041_harden_rag_knowledge_version_immutability.up.sql` (migration)
- `migrations/000042_add_agent_message_authorization_and_hardening.down.sql` (migration)
- `migrations/000042_add_agent_message_authorization_and_hardening.up.sql` (migration)
- `migrations/000043_restrict_agent_message_type.down.sql` (migration)
- `migrations/000043_restrict_agent_message_type.up.sql` (migration)
- `migrations/000044_make_egress_revision_ownership_restorable.down.sql` (migration)
- `migrations/000044_make_egress_revision_ownership_restorable.up.sql` (migration)
- `migrations/000045_make_audit_events_immutable.down.sql` (migration)
- `migrations/000045_make_audit_events_immutable.up.sql` (migration)
- `migrations/000046_recognize_historical_egress_revision_bindings.down.sql` (migration)
- `migrations/000046_recognize_historical_egress_revision_bindings.up.sql` (migration)
- `migrations/000047_seed_openai_responses_pricing_and_wallet.down.sql` (migration)
- `migrations/000047_seed_openai_responses_pricing_and_wallet.up.sql` (migration)
- `migrations/000048_enforce_single_active_execution_principal_per_role.down.sql` (migration)
- `migrations/000048_enforce_single_active_execution_principal_per_role.up.sql` (migration)

## negocio

Tracked artifacts: 19; kinds: configuration_or_declaration=19

Non-Go artifacts:
- `negocio/AGENT.md` (configuration_or_declaration)
- `negocio/administrador_financiero/PERFIL.md` (configuration_or_declaration)
- `negocio/analista_audiencias/PERFIL.md` (configuration_or_declaration)
- `negocio/analista_costos/PERFIL.md` (configuration_or_declaration)
- `negocio/analista_kpis/PERFIL.md` (configuration_or_declaration)
- `negocio/analista_performance/PERFIL.md` (configuration_or_declaration)
- `negocio/community_manager/PERFIL.md` (configuration_or_declaration)
- `negocio/copywriter/PERFIL.md` (configuration_or_declaration)
- `negocio/director_negocio/PERFIL.md` (configuration_or_declaration)
- `negocio/disenador/PERFIL.md` (configuration_or_declaration)
- `negocio/editor_contenido_marca/PERFIL.md` (configuration_or_declaration)
- `negocio/editor_video/PERFIL.md` (configuration_or_declaration)
- `negocio/estratega_crecimiento/PERFIL.md` (configuration_or_declaration)
- `negocio/estratega_expansion/PERFIL.md` (configuration_or_declaration)
- `negocio/fotografo/PERFIL.md` (configuration_or_declaration)
- `negocio/ilustrador/PERFIL.md` (configuration_or_declaration)
- `negocio/ingeniero_industrial/PERFIL.md` (configuration_or_declaration)
- `negocio/investigador_consumidor/PERFIL.md` (configuration_or_declaration)
- `negocio/rrpp_alianzas/PERFIL.md` (configuration_or_declaration)

## recursos_agenticos

Tracked artifacts: 2; kinds: configuration_or_declaration=2

Non-Go artifacts:
- `recursos_agenticos/AGENT.md` (configuration_or_declaration)
- `recursos_agenticos/desarrollo_organizacional/PERFIL.md` (configuration_or_declaration)

## root

Tracked artifacts: 16; kinds: configuration_or_declaration=4, deployment_or_build_infrastructure=2, documentation_or_data=6, external_dependency_declaration=2, other=2

Non-Go artifacts:
- `.dockerignore` (other)
- `.env.example` (configuration_or_declaration)
- `.gitignore` (other)
- `AGENT.md` (configuration_or_declaration)
- `Dockerfile` (deployment_or_build_infrastructure)
- `HANDOFF-2026-08-10-noche.md` (documentation_or_data)
- `HANDOFF-bgem3-sidecar.md` (documentation_or_data)
- `HANDOFF-knowledge-ingestion-phase1.md` (documentation_or_data)
- `HANDOFF-rag-canary.md` (documentation_or_data)
- `Makefile` (deployment_or_build_infrastructure)
- `POST_INCIDENT_VALIDATION.md` (documentation_or_data)
- `README.md` (documentation_or_data)
- `compose.integration.yaml` (configuration_or_declaration)
- `compose.yaml` (configuration_or_declaration)
- `go.mod` (external_dependency_declaration)
- `go.sum` (external_dependency_declaration)

## scripts

Tracked artifacts: 31; kinds: documentation_or_data=2, supporting_script_signal=29

Non-Go artifacts:
- `scripts/check-alibaba-cli-fitness.sh` (supporting_script_signal)
- `scripts/check-authorization-fitness.sh` (supporting_script_signal)
- `scripts/check-cellworker-fitness.sh` (supporting_script_signal)
- `scripts/check-completion-fitness.sh` (supporting_script_signal)
- `scripts/check-context-fitness.sh` (supporting_script_signal)
- `scripts/check-decisiongraph-fitness.sh` (supporting_script_signal)
- `scripts/check-egress-restorability-fitness.sh` (supporting_script_signal)
- `scripts/check-embeddingruntime-fitness.sh` (supporting_script_signal)
- `scripts/check-executive-fitness.sh` (supporting_script_signal)
- `scripts/check-improvement-fitness.sh` (supporting_script_signal)
- `scripts/check-integration-evidence-fitness.sh` (supporting_script_signal)
- `scripts/check-memory-fitness.sh` (supporting_script_signal)
- `scripts/check-model-dispatch-fitness.sh` (supporting_script_signal)
- `scripts/check-model-egress-fitness.sh` (supporting_script_signal)
- `scripts/check-model-identity-fitness.sh` (supporting_script_signal)
- `scripts/check-model-provider-fitness.sh` (supporting_script_signal)
- `scripts/check-model-runtime-fitness.sh` (supporting_script_signal)
- `scripts/check-parallel-worker-isolation.sh` (supporting_script_signal)
- `scripts/check-pdfingest-fitness.sh` (supporting_script_signal)
- `scripts/check-rag-fitness.sh` (supporting_script_signal)
- `scripts/check-skillregistry-fitness.sh` (supporting_script_signal)
- `scripts/check-staging-fitness.sh` (supporting_script_signal)
- `scripts/check-task-fitness.sh` (supporting_script_signal)
- `scripts/check-testdbguard-fitness.sh` (supporting_script_signal)
- `scripts/check-webevidence-fitness.sh` (supporting_script_signal)
- `scripts/integration-preconditions.tsv` (documentation_or_data)
- `scripts/integration-suites.tsv` (documentation_or_data)
- `scripts/test-executive-integration.sh` (supporting_script_signal)
- `scripts/test-integration.sh` (supporting_script_signal)
- `scripts/validate_branch0.py` (supporting_script_signal)
- `scripts/verify-context-source-image.sh` (supporting_script_signal)

## servicios

Tracked artifacts: 8; kinds: configuration_or_declaration=8

Non-Go artifacts:
- `servicios/AGENT.md` (configuration_or_declaration)
- `servicios/analista_calidad/PERFIL.md` (configuration_or_declaration)
- `servicios/disenador_uxui/PERFIL.md` (configuration_or_declaration)
- `servicios/operaciones_servicio/PERFIL.md` (configuration_or_declaration)
- `servicios/product_manager_portafolio/PERFIL.md` (configuration_or_declaration)
- `servicios/responsable_datos/PERFIL.md` (configuration_or_declaration)
- `servicios/service_designer/PERFIL.md` (configuration_or_declaration)
- `servicios/soporte_usuario/PERFIL.md` (configuration_or_declaration)

## tools

Tracked artifacts: 7; kinds: supporting_script_signal=4, test_only_signal=3

Non-Go artifacts:
- `tools/instrumentv4/loop_controller.binding.patch` (supporting_script_signal)
- `tools/instrumentv4/q3_002_campaign_entry_gate.py` (supporting_script_signal)
- `tools/instrumentv4/q3_002_campaign_entry_gate_test.py` (supporting_script_signal)
- `tools/instrumentv4/replay_controller.py` (supporting_script_signal)
- `tools/instrumentv4/testdata/legitimate-narrowing.json` (test_only_signal)
- `tools/instrumentv4/testdata/q3-001-target-drift-assignment.json` (test_only_signal)
- `tools/instrumentv4/testdata/q3-002-clean-initial-state.json` (test_only_signal)

# Registered runtime universe summary

Accessible tables/views: 103 (all registered; zero-row units retained).

Non-empty runtime units:
- `public.agent_messages` — rows=4
- `public.audit_events` — rows=138
- `public.authorization_decisions` — rows=42
- `public.authorization_requests` — rows=42
- `public.authorization_uses` — rows=42
- `public.embedding_invocations` — rows=393
- `public.model_capability_snapshots` — rows=7
- `public.model_egress_policy_versions` — rows=1
- `public.model_egress_revision_bindings` — rows=1
- `public.model_egress_rules` — rows=14
- `public.model_execution_identity_policy_versions` — rows=1
- `public.model_execution_principals` — rows=3
- `public.model_pricing` — rows=17
- `public.model_profile_versions` — rows=7
- `public.model_profiles` — rows=7
- `public.model_providers` — rows=4
- `public.organization_registry_revision_documents` — rows=36
- `public.organization_registry_revisions` — rows=4
- `public.organization_reporting_lines` — rows=200
- `public.organization_roles` — rows=48
- `public.organizational_units` — rows=6
- `public.organizations` — rows=1
- `public.outbox_events` — rows=1942
- `public.provider_wallet_events` — rows=785
- `public.provider_wallets` — rows=5
- `public.rag_chunk_embeddings` — rows=383
- `public.rag_index_generations` — rows=1
- `public.rag_knowledge_chunks` — rows=383
- `public.rag_knowledge_documents` — rows=16
- `public.rag_knowledge_evidence_refs` — rows=16
- `public.rag_knowledge_idempotency` — rows=16
- `public.rag_knowledge_lifecycle_events` — rows=32
- `public.rag_knowledge_versions` — rows=16
- `public.role_model_bindings` — rows=46
- `public.schema_migrations` — rows=48
- `public.tasks` — rows=3

Registered categorical observations:
- `public.agent_messages.message_type` = `completion` — rows=2
- `public.agent_messages.message_type` = `delegation` — rows=2
- `public.agent_messages.status` = `delivered` — rows=4
- `public.audit_events.event_type` = `authorization.approval_consumed` — rows=42
- `public.audit_events.event_type` = `authorization.decision_approved` — rows=42
- `public.audit_events.event_type` = `authorization.request_created` — rows=42
- `public.audit_events.event_type` = `model.egress_registry_synced` — rows=1
- `public.audit_events.event_type` = `model.egress_registry_validated` — rows=1
- `public.audit_events.event_type` = `model.execution_identity_policy_synced` — rows=1
- `public.audit_events.event_type` = `model.execution_principal_registered` — rows=3
- `public.audit_events.event_type` = `model.registry_synced` — rows=1
- `public.audit_events.event_type` = `model.registry_validated` — rows=1
- `public.audit_events.event_type` = `organization.registry_synced` — rows=4
- `public.authorization_requests.capability_id` = `rag.publish_approved` — rows=42
- `public.authorization_requests.status` = `consumed` — rows=42
- `public.model_egress_policy_versions.status` = `materialized` — rows=1
- `public.model_execution_identity_policy_versions.status` = `active` — rows=1
- `public.model_execution_principals.status` = `active` — rows=3
- `public.model_profile_versions.decision_status` = `owner_confirmation_required` — rows=1
- `public.organization_registry_revisions.status` = `applied` — rows=4
- `public.organization_reporting_lines.role_id` = `empresa/ceo` — rows=4
- `public.organization_reporting_lines.role_id` = `empresa/ceo_observer` — rows=4
- `public.organization_reporting_lines.role_id` = `ingenieria_ia/arquitecto_software` — rows=4
- `public.organization_reporting_lines.role_id` = `ingenieria_ia/ciberseguridad` — rows=4
- `public.organization_reporting_lines.role_id` = `ingenieria_ia/code-runner` — rows=4
- `public.organization_reporting_lines.role_id` = `ingenieria_ia/data_engineer` — rows=4
- `public.organization_reporting_lines.role_id` = `ingenieria_ia/frontend` — rows=4
- `public.organization_reporting_lines.role_id` = `ingenieria_ia/ingeniero_ia` — rows=4
- `public.organization_reporting_lines.role_id` = `ingenieria_ia/ml_data_scientist` — rows=4
- `public.organization_reporting_lines.role_id` = `ingenieria_ia/orquestador` — rows=4
- `public.organization_reporting_lines.role_id` = `ingenieria_ia/qa` — rows=4
- `public.organization_reporting_lines.role_id` = `ingenieria_ia/razonamiento_logico` — rows=4
- `public.organization_reporting_lines.role_id` = `ingenieria_ia/semantic_engineer` — rows=4
- `public.organization_reporting_lines.role_id` = `investigacion/auditor_cerebro_empresa` — rows=8
- `public.organization_reporting_lines.role_id` = `investigacion/research_worker_hourly` — rows=8
- `public.organization_reporting_lines.role_id` = `investigacion/research_worker_hourly_mimo_canary` — rows=8
- `public.organization_reporting_lines.role_id` = `negocio/administrador_financiero` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/analista_audiencias` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/analista_costos` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/analista_kpis` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/analista_performance` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/community_manager` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/copywriter` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/director_negocio` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/disenador` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/editor_contenido_marca` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/editor_video` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/estratega_crecimiento` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/estratega_expansion` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/fotografo` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/ilustrador` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/ingeniero_industrial` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/investigador_consumidor` — rows=4
- `public.organization_reporting_lines.role_id` = `negocio/rrpp_alianzas` — rows=4
- `public.organization_reporting_lines.role_id` = `recursos_agenticos/curador_catalogo` — rows=4
- `public.organization_reporting_lines.role_id` = `recursos_agenticos/desarrollo_organizacional` — rows=4
- `public.organization_reporting_lines.role_id` = `recursos_agenticos/disenador_perfiles` — rows=4
- `public.organization_reporting_lines.role_id` = `recursos_agenticos/disenador_skills` — rows=4
- `public.organization_reporting_lines.role_id` = `recursos_agenticos/evaluador_agentes` — rows=4
- `public.organization_reporting_lines.role_id` = `recursos_agenticos/investigacion_ra` — rows=4
- `public.organization_reporting_lines.role_id` = `servicios/analista_calidad` — rows=4
- `public.organization_reporting_lines.role_id` = `servicios/disenador_uxui` — rows=4
- `public.organization_reporting_lines.role_id` = `servicios/operaciones_servicio` — rows=4
- `public.organization_reporting_lines.role_id` = `servicios/product_manager_portafolio` — rows=4
- `public.organization_reporting_lines.role_id` = `servicios/responsable_datos` — rows=4
- `public.organization_reporting_lines.role_id` = `servicios/service_designer` — rows=4
- `public.organization_reporting_lines.role_id` = `servicios/soporte_usuario` — rows=4
- `public.organizational_units.kind` = `executive_layer` — rows=1
- `public.organizational_units.kind` = `independent_audit_and_research` — rows=1
- `public.organizational_units.kind` = `operational_department` — rows=4
- `public.outbox_events.event_type` = `authorization.approval_consumed` — rows=58
- `public.outbox_events.event_type` = `authorization.decision_approved` — rows=58
- `public.outbox_events.event_type` = `authorization.request_created` — rows=58
- `public.outbox_events.event_type` = `context.policy_drift_rejected` — rows=6
- `public.outbox_events.event_type` = `context.snapshot_created` — rows=296
- `public.outbox_events.event_type` = `context.snapshot_validation_failed` — rows=6
- `public.outbox_events.event_type` = `model.invocation_ambiguous` — rows=1
- `public.outbox_events.event_type` = `model.invocation_failed` — rows=184
- `public.outbox_events.event_type` = `model.invocation_requested` — rows=278
- `public.outbox_events.event_type` = `model.invocation_succeeded` — rows=91
- `public.outbox_events.event_type` = `task.attempt_finished` — rows=80
- `public.outbox_events.event_type` = `task.attempt_started` — rows=175
- `public.outbox_events.event_type` = `task.cancelled` — rows=78
- `public.outbox_events.event_type` = `task.claimed` — rows=194
- `public.outbox_events.event_type` = `task.created` — rows=192
- `public.outbox_events.event_type` = `task.dead_lettered` — rows=3
- `public.outbox_events.event_type` = `task.failed` — rows=26
- `public.outbox_events.event_type` = `task.finalized` — rows=80
- `public.outbox_events.event_type` = `task.lease_expired` — rows=39
- `public.outbox_events.event_type` = `task.ready` — rows=39
- `public.outbox_events.status` = `pending` — rows=1942
- `public.provider_wallet_events.kind` = `committed` — rows=392
- `public.provider_wallet_events.kind` = `reserved` — rows=393
- `public.rag_index_generations.status` = `active` — rows=1
- `public.role_model_bindings.role_id` = `empresa/ceo` — rows=1
- `public.role_model_bindings.role_id` = `empresa/ceo_observer` — rows=1
- `public.role_model_bindings.role_id` = `ingenieria_ia/arquitecto_software` — rows=1
- `public.role_model_bindings.role_id` = `ingenieria_ia/ciberseguridad` — rows=1
- `public.role_model_bindings.role_id` = `ingenieria_ia/data_engineer` — rows=1
- `public.role_model_bindings.role_id` = `ingenieria_ia/frontend` — rows=1
- `public.role_model_bindings.role_id` = `ingenieria_ia/ingeniero_ia` — rows=1
- `public.role_model_bindings.role_id` = `ingenieria_ia/ml_data_scientist` — rows=1
- `public.role_model_bindings.role_id` = `ingenieria_ia/orquestador` — rows=1
- `public.role_model_bindings.role_id` = `ingenieria_ia/qa` — rows=1
- `public.role_model_bindings.role_id` = `ingenieria_ia/razonamiento_logico` — rows=1
- `public.role_model_bindings.role_id` = `ingenieria_ia/semantic_engineer` — rows=1
- `public.role_model_bindings.role_id` = `investigacion/auditor_cerebro_empresa` — rows=1
- `public.role_model_bindings.role_id` = `investigacion/research_worker_hourly` — rows=1
- `public.role_model_bindings.role_id` = `investigacion/research_worker_hourly_mimo_canary` — rows=1
- `public.role_model_bindings.role_id` = `negocio/administrador_financiero` — rows=1
- `public.role_model_bindings.role_id` = `negocio/analista_audiencias` — rows=1
- `public.role_model_bindings.role_id` = `negocio/analista_costos` — rows=1
- `public.role_model_bindings.role_id` = `negocio/analista_kpis` — rows=1
- `public.role_model_bindings.role_id` = `negocio/analista_performance` — rows=1
- `public.role_model_bindings.role_id` = `negocio/community_manager` — rows=1
- `public.role_model_bindings.role_id` = `negocio/copywriter` — rows=1
- `public.role_model_bindings.role_id` = `negocio/director_negocio` — rows=1
- `public.role_model_bindings.role_id` = `negocio/disenador` — rows=1
- `public.role_model_bindings.role_id` = `negocio/editor_contenido_marca` — rows=1
- `public.role_model_bindings.role_id` = `negocio/editor_video` — rows=1
- `public.role_model_bindings.role_id` = `negocio/estratega_crecimiento` — rows=1
- `public.role_model_bindings.role_id` = `negocio/estratega_expansion` — rows=1
- `public.role_model_bindings.role_id` = `negocio/fotografo` — rows=1
- `public.role_model_bindings.role_id` = `negocio/ilustrador` — rows=1
- `public.role_model_bindings.role_id` = `negocio/ingeniero_industrial` — rows=1
- `public.role_model_bindings.role_id` = `negocio/investigador_consumidor` — rows=1
- `public.role_model_bindings.role_id` = `negocio/rrpp_alianzas` — rows=1
- `public.role_model_bindings.role_id` = `recursos_agenticos/curador_catalogo` — rows=1
- `public.role_model_bindings.role_id` = `recursos_agenticos/desarrollo_organizacional` — rows=1
- `public.role_model_bindings.role_id` = `recursos_agenticos/disenador_perfiles` — rows=1
- `public.role_model_bindings.role_id` = `recursos_agenticos/disenador_skills` — rows=1
- `public.role_model_bindings.role_id` = `recursos_agenticos/evaluador_agentes` — rows=1
- `public.role_model_bindings.role_id` = `recursos_agenticos/investigacion_ra` — rows=1
- `public.role_model_bindings.role_id` = `servicios/analista_calidad` — rows=1
- `public.role_model_bindings.role_id` = `servicios/disenador_uxui` — rows=1
- `public.role_model_bindings.role_id` = `servicios/operaciones_servicio` — rows=1
- `public.role_model_bindings.role_id` = `servicios/product_manager_portafolio` — rows=1
- `public.role_model_bindings.role_id` = `servicios/responsable_datos` — rows=1
- `public.role_model_bindings.role_id` = `servicios/service_designer` — rows=1
- `public.role_model_bindings.role_id` = `servicios/soporte_usuario` — rows=1
- `public.tasks.status` = `no_action` — rows=3

