package organization

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type fakeTaskCreator struct {
	created []tasks.CreateRequest
	reused  bool
	err     error
}

func (f *fakeTaskCreator) CreateTask(ctx context.Context, req tasks.CreateRequest, actorType, actorID string) (tasks.Task, bool, error) {
	if f.err != nil {
		return tasks.Task{}, false, f.err
	}
	f.created = append(f.created, req)
	return tasks.Task{
		ID:             999,
		OrganizationID: req.OrganizationID,
		Title:          req.Title,
		Status:         "ready",
	}, f.reused, nil
}

type fakeAcceptanceRecorder struct {
	recorded map[int64][]executive.AcceptanceCriterion
}

func (f *fakeAcceptanceRecorder) RecordAcceptance(ctx context.Context, rootTaskID int64, criteria []executive.AcceptanceCriterion) error {
	if f.recorded == nil {
		f.recorded = make(map[int64][]executive.AcceptanceCriterion)
	}
	f.recorded[rootTaskID] = criteria
	return nil
}

type fakeBudgetCreator struct {
	created []agentbudget.Budget
}

func (f *fakeBudgetCreator) CreateRootBudget(ctx context.Context, organizationID string, rootTaskID int64, roleID string, limits agentbudget.Limits, now time.Time) (agentbudget.Budget, error) {
	b := agentbudget.Budget{
		ID:             1,
		OrganizationID: organizationID,
		RootTaskID:     rootTaskID,
		TaskID:         rootTaskID,
		RoleID:         roleID,
		Limits:         limits,
	}
	f.created = append(f.created, b)
	return b, nil
}

func TestCEOMessageValidation(t *testing.T) {
	svc := &Service{}

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"empty body", "", http.StatusBadRequest},
		{"empty message", `{"message": ""}`, http.StatusBadRequest},
		{"whitespace message", `{"message": "   "}`, http.StatusBadRequest},
		{"invalid json", `{"message": `, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/organization/ceo/messages", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			svc.HandleCEOMessage(w, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, w.Code)
			}
		})
	}
}

func TestCreateMissionValidation(t *testing.T) {
	taskFake := &fakeTaskCreator{}
	accFake := &fakeAcceptanceRecorder{}
	budgetFake := &fakeBudgetCreator{}
	svc := &Service{
		taskService: taskFake,
		acceptance:  accFake,
		budgetStore: budgetFake,
		cfg: config.Config{
			Tasks: config.TaskConfig{OrganizationID: "explorarte"},
		},
	}

	// 1. Missing Idempotency-Key
	req := httptest.NewRequest(http.MethodPost, "/api/organization/missions", strings.NewReader(`{"objective": "Plan", "budgetMicrousd": 5000000}`))
	w := httptest.NewRecorder()
	svc.HandleCreateMission(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing idempotency key, got %d", w.Code)
	}

	// 2. Invalid budget <= 0
	req = httptest.NewRequest(http.MethodPost, "/api/organization/missions", strings.NewReader(`{"objective": "Plan", "budgetMicrousd": 0}`))
	req.Header.Set("Idempotency-Key", "test-key-1")
	w = httptest.NewRecorder()
	svc.HandleCreateMission(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for zero budget, got %d", w.Code)
	}

	// 3. Valid creation
	req = httptest.NewRequest(http.MethodPost, "/api/organization/missions", strings.NewReader(`{"objective": "Auditar arquitectura y memoria", "budgetMicrousd": 5000000}`))
	req.Header.Set("Idempotency-Key", "test-key-valid")
	w = httptest.NewRecorder()
	svc.HandleCreateMission(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid mission, got %d: %s", w.Code, w.Body.String())
	}

	var res CreateMissionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if res.Mission.ID != "MS-999" {
		t.Errorf("expected mission ID MS-999, got %s", res.Mission.ID)
	}
	if res.Mission.Title != "Auditar arquitectura y memoria" {
		t.Errorf("expected title to match objective, got %s", res.Mission.Title)
	}
	if res.Mission.BudgetMicrousd != 5000000 {
		t.Errorf("expected budget 5000000, got %d", res.Mission.BudgetMicrousd)
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
