package organization

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// See owner_channel.go: the API is read-only, and the two endpoints that used to act as the owner
// refuse without reading anything (a nil pool would panic if they did).
func TestTheOwnerEndpointsRefuseAndNameTheOwnerChannel(t *testing.T) {
	mux := http.NewServeMux()
	(&Service{}).RegisterRoutes(mux)
	for _, target := range []struct{ path, body string }{
		{"/api/organization/missions", `{"objective":"audit","budgetMicrousd":9223372036854775807}`},
		{"/api/organization/ceo/messages", `{"message":"/mision lanza una campaña"}`},
	} {
		req := httptest.NewRequest(http.MethodPost, target.path, strings.NewReader(target.body))
		req.Header.Set("Idempotency-Key", "audit-local-only")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s answered %d, want 403: %s", target.path, w.Code, w.Body.String())
		}
		var body ErrorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"solo lectura", "orgctl executive chat send", "orgctl campaign approve", "orgctl campaign promote"} {
			if !strings.Contains(body.Error, want) {
				t.Errorf("%s refusal lacks %q: %s", target.path, want, body.Error)
			}
		}
	}
}

func TestAStoppedRootIsNeverShownAsActive(t *testing.T) {
	for status, want := range map[string]string{
		"pending": "active", "ready": "active", "leased": "active", "running": "active", "retry_wait": "active",
		"awaiting_verification": "review", "completed": "completed",
		"blocked": "blocked", "failed": "failed", "dead_letter": "failed", "rejected": "failed",
		"cancelled": "cancelled", "no_action": "cancelled",
	} {
		if got := missionStatus(status); got != want {
			t.Errorf("missionStatus(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestProgressComesFromTheRootsOwnChildren(t *testing.T) {
	for _, c := range []struct {
		status           string
		completed, total int64
		want             float64
	}{
		{"completed", 0, 0, 100},
		{"blocked", 0, 0, 0},
		{"blocked", 2, 3, float64(200) / 3},
		{"running", 13, 13, 95},
	} {
		if got := missionProgress(c.status, c.completed, c.total); got != c.want {
			t.Errorf("missionProgress(%q, %d, %d) = %v, want %v", c.status, c.completed, c.total, got, c.want)
		}
	}
}

func TestSnapshotSerialization(t *testing.T) {
	snap := Snapshot{
		Organization: OrganizationInfo{Name: "Psi.Explorarte"},
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
		Metrics: Metrics{
			Objectives: ObjectivesMetric{Completed: 10, Total: 20},
			Missions:   MissionsMetric{Active: 2, Completed: 5},
			Learning:   LearningMetric{Episodes: 3, Consolidated: 0},
			Skills:     SkillsMetric{Created: 1, Learned: 0},
			Memories:   MemoriesMetric{Episodic: 0, Semantic: 0, Corrective: 0},
			Cost: CostMetric{
				ActualMicrousd:    1224250,
				BudgetMicrousd:    25000000,
				EstimatedMicrousd: 5000000,
			},
		},
		Departments: []Department{
			{
				ID:       "empresa",
				Name:     "Empresa",
				Subtitle: "Dirección",
				Color:    "#b89078",
				Roles: []Role{
					{
						ID:       "empresa/ceo",
						Name:     "CEO",
						Activity: "Disponible",
						Status:   "idle",
						Progress: 0,
					},
				},
			},
		},
		Missions: []Mission{
			{
				ID:             "MS-1",
				Title:          "Misión 1",
				Status:         "active",
				Department:     "empresa",
				BudgetMicrousd: 5000000,
				SpentMicrousd:  0,
				Progress:       10,
				CompletedTasks: 1,
				TotalTasks:     4,
			},
		},
		Learning: []LearningItem{
			{
				ID:         "ep-1",
				Title:      "Episodio",
				Department: "empresa",
				Status:     "observed",
				Time:       "Hace un momento",
				Type:       "episode",
			},
		},
		Activity: []ActivityItem{
			{
				ID:   "act-1",
				Role: "empresa/ceo",
				Text: "Inició",
				Time: "Hace un momento",
			},
		},
		Spend: []SpendItem{
			{
				Label:    "Empresa",
				Microusd: 1224250,
			},
		},
		Capabilities: Capabilities{Chat: true, CreateMission: true},
	}

	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(snap); err != nil {
		t.Fatalf("failed to encode snapshot: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("failed to decode snapshot: %v", err)
	}

	for _, key := range []string{"organization", "updatedAt", "metrics", "departments", "missions", "learning", "activity", "spend", "capabilities"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("missing key %s in serialized snapshot", key)
		}
	}
}
