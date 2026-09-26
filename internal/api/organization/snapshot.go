package organization

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The snapshot is a projection of canonical state, and it says so when it cannot be one.
//
// Audit 2026-09-26, finding 3: missions were any task requested by empresa/human (CEO plans, Finance
// reviews and chat turns among them); every status other than completed and awaiting_verification was
// shown as active, blocked roots included; the counters were computed over the last 15 rows; and three
// queries carried unquoted literals (`status IN (leased, ...)`, `LIKE executive:%`, `entry_type =
// episodic`) whose errors were discarded, so the panel showed every role idle, a fixed 15% progress and
// 0/0 task counts. Figures that had no source were invented: a $5 budget for a root without one, a $25
// organization budget when none was recorded, and an "estimate" of a quarter of the unspent budget.
//
// Missions are now the owner.goal roots only, each with its own status and reason and its children
// counted by its own correlation; counters are computed over every root, not over the page; what the
// panel cannot read either fails the request (the sections a mission view depends on) or is named in
// Partial; and a figure with no source is zero, never a default.

// snapshotMissionLimit is how many roots the mission list shows. The counters never depend on it.
const snapshotMissionLimit = 15

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

// missionStatus maps a root task's status onto the panel's vocabulary without folding any state into
// another: a blocked, failed or cancelled root is never shown as active.
func missionStatus(taskStatus string) string {
	switch taskStatus {
	case "completed":
		return "completed"
	case "awaiting_verification":
		return "review"
	case "blocked":
		return "blocked"
	case "failed", "dead_letter", "rejected":
		return "failed"
	case "cancelled", "no_action":
		return "cancelled"
	default: // pending, ready, leased, running, retry_wait
		return "active"
	}
}

// missionProgress is the share of a root's children that completed. A completed root is 100; one
// with no children yet is 0, not a placeholder; an unfinished root never shows 100.
func missionProgress(status string, completed, total int64) float64 {
	if status == "completed" {
		return 100
	}
	if total <= 0 {
		return 0
	}
	progress := float64(completed*100) / float64(total)
	if progress > 95 {
		progress = 95
	}
	return progress
}

func missionTitle(title, instructions string) string {
	if (title == "Executive owner goal" || strings.TrimSpace(title) == "") && strings.TrimSpace(instructions) != "" {
		first := strings.Split(strings.TrimSpace(instructions), "\n")[0]
		if len(first) > 90 {
			first = strings.ToValidUTF8(first[:87], "") + "..."
		}
		return first
	}
	return title
}

func nanosToMicros(nanos *int64) int64 {
	if nanos == nil {
		return 0
	}
	return *nanos / 1000
}

