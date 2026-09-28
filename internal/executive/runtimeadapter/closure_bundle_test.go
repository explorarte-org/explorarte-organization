package runtimeadapter

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// Local smoke #47 (root 2066): every department review accepted, and the CEO closure reported
// partial because it held only the reviews' verdicts. The closure bundle carries each department's
// request and its reviewed deliverables.
func TestTheClosureSeesEachDepartmentsRequestAndAnswers(t *testing.T) {
	answer := "Two monthly signals: unheld bookings and active base change; collection cost unknown."
	bundle := fitClosureBundle(nil, nil, []fullDepartment{{
		id: "negocio", request: "Which two signals should the clinic track monthly?",
		deliverables: []fullDeliverable{{closureDeliverable: closureDeliverable{TaskID: 2079, RoleID: "negocio/analista_kpis"}, full: answer}},
	}})
	if len(bundle.Departments) != 1 || bundle.Departments[0].Request == "" || bundle.Departments[0].Deliverables[0].Summary != answer {
		t.Fatalf("bundle %+v", bundle.Departments)
	}
}

func TestAClosureBundleShrinksItsAnswersToFit(t *testing.T) {
	long := strings.Repeat("respuesta ", 1200) // 12000 bytes, the worker summary limit
	departments := make([]fullDepartment, 4)
	for i := range departments {
		departments[i] = fullDepartment{id: "d", request: strings.Repeat("q", closureRequestBytes)}
		for j := 0; j < 3; j++ {
			departments[i].deliverables = append(departments[i].deliverables, fullDeliverable{full: long})
		}
	}
	reviews := make([]projectedReview, 4)
	for i := range reviews {
		reviews[i] = projectedReview{Findings: []string{strings.Repeat("f", 3000)}}
	}
	bundle := fitClosureBundle(reviews, nil, departments)
	body, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > executiveEvidenceBundleBytes {
		t.Fatalf("bundle is %d bytes, over %d", len(body), executiveEvidenceBundleBytes)
	}
	if got := bundle.Departments[0].Deliverables[0].Summary; got == "" || !strings.Contains(got, "[cut by the host") {
		t.Fatalf("twelve long answers should be cut, not dropped, to fit: %d bytes", len(got))
	}
}

func TestReviewedWorkerIDsComeFromTheReviewsOwnBundle(t *testing.T) {
	var metadata map[string]any
	if err := json.Unmarshal([]byte(`{"bundle":{"workers":[{"task_id":2079},{"task_id":2077}]}}`), &metadata); err != nil {
		t.Fatal(err)
	}
	review := executive.TaskRecord{Evidence: []executive.EvidenceRecord{
		{Reference: "executive-evidence:department:negocio:1ae6377ecf36266a", Metadata: metadata},
		{Reference: "model-invocation:962"},
	}}
	if got := reviewedWorkerIDs(review); !slices.Equal(got, []int64{2077, 2079}) {
		t.Fatalf("ids %v", got)
	}
}
