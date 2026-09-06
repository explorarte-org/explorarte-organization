package organization

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

func (s *Service) HandleCreateMission(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 200 || strings.ContainsAny(idempotencyKey, "\r\n") {
		writeError(w, http.StatusBadRequest, "La campaña necesita una clave de idempotencia válida.")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "No se pudo leer el cuerpo de la solicitud")
		return
	}

	var req CreateMissionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON inválido en la solicitud de creación de misión")
		return
	}

	objective := strings.TrimSpace(req.Objective)
	if objective == "" || len(objective) > 2000 {
		writeError(w, http.StatusBadRequest, "El objetivo debe tener entre 1 y 2.000 caracteres.")
		return
	}

	if req.BudgetMicrousd <= 0 {
		writeError(w, http.StatusBadRequest, "El presupuesto debe ser un entero positivo de microUSD.")
		return
	}

	hasher := sha256.New()
	hasher.Write([]byte(idempotencyKey))
	correlationID := fmt.Sprintf("executive:%x", hasher.Sum(nil))[:42]

	createCmd := tasks.CreateRequest{
		OrganizationID:    s.cfg.Tasks.OrganizationID,
		RequestedByRoleID: "empresa/human",
		AssignedRoleID:    "empresa/ceo",
		TaskClass:         "owner.goal",
		IdempotencyKey:    idempotencyKey,
		Title:             "Executive owner goal",
		Instructions:      objective,
		AcceptanceCriteria: []string{
			"Elaborar y validar el plan ejecutivo del objetivo: " + objective,
			"Verificar cumplimiento de límites de presupuesto y gobernanza",
		},
		Priority:      100,
		MaxAttempts:   2,
		CorrelationID: correlationID,
		CausationID:   "owner:" + idempotencyKey,
	}

	task, reused, err := s.taskService.CreateTask(r.Context(), createCmd, "role", "empresa/human")
	if err != nil {
		if errors.Is(err, tasks.ErrIdempotencyConflict) {
			writeError(w, http.StatusConflict, "Esta clave ya fue usada para una campaña diferente.")
			return
		}
		s.logger.Error("failed to create mission root task", "error", err)
		writeError(w, http.StatusInternalServerError, "Error al crear la tarea en el kernel: "+err.Error())
		return
	}

	if !reused {
		if s.acceptance != nil {
			err = s.acceptance.RecordAcceptance(r.Context(), task.ID, []executive.AcceptanceCriterion{
				{Text: "Elaborar y validar el plan ejecutivo del objetivo: " + objective, Phase: executive.AcceptanceDesign},
				{Text: "Verificar cumplimiento de límites de presupuesto y gobernanza", Phase: executive.AcceptanceDesign},
			})
			if err != nil {
				s.logger.Warn("failed to record acceptance criteria", "task_id", task.ID, "error", err)
			}
		}

		if s.budgetStore != nil {
			limits := agentbudget.Limits{
				MaxUSD:        modelpricing.USDNanos(req.BudgetMicrousd * 1000),
				MaxTokens:     500000,
				MaxModelCalls: 100,
				MaxWallTimeMS: int64(30 * time.Minute / time.Millisecond),
				MaxDepth:      5,
				MaxRetries:    2,
				MaxSubagents:  10,
			}
			_, err = s.budgetStore.CreateRootBudget(r.Context(), s.cfg.Tasks.OrganizationID, task.ID, "empresa/ceo", limits, time.Now().UTC())
			if err != nil {
				s.logger.Warn("failed to create root agent budget", "task_id", task.ID, "error", err)
			}
		}
	}

	mission := Mission{
		ID:             fmt.Sprintf("MS-%d", task.ID),
		Title:          objective,
		Status:         "active",
		Department:     "empresa",
		BudgetMicrousd: req.BudgetMicrousd,
		SpentMicrousd:  0,
		Progress:       5.0,
		CompletedTasks: 0,
		TotalTasks:     1,
	}

	message := fmt.Sprintf(
		"Campaña registrada en el kernel con ID #%d. Se asignó la planificación al CEO y se estableció el techo presupuestario en $%0.2f USD.",
		task.ID,
		float64(req.BudgetMicrousd)/1e6,
	)
	if reused {
		message = fmt.Sprintf("Campaña #%d recuperada (solicitud idempotente). Ya se encuentra en curso.", task.ID)
	}

	writeJSON(w, http.StatusOK, CreateMissionResponse{
		Mission: mission,
		Message: message,
	})
}