func (s *Service) GetSnapshot(ctx context.Context) (Snapshot, error) {
	orgID := s.cfg.Tasks.OrganizationID
	var partial []string
	degrade := func(section string, err error) {
		s.logger.Warn("organization snapshot section unavailable", "section", section, "error", err)
		partial = append(partial, section)
	}

	orgName := "Organización Explorarte"
	var name string
	if err := s.pool.QueryRow(ctx, `SELECT display_name FROM organizations WHERE id = $1`, orgID).Scan(&name); err != nil {
		degrade("organization", err)
	} else if name != "" {
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

	// The latest task each role holds that is not settled, with the root it belongs to.
	type roleTask struct {
		taskID int64
		rootID int64
		status string
		title  string
	}
	activeTasks := make(map[string]roleTask)
	taskRows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (t.assigned_role_id) t.assigned_role_id, t.status, t.title, t.id, coalesce(root.id, 0)
		FROM tasks t
		LEFT JOIN LATERAL (
			SELECT r.id FROM tasks r
			WHERE r.organization_id = t.organization_id AND r.correlation_id = t.correlation_id AND r.task_class = 'owner.goal'
			ORDER BY r.id LIMIT 1
		) root ON true
		WHERE t.organization_id = $1
		  AND t.status IN ('leased', 'running', 'awaiting_verification', 'blocked')
		ORDER BY t.assigned_role_id, t.id DESC`, orgID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list tasks held by roles: %w", err)
	}
	for taskRows.Next() {
		var roleID string
		var task roleTask
		if err := taskRows.Scan(&roleID, &task.status, &task.title, &task.taskID, &task.rootID); err != nil {
			taskRows.Close()
			return Snapshot{}, fmt.Errorf("scan task held by a role: %w", err)
		}
		activeTasks[roleID] = task
	}
	taskRows.Close()
	if err := taskRows.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("list tasks held by roles: %w", err)
	}

	type unit struct{ id, name string }
	var units []unit
	unitRows, err := s.pool.Query(ctx, `SELECT id, display_name FROM organizational_units WHERE organization_id = $1 AND retired_at IS NULL ORDER BY id`, orgID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list organizational units: %w", err)
	}
	for unitRows.Next() {
		var u unit
		if err := unitRows.Scan(&u.id, &u.name); err != nil {
			unitRows.Close()
			return Snapshot{}, fmt.Errorf("scan organizational unit: %w", err)
		}
		units = append(units, u)
	}
	unitRows.Close()
	if err := unitRows.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("list organizational units: %w", err)
	}

	roleRows, err := s.pool.Query(ctx, `SELECT id, unit_id, display_name FROM organization_roles WHERE organization_id = $1 AND retired_at IS NULL AND enabled = true ORDER BY unit_id, id`, orgID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list organization roles: %w", err)
	}
	rolesByUnit := make(map[string][]Role)
	for roleRows.Next() {
		var id, unitID, displayName string
		if err := roleRows.Scan(&id, &unitID, &displayName); err != nil {
			roleRows.Close()
			return Snapshot{}, fmt.Errorf("scan organization role: %w", err)
		}
		role := Role{ID: id, Name: displayName, Activity: "Disponible / En espera de asignación", Status: "idle"}
		if task, ok := activeTasks[id]; ok {
			if task.rootID != 0 {
				role.MissionID = fmt.Sprintf("MS-%d", task.rootID)
			}
			switch task.status {
			case "leased", "running":
				role.Status, role.Activity, role.Progress = "working", task.title, 50
			case "awaiting_verification":
				role.Status, role.Activity, role.Progress = "reviewing", "Revisando: "+task.title, 80
			case "blocked":
				role.Status, role.Activity, role.Progress = "blocked", "Bloqueado: "+task.title, 0
			}
		}
		rolesByUnit[unitID] = append(rolesByUnit[unitID], role)
	}
	roleRows.Close()
	if err := roleRows.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("list organization roles: %w", err)
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
		departments = append(departments, Department{ID: u.id, Name: u.name, Subtitle: meta.subtitle, Color: meta.color, Roles: roles})
	}

	missions, err := s.listMissions(ctx, orgID, snapshotMissionLimit)
	if err != nil {
		return Snapshot{}, err
	}

	var counts MissionsMetric
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status NOT IN ('completed', 'blocked', 'failed', 'dead_letter', 'rejected', 'cancelled', 'no_action')),
		       count(*) FILTER (WHERE status = 'completed'),
		       count(*) FILTER (WHERE status = 'blocked'),
		       count(*) FILTER (WHERE status IN ('failed', 'dead_letter', 'rejected')),
		       count(*) FILTER (WHERE status IN ('cancelled', 'no_action')),
		       count(*)
		FROM tasks
		WHERE organization_id = $1 AND task_class = 'owner.goal'`, orgID).Scan(
		&counts.Active, &counts.Completed, &counts.Blocked, &counts.Failed, &counts.Cancelled, &counts.Total); err != nil {
		return Snapshot{}, fmt.Errorf("count missions: %w", err)
	}

	var learningMetric LearningMetric
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM memoryos_episodes), (SELECT count(*) FROM memoryos_clusters)`).Scan(
		&learningMetric.Episodes, &learningMetric.Consolidated); err != nil {
		degrade("learning", err)
	}
	var skills SkillsMetric
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM skill_registry_skills), (SELECT count(*) FROM skill_registry_assignments)`).Scan(
		&skills.Created, &skills.Learned); err != nil {
		degrade("skills", err)
	}
	// organizational_memory_entries records no memory type, so there is no episodic, semantic or
	// corrective breakdown to report: only the total, and the breakdown stays zero.
	var memories MemoriesMetric
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM organizational_memory_entries WHERE organization_id = $1`, orgID).Scan(&memories.Total); err != nil {
		degrade("memories", err)
	}

	// Only what the budgets record: no default when there are none, and no forecast.
	var cost CostMetric
	if err := s.pool.QueryRow(ctx, `
		SELECT coalesce(sum(used_usd_nanos) / 1000, 0)::bigint, coalesce(sum(max_usd_nanos) / 1000, 0)::bigint
		FROM agent_budgets WHERE organization_id = $1`, orgID).Scan(&cost.ActualMicrousd, &cost.BudgetMicrousd); err != nil {
		degrade("cost", err)
	}
	cost.EstimatedMicrousd = cost.ActualMicrousd

	learning := []LearningItem{}
	if epRows, err := s.pool.Query(ctx, `SELECT id, role_id, execution_purpose, status, created_at FROM memoryos_episodes ORDER BY created_at DESC LIMIT 4`); err != nil {
		degrade("learning_items", err)
	} else {
		for epRows.Next() {
			var id, roleID, purpose, status string
			var createdAt time.Time
			if err := epRows.Scan(&id, &roleID, &purpose, &status, &createdAt); err != nil {
				degrade("learning_items", err)
				break
			}
			unitID := "empresa"
			if parts := strings.Split(roleID, "/"); len(parts) > 0 && parts[0] != "" {
				unitID = parts[0]
			}
			learning = append(learning, LearningItem{
				ID: "ep-" + id[:min(12, len(id))], Title: fmt.Sprintf("Episodio %s (%s)", purpose, roleID),
				Department: unitID, Status: status, Time: formatRelativeTime(createdAt), Type: "episode",
			})
		}
		epRows.Close()
	}
	if skRows, err := s.pool.Query(ctx, `SELECT skill_id, created_by_role_id, created_at FROM skill_registry_skills ORDER BY created_at DESC LIMIT 3`); err != nil {
		degrade("skill_items", err)
	} else {
		for skRows.Next() {
			var skillID, creator string
			var createdAt time.Time
			if err := skRows.Scan(&skillID, &creator, &createdAt); err != nil {
				degrade("skill_items", err)
				break
			}
			unitID := "recursos_agenticos"
			if parts := strings.Split(creator, "/"); len(parts) > 0 && parts[0] != "" {
				unitID = parts[0]
			}
			learning = append(learning, LearningItem{
				ID: "sk-" + skillID, Title: fmt.Sprintf("Skill: %s", skillID), Department: unitID,
				Status: "Registrada", Time: formatRelativeTime(createdAt), Type: "skill",
			})
		}
		skRows.Close()
	}

	activity := []ActivityItem{}
	if actRows, err := s.pool.Query(ctx, `SELECT id, assigned_role_id, status, title, created_at FROM tasks WHERE organization_id = $1 ORDER BY id DESC LIMIT 8`, orgID); err != nil {
		degrade("activity", err)
	} else {
		for actRows.Next() {
			var id int64
			var roleID, status, title string
			var createdAt time.Time
			if err := actRows.Scan(&id, &roleID, &status, &title, &createdAt); err != nil {
				degrade("activity", err)
				break
			}
			activity = append(activity, ActivityItem{
				ID: fmt.Sprintf("act-%d", id), Role: roleID, Text: fmt.Sprintf("[%s] %s", status, title), Time: formatRelativeTime(createdAt),
			})
		}
		actRows.Close()
	}

	spend := []SpendItem{}
	if spendRows, err := s.pool.Query(ctx, `
		SELECT u.display_name, coalesce(sum(b.used_usd_nanos) / 1000, 0)::bigint
		FROM organizational_units u
		LEFT JOIN organization_roles r ON r.unit_id = u.id
		LEFT JOIN agent_budgets b ON b.role_id = r.id
		WHERE u.organization_id = $1
		GROUP BY u.id, u.display_name
		ORDER BY u.id`, orgID); err != nil {
		degrade("spend", err)
	} else {
		for spendRows.Next() {
			var item SpendItem
			if err := spendRows.Scan(&item.Label, &item.Microusd); err != nil {
				degrade("spend", err)
				break
			}
			spend = append(spend, item)
		}
		spendRows.Close()
	}

	return Snapshot{
		Organization: OrganizationInfo{Name: orgName},
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
		Metrics: Metrics{
			Objectives: ObjectivesMetric{Completed: counts.Completed, Total: counts.Total},
			Missions:   counts,
			Learning:   learningMetric,
			Skills:     skills,
			Memories:   memories,
			Cost:       cost,
		},
		Departments: departments,
		Missions:    missions,
		Learning:    learning,
		Activity:    activity,
		Spend:       spend,
		// Read-only: see owner_channel.go.
		Capabilities: Capabilities{Chat: false, CreateMission: false},
		Partial:      partial,
	}, nil
}

// listMissions returns the newest owner.goal roots, each with its own status and reason, its budget
// as recorded, and its children counted by its own correlation.
func (s *Service) listMissions(ctx context.Context, orgID string, limit int) ([]Mission, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.title, t.instructions, t.status,
		       coalesce(t.status_reason_code, ''), coalesce(t.status_reason, ''),
		       b.max_usd_nanos, b.used_usd_nanos,
		       coalesce(c.total, 0), coalesce(c.completed, 0), coalesce(c.blocked, 0), coalesce(c.failed, 0)
		FROM tasks t
		LEFT JOIN agent_budgets b ON b.root_task_id = t.id AND b.task_id = t.id
		LEFT JOIN LATERAL (
			SELECT count(*) AS total,
			       count(*) FILTER (WHERE child.status = 'completed') AS completed,
			       count(*) FILTER (WHERE child.status = 'blocked') AS blocked,
			       count(*) FILTER (WHERE child.status IN ('failed', 'dead_letter', 'rejected')) AS failed
			FROM tasks child
			WHERE child.organization_id = t.organization_id AND child.correlation_id = t.correlation_id AND child.id <> t.id
		) c ON true
		WHERE t.organization_id = $1 AND t.task_class = 'owner.goal'
		ORDER BY t.id DESC
		LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("list missions: %w", err)
	}
	defer rows.Close()
	missions := []Mission{}
	for rows.Next() {
		var id, total, completed, blocked, failed int64
		var title, instructions, status, reasonCode, reason string
		var maxNanos, usedNanos *int64
		if err := rows.Scan(&id, &title, &instructions, &status, &reasonCode, &reason, &maxNanos, &usedNanos,
			&total, &completed, &blocked, &failed); err != nil {
			return nil, fmt.Errorf("scan mission: %w", err)
		}
		missions = append(missions, Mission{
			ID:               fmt.Sprintf("MS-%d", id),
			RootTaskID:       id,
			Title:            missionTitle(title, instructions),
			Status:           missionStatus(status),
			TaskStatus:       status,
			StatusReasonCode: reasonCode,
			StatusReason:     reason,
			Department:       "empresa",
			BudgetMicrousd:   nanosToMicros(maxNanos),
			SpentMicrousd:    nanosToMicros(usedNanos),
			Progress:         missionProgress(status, completed, total),
			CompletedTasks:   completed,
			BlockedTasks:     blocked,
			FailedTasks:      failed,
			TotalTasks:       total,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list missions: %w", err)
	}
	return missions, nil
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
