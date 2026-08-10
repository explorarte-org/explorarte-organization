package endtoendfixtures

import "testing"

func TestWorkerResultCitesRealEvidenceDetectsMissingCitation(t *testing.T) {
	evidence := researchEvidence{ragChunkID: "chunk-1", memoryEntryID: "entry-1"}
	models := newFakeModelRuntime(evidence)
	if workerResultCitesRealEvidence(models, 42, evidence) {
		t.Fatal("expected no citation before any invocation is recorded")
	}
}
