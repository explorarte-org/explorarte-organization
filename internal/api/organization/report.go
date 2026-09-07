package organization

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Service) HandleMissionReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	idParam := strings.TrimSpace(r.PathValue("id"))
	if idParam == "" {
		idParam = strings.TrimSpace(r.URL.Query().Get("id"))
	}
	if idParam == "" {
		writeError(w, http.StatusBadRequest, "Parámetro de misión requerido")
		return
	}

	report, err := s.GetMissionReport(r.Context(), idParam)
	if err != nil {
		s.logger.Warn("failed to get mission report", "mission_id", idParam, "error", err)
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, report)
}

func (s *Service) GetMissionReport(ctx context.Context, missionID string) (MissionReport, error) {
	cleanID := strings.TrimSpace(missionID)
	cleanID = strings.TrimPrefix(cleanID, "MS-")
	taskID, err := strconv.ParseInt(cleanID, 10, 64)
	if err != nil {
		return MissionReport{}, fmt.Errorf("identificador de misión inválido: %s", missionID)
	}

	var rootTask struct {
		id            int64
		title         string
		instructions  string
		status        string
		correlationID string
		createdAt     time.Time
		terminalAt    *time.Time
	}

	err = s.pool.QueryRow(ctx, `
		SELECT id, title, coalesce(instructions, ''), status, coalesce(correlation_id, ''), created_at, terminal_at
		FROM tasks
		WHERE id = $1
	`, taskID).Scan(
		&rootTask.id,
		&rootTask.title,
		&rootTask.instructions,
		&rootTask.status,
		&rootTask.correlationID,
		&rootTask.createdAt,
		&rootTask.terminalAt,
	)
	if err != nil {
		return MissionReport{}, fmt.Errorf("misión #%d no encontrada: %w", taskID, err)
	}

	displayTitle := rootTask.title
	objective := rootTask.instructions
	if (rootTask.title == "Executive owner goal" || strings.TrimSpace(rootTask.title) == "") && strings.TrimSpace(rootTask.instructions) != "" {
		lines := strings.Split(strings.TrimSpace(rootTask.instructions), "\n")
		displayTitle = lines[0]
		if len(displayTitle) > 120 {
			displayTitle = displayTitle[:117] + "..."
		}
	}

	var bMax, bUsed int64
	_ = s.pool.QueryRow(ctx, `
		SELECT coalesce(max_usd_nanos / 1000, 5000000)::bigint, coalesce(used_usd_nanos / 1000, 0)::bigint
		FROM agent_budgets
		WHERE root_task_id = $1 AND task_id = $1
	`, taskID).Scan(&bMax, &bUsed)

	var totalTokens, inputTokens, outputTokens int64
	if rootTask.correlationID != "" {
		_ = s.pool.QueryRow(ctx, `
			SELECT 
				coalesce(sum(miu.total_tokens), 0)::bigint,
				coalesce(sum(miu.input_tokens), 0)::bigint,
				coalesce(sum(miu.output_tokens), 0)::bigint
			FROM tasks t
			JOIN model_invocations mi ON mi.task_id = t.id
			JOIN model_invocation_usage miu ON miu.invocation_id = mi.id
			WHERE t.correlation_id = $1
		`, rootTask.correlationID).Scan(&totalTokens, &inputTokens, &outputTokens)
	}

	completedAtStr := ""
	durationStr := ""
	if rootTask.terminalAt != nil {
		completedAtStr = rootTask.terminalAt.UTC().Format(time.RFC3339)
		d := rootTask.terminalAt.Sub(rootTask.createdAt).Round(time.Second)
		if d > 0 {
			mins := int(d.Minutes())
			secs := int(d.Seconds()) % 60
			if mins > 0 {
				durationStr = fmt.Sprintf("%dm %ds", mins, secs)
			} else {
				durationStr = fmt.Sprintf("%ds", secs)
			}
		}
	}

	report := MissionReport{
		MissionID:         fmt.Sprintf("MS-%d", taskID),
		RootTaskID:        taskID,
		Title:             displayTitle,
		Objective:         objective,
		Status:            rootTask.status,
		CreatedAt:         rootTask.createdAt.UTC().Format(time.RFC3339),
		CompletedAt:       completedAtStr,
		Duration:          durationStr,
		BudgetMicrousd:    bMax,
		SpentMicrousd:     bUsed,
		TotalTokens:       totalTokens,
		InputTokens:       inputTokens,
		OutputTokens:      outputTokens,
		TotalTasks:        0,
		CompletedTasks:    0,
		DepartmentReviews: []DepartmentReviewItem{},
		SpecialistAudits:  []SpecialistAuditItem{},
		KeyResolutions:    []string{},
	}

	if rootTask.correlationID == "" {
		return report, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT 
			t.id, t.title, t.task_class, coalesce(t.assigned_role_id, ''), t.status,
			coalesce(mir.json_output->>'schema_version', ''),
			coalesce(mir.json_output::text, ''),
			coalesce(mir.text_output, '')
		FROM tasks t
		LEFT JOIN model_invocations mi ON mi.task_id = t.id AND mi.status = 'succeeded'
		LEFT JOIN model_invocation_results mir ON mir.invocation_id = mi.id
		WHERE t.correlation_id = $1
		ORDER BY t.id ASC
	`, rootTask.correlationID)
	if err != nil {
		s.logger.Warn("failed to query tasks for mission report", "correlation_id", rootTask.correlationID, "error", err)
		return report, nil
	}
	defer rows.Close()

	for rows.Next() {
		var tID int64
		var tTitle, tClass, roleID, tStatus, schemaVer, jsonText, textOut string
		if err := rows.Scan(&tID, &tTitle, &tClass, &roleID, &tStatus, &schemaVer, &jsonText, &textOut); err != nil {
			s.logger.Warn("failed to scan task row for mission report", "error", err)
			continue
		}

		report.TotalTasks++
		if tStatus == "completed" {
			report.CompletedTasks++
		}

		dept := "empresa"
		roleName := roleID
		if parts := strings.Split(roleID, "/"); len(parts) == 2 {
			dept = parts[0]
			roleName = parts[1]
		}

		switch tClass {
		case "coordination.ceo_closure":
			if jsonText != "" {
				var raw struct {
					Status              string   `json:"status"`
					AnswerToOwner       string   `json:"answer_to_owner"`
					CompletedItems      []string `json:"completed_items"`
					BlockedItems        []string `json:"blocked_items"`
					UnresolvedDecisions []string `json:"unresolved_decisions"`
				}
				if err := json.Unmarshal([]byte(jsonText), &raw); err == nil {
					report.CeoClosure = &CeoClosureReport{
						Status:              raw.Status,
						AnswerToOwner:       raw.AnswerToOwner,
						CompletedItems:      raw.CompletedItems,
						BlockedItems:        raw.BlockedItems,
						UnresolvedDecisions: raw.UnresolvedDecisions,
					}
				}
			}
		case "coordination.ceo_plan", "coordination.executive_plan":
			if jsonText != "" {
				var raw struct {
					Objective         string   `json:"objective"`
					SuccessCriteria   []string `json:"success_criteria"`
					GlobalConstraints []string `json:"global_constraints"`
				}
				if err := json.Unmarshal([]byte(jsonText), &raw); err == nil {
					report.ExecutivePlan = &ExecutivePlanReport{
						Objective:         raw.Objective,
						SuccessCriteria:   raw.SuccessCriteria,
						GlobalConstraints: raw.GlobalConstraints,
					}
				}
			}
		case "coordination.department_review":
			if jsonText != "" {
				var review struct {
					Verdict  string   `json:"verdict"`
					Findings []string `json:"findings"`
				}
				if err := json.Unmarshal([]byte(jsonText), &review); err == nil {
					report.DepartmentReviews = append(report.DepartmentReviews, DepartmentReviewItem{
						TaskID:     tID,
						Department: dept,
						RoleID:     roleID,
						Verdict:    review.Verdict,
						Findings:   review.Findings,
					})
				}
			}
		case "owner.goal", "coordination.department_plan":
			// Handled at top level or in reviews

		default:
			summary := ""
			if jsonText != "" {
				var obj map[string]any
				if err := json.Unmarshal([]byte(jsonText), &obj); err == nil {
					if s, ok := obj["summary"].(string); ok {
						summary = s
					}
				}
			}
			if summary == "" && textOut != "" {
				summary = textOut
			}
			report.SpecialistAudits = append(report.SpecialistAudits, SpecialistAuditItem{
				TaskID:     tID,
				Department: dept,
				RoleID:     roleID,
				RoleName:   roleName,
				Title:      tTitle,
				TaskClass:  tClass,
				Status:     tStatus,
				Summary:    summary,
			})
		}
	}

	if report.CeoClosure != nil && len(report.CeoClosure.CompletedItems) > 0 {
		for _, item := range report.CeoClosure.CompletedItems {
			report.KeyResolutions = append(report.KeyResolutions, item)
		}
	}
	if len(report.KeyResolutions) == 0 {
		for _, dr := range report.DepartmentReviews {
			for _, f := range dr.Findings {
				report.KeyResolutions = append(report.KeyResolutions, fmt.Sprintf("[%s] %s", dr.Department, f))
			}
		}
	}

	return report, nil
}
