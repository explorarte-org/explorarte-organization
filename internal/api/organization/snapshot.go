package organization

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *Service) HandleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	snapshot, err := s.GetSnapshot(r.Context())
	if err != nil {
		s.logger.Error("failed to get organization snapshot", "error", err)
		writeError(w, http.StatusInternalServerError, "Error al obtener el estado de la organización: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Service) GetSnapshot(ctx context.Context) (Snapshot, error) {
	orgName := "Organización Explorarte"
	var name string
	if err := s.pool.QueryRow(ctx, `SELECT display_name FROM organizations LIMIT 1`).Scan(&name); err == nil && name != "" {
		orgName = name
	}

	deptMeta := map[string]struct {
		subtitle string
		color    string
	}{
		"empresa":            {subtitle: "Dirección y orquestación ejecutiva", color: "#b89078"},
		"ingenieria_ia":      {subtitle: "Arquitectura, pipelines y ejecución", color: "#7b9eb8"},
		"negocio":            {subtitle: "Estrategia, finanzas y crecimiento", color: "#b8a878"},
		"servicios":          {subtitle: "Diseño de servicios y experiencia", color: "#9bb880"},
		"investigacion":      {subtitle: "Auditoría adversarial y análisis", color: "#9a88b8"},
		"recursos_agenticos": {subtitle: "Diseño de agentes y catálogo de skills", color: "#88b8a8"},
	}

	activeTasks := make(map[string]struct {
		taskID int64
		status string
		title  string
	})
	taskRows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (assigned_role_id) assigned_role_id, status, title, id
		FROM tasks
		WHERE status IN (leased, running, awaiting_verification, blocked)
		ORDER BY assigned_role_id, id DESC
	`)
	if err == nil {
		defer taskRows.Close()
		for taskRows.Next() {
			var roleID, status, title string
			var id int64
			if err := taskRows.Scan(&roleID, &status, &title, &id); err == nil {
				activeTasks[roleID] = struct {
					taskID int64
					status string
					title  string
				}{taskID: id, status: status, title: title}
			}
		}
	}

	unitRows, err := s.pool.Query(ctx, `SELECT id, display_name FROM organizational_units WHERE retired_at IS NULL ORDER BY id`)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list organizational units: %w", err)
	}
	defer unitRows.Close()

	var units []struct {
		id   string
		name string
	}
	for unitRows.Next() {
		var id, dName string
		if err := unitRows.Scan(&id, &dName); err == nil {
			units = append(units, struct {
				id   string
				name string
			}{id: id, name: dName})
		}
	}

	roleRows, err := s.pool.Query(ctx, `SELECT id, unit_id, display_name FROM organization_roles WHERE retired_at IS NULL AND enabled = true ORDER BY unit_id, id`)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list organization roles: %w", err)
	}
	defer roleRows.Close()

	rolesByUnit := make(map[string][]Role)
	for roleRows.Next() {
		var id, unitID, dName string
		if err := roleRows.Scan(&id, &unitID, &dName); err == nil {
			roleStatus := "idle"
			activity := "Disponible / En espera de asignación"
			progress := 0.0
			var missionID string

			if at, ok := activeTasks[id]; ok {
				missionID = fmt.Sprintf("MS-%d", at.taskID)
				switch at.status {
				case "leased", "running":
					roleStatus = "working"
					activity = at.title
					progress = 50.0
				case "awaiting_verification":
					roleStatus = "reviewing"
					activity = "Revisando: " + at.title
					progress = 80.0
				case "blocked":
					roleStatus = "blocked"
					activity = "Bloqueado: " + at.title
					progress = 20.0
				}
			}

			rolesByUnit[unitID] = append(rolesByUnit[unitID], Role{
				ID:        id,
				Name:      dName,
				Activity:  activity,
				Status:    roleStatus,
				Progress:  progress,
				MissionID: missionID,
			})
		}
	}

	departments := make([]Department, 0, len(units))
	for _, u := range units {
		meta := deptMeta[u.id]
		if meta.color == "" {
			meta.color = "#9bb880"
		}
		if meta.subtitle == "" {
			meta.subtitle = "Departamento operativo"
		}
		roles := rolesByUnit[u.id]
		if roles == nil {
			roles = []Role{}
		}
		departments = append(departments, Department{
			ID:       u.id,
			Name:     u.name,
			Subtitle: meta.subtitle,
			Color:    meta.color,
			Roles:    roles,
		})
	}

	missionRows, err := s.pool.Query(ctx, `
		SELECT t.id, t.title, t.instructions, t.status,
		       coalesce(b.max_usd_nanos / 1000, 5000000) as b_max,
		       coalesce(b.used_usd_nanos / 1000, 0) as b_used
		FROM tasks t
		LEFT JOIN agent_budgets b ON b.root_task_id = t.id AND b.task_id = t.id
		WHERE t.task_class = owner.goal OR t.requested_by_role_id = empresa/human
		ORDER BY t.id DESC
		LIMIT 15
	`)
	var missions []Mission
	var activeMissionsCount, completedMissionsCount int64
	if err == nil {
		defer missionRows.Close()
		for missionRows.Next() {
			var id int64
			var title, instructions, status string
			var bMax, bUsed float64
			if err := missionRows.Scan(&id, &title, &instructions, &status, &bMax, &bUsed); err == nil {
				mStatus := "active"
				switch status {
				case "completed":
					mStatus = "completed"
					completedMissionsCount++
				case "awaiting_verification":
					mStatus = "review"
					activeMissionsCount++
				default:
					mStatus = "active"
					activeMissionsCount++
				}

				displayTitle := title
				if (title == "Executive owner goal" || strings.TrimSpace(title) == "") && strings.TrimSpace(instructions) != "" {
					lines := strings.Split(strings.TrimSpace(instructions), "\n")
					displayTitle = lines[0]
					if len(displayTitle) > 90 {
						displayTitle = displayTitle[:87] + "..."
					}
				}

				var compTasks, totTasks int64
				_ = s.pool.QueryRow(ctx, `
					SELECT count(*) FILTER (WHERE status = completed), count(*)
					FROM tasks
					WHERE correlation_id LIKE executive:% AND causation_id LIKE owner:%
				`).Scan(&compTasks, &totTasks)

				prog := 15.0
				if mStatus == "completed" {
					prog = 100.0
				} else if totTasks > 0 {
					prog = float64(compTasks*100) / float64(totTasks)
					if prog > 95.0 {
						prog = 95.0
					}
				}

				missions = append(missions, Mission{
					ID:             fmt.Sprintf("MS-%d", id),
					Title:          displayTitle,
					Status:         mStatus,
					Department:     "empresa",
					BudgetMicrousd: int64(bMax),
					SpentMicrousd:  int64(bUsed),
					Progress:       prog,
					CompletedTasks: compTasks,
					TotalTasks:     totTasks,
				})
			}
		}
	}
	if missions == nil {
		missions = []Mission{}
	}

	var objCompleted, objTotal int64
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = completed), count(*) FROM tasks`).Scan(&objCompleted, &objTotal)

	var episodesCount, clustersCount int64
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM memoryos_episodes`).Scan(&episodesCount)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM memoryos_clusters`).Scan(&clustersCount)

	var skillsCreated, skillsLearned int64
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM skill_registry_skills`).Scan(&skillsCreated)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM skill_registry_assignments`).Scan(&skillsLearned)

	var memEpisodic, memSemantic, memCorrective int64
	_ = s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE entry_type = episodic),
		       count(*) FILTER (WHERE entry_type = semantic),
		       count(*) FILTER (WHERE entry_type = corrective)
		FROM organizational_memory_entries
	`).Scan(&memEpisodic, &memSemantic, &memCorrective)

	var costActual, costBudget float64
	_ = s.pool.QueryRow(ctx, `
		SELECT coalesce(sum(used_usd_nanos) / 1000, 0),
		       coalesce(sum(max_usd_nanos) / 1000, 0)
		FROM agent_budgets
	`).Scan(&costActual, &costBudget)
	if costBudget == 0 {
		costBudget = 25000000
	}
	costEst := costActual
	if costActual < costBudget {
		costEst = costActual + (costBudget-costActual)/4
	}

	var learning []LearningItem
	epRows, err := s.pool.Query(ctx, `
		SELECT id, role_id, execution_purpose, status, created_at
		FROM memoryos_episodes
		ORDER BY created_at DESC
		LIMIT 4
	`)
	if err == nil {
		defer epRows.Close()
		for epRows.Next() {
			var id, roleID, purpose, status string
			var createdAt time.Time
			if err := epRows.Scan(&id, &roleID, &purpose, &status, &createdAt); err == nil {
				unitID := "empresa"
				if parts := strings.Split(roleID, "/"); len(parts) > 0 {
					unitID = parts[0]
				}
				learning = append(learning, LearningItem{
					ID:         "ep-" + id[:min(12, len(id))],
					Title:      fmt.Sprintf("Episodio %s (%s)", purpose, roleID),
					Department: unitID,
					Status:     status,
					Time:       formatRelativeTime(createdAt),
					Type:       "episode",
				})
			}
		}
	}

	skRows, err := s.pool.Query(ctx, `
		SELECT skill_id, created_by_role_id, created_at
		FROM skill_registry_skills
		ORDER BY created_at DESC
		LIMIT 3
	`)
	if err == nil {
		defer skRows.Close()
		for skRows.Next() {
			var skillID, creator string
			var createdAt time.Time
			if err := skRows.Scan(&skillID, &creator, &createdAt); err == nil {
				unitID := "recursos_agenticos"
				if parts := strings.Split(creator, "/"); len(parts) > 0 {
					unitID = parts[0]
				}
				learning = append(learning, LearningItem{
					ID:         "sk-" + skillID,
					Title:      fmt.Sprintf("Skill: %s", skillID),
					Department: unitID,
					Status:     "Registrada",
					Time:       formatRelativeTime(createdAt),
					Type:       "skill",
				})
			}
		}
	}
	if learning == nil {
		learning = []LearningItem{}
	}

	var activity []ActivityItem
	actRows, err := s.pool.Query(ctx, `
		SELECT id, assigned_role_id, status, title, created_at
		FROM tasks
		ORDER BY id DESC
		LIMIT 8
	`)
	if err == nil {
		defer actRows.Close()
		for actRows.Next() {
			var id int64
			var roleID, status, title string
			var createdAt time.Time
			if err := actRows.Scan(&id, &roleID, &status, &title, &createdAt); err == nil {
				activity = append(activity, ActivityItem{
					ID:   fmt.Sprintf("act-%d", id),
					Role: roleID,
					Text: fmt.Sprintf("[%s] %s", status, title),
					Time: formatRelativeTime(createdAt),
				})
			}
		}
	}
	if activity == nil {
		activity = []ActivityItem{}
	}

	var spend []SpendItem
	spendRows, err := s.pool.Query(ctx, `
		SELECT u.display_name, coalesce(sum(b.used_usd_nanos)/1000, 0) as used
		FROM organizational_units u
		LEFT JOIN organization_roles r ON r.unit_id = u.id
		LEFT JOIN agent_budgets b ON b.role_id = r.id
		GROUP BY u.id, u.display_name
		ORDER BY u.id
	`)
	if err == nil {
		defer spendRows.Close()
		for spendRows.Next() {
			var label string
			var used float64
			if err := spendRows.Scan(&label, &used); err == nil {
				spend = append(spend, SpendItem{
					Label:    label,
					Microusd: int64(used),
				})
			}
		}
	}
	if len(spend) == 0 {
		for _, u := range units {
			spend = append(spend, SpendItem{Label: u.name, Microusd: 0})
		}
	}

	return Snapshot{
		Organization: OrganizationInfo{Name: orgName},
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
		Metrics: Metrics{
			Objectives: ObjectivesMetric{Completed: objCompleted, Total: objTotal},
			Missions:   MissionsMetric{Active: activeMissionsCount, Completed: completedMissionsCount},
			Learning:   LearningMetric{Episodes: episodesCount, Consolidated: clustersCount},
			Skills:     SkillsMetric{Created: skillsCreated, Learned: skillsLearned},
			Memories:   MemoriesMetric{Episodic: memEpisodic, Semantic: memSemantic, Corrective: memCorrective},
			Cost: CostMetric{
				ActualMicrousd:    int64(costActual),
				BudgetMicrousd:    int64(costBudget),
				EstimatedMicrousd: int64(costEst),
			},
		},
		Departments:  departments,
		Missions:     missions,
		Learning:     learning,
		Activity:     activity,
		Spend:        spend,
		Capabilities: Capabilities{Chat: true, CreateMission: true},
	}, nil
}

func formatRelativeTime(t time.Time) string {
	diff := time.Since(t)
	if diff < time.Minute {
		return "Hace un momento"
	}
	if diff < time.Hour {
		mins := int(diff.Minutes())
		if mins == 1 {
			return "Hace 1 minuto"
		}
		return fmt.Sprintf("Hace %d minutos", mins)
	}
	if diff < 24*time.Hour {
		hours := int(diff.Hours())
		if hours == 1 {
			return "Hace 1 hora"
		}
		return fmt.Sprintf("Hace %d horas", hours)
	}
	days := int(diff.Hours() / 24)
	if days == 1 {
		return "Hace 1 día"
	}
	return fmt.Sprintf("Hace %d días", days)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
