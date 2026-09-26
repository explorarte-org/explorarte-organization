package organization

type Snapshot struct {
	Organization OrganizationInfo `json:"organization"`
	UpdatedAt    string           `json:"updatedAt"`
	Metrics      Metrics          `json:"metrics"`
	Departments  []Department     `json:"departments"`
	Missions     []Mission        `json:"missions"`
	Learning     []LearningItem   `json:"learning"`
	Activity     []ActivityItem   `json:"activity"`
	Spend        []SpendItem      `json:"spend"`
	Capabilities Capabilities     `json:"capabilities"`
	// Partial names the sections that could not be read; their figures are zero, not estimates.
	Partial []string `json:"partial,omitempty"`
}

type OrganizationInfo struct {
	Name string `json:"name"`
}

type Metrics struct {
	Objectives ObjectivesMetric `json:"objectives"`
	Missions   MissionsMetric   `json:"missions"`
	Learning   LearningMetric   `json:"learning"`
	Skills     SkillsMetric     `json:"skills"`
	Memories   MemoriesMetric   `json:"memories"`
	Cost       CostMetric       `json:"cost"`
}

type ObjectivesMetric struct {
	Completed int64 `json:"completed"`
	Total     int64 `json:"total"`
}

// MissionsMetric counts every owner.goal root by status, independently of how many the list shows.
type MissionsMetric struct {
	Active    int64 `json:"active"`
	Completed int64 `json:"completed"`
	Blocked   int64 `json:"blocked"`
	Failed    int64 `json:"failed"`
	Cancelled int64 `json:"cancelled"`
	Total     int64 `json:"total"`
}

type LearningMetric struct {
	Episodes     int64 `json:"episodes"`
	Consolidated int64 `json:"consolidated"`
}

type SkillsMetric struct {
	Created int64 `json:"created"`
	Learned int64 `json:"learned"`
}

// MemoriesMetric: organizational_memory_entries records no memory type, so only Total is measured
// and the breakdown is zero.
type MemoriesMetric struct {
	Total      int64 `json:"total"`
	Episodic   int64 `json:"episodic"`
	Semantic   int64 `json:"semantic"`
	Corrective int64 `json:"corrective"`
}

type CostMetric struct {
	ActualMicrousd    int64 `json:"actualMicrousd"`
	BudgetMicrousd    int64 `json:"budgetMicrousd"`
	EstimatedMicrousd int64 `json:"estimatedMicrousd"`
}

type Department struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Subtitle string `json:"subtitle"`
	Color    string `json:"color"`
	Roles    []Role `json:"roles"`
}

type Role struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Activity  string  `json:"activity"`
	Status    string  `json:"status"`   // "working", "reviewing", "idle", "blocked"
	Progress  float64 `json:"progress"` // 0 to 100
	MissionID string  `json:"missionId,omitempty"`
}

type Mission struct {
	ID         string `json:"id"`
	RootTaskID int64  `json:"rootTaskId"`
	Title      string `json:"title"`
	// Status is "active", "review", "blocked", "failed", "cancelled" or "completed"; TaskStatus is
	// the root task's own status, and the reason is the one the root records.
	Status           string  `json:"status"`
	TaskStatus       string  `json:"taskStatus"`
	StatusReasonCode string  `json:"statusReasonCode,omitempty"`
	StatusReason     string  `json:"statusReason,omitempty"`
	Department       string  `json:"department"`
	BudgetMicrousd   int64   `json:"budgetMicrousd"`
	SpentMicrousd    int64   `json:"spentMicrousd"`
	Progress         float64 `json:"progress"`
	CompletedTasks   int64   `json:"completedTasks"`
	BlockedTasks     int64   `json:"blockedTasks"`
	FailedTasks      int64   `json:"failedTasks"`
	TotalTasks       int64   `json:"totalTasks"`
}

type LearningItem struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Department string `json:"department"`
	Status     string `json:"status"`
	Time       string `json:"time"`
	Type       string `json:"type"` // "skill", "memory", "episode"
}

type ActivityItem struct {
	ID   string `json:"id"`
	Role string `json:"role"`
	Text string `json:"text"`
	Time string `json:"time"`
}

type SpendItem struct {
	Label    string `json:"label"`
	Microusd int64  `json:"microusd"`
}

type Capabilities struct {
	Chat          bool `json:"chat"`
	CreateMission bool `json:"createMission"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type MissionReport struct {
	MissionID         string                 `json:"missionId"`
	RootTaskID        int64                  `json:"rootTaskId"`
	Title             string                 `json:"title"`
	Objective         string                 `json:"objective"`
	Status            string                 `json:"status"`
	CreatedAt         string                 `json:"createdAt"`
	CompletedAt       string                 `json:"completedAt,omitempty"`
	Duration          string                 `json:"duration,omitempty"`
	BudgetMicrousd    int64                  `json:"budgetMicrousd"`
	SpentMicrousd     int64                  `json:"spentMicrousd"`
	TotalTokens       int64                  `json:"totalTokens"`
	InputTokens       int64                  `json:"inputTokens"`
	OutputTokens      int64                  `json:"outputTokens"`
	TotalTasks        int64                  `json:"totalTasks"`
	CompletedTasks    int64                  `json:"completedTasks"`
	CeoClosure        *CeoClosureReport      `json:"ceoClosure,omitempty"`
	ExecutivePlan     *ExecutivePlanReport   `json:"executivePlan,omitempty"`
	DepartmentReviews []DepartmentReviewItem `json:"departmentReviews"`
	SpecialistAudits  []SpecialistAuditItem  `json:"specialistAudits"`
	KeyResolutions    []string               `json:"keyResolutions"`
}

type CeoClosureReport struct {
	Status              string   `json:"status"`
	AnswerToOwner       string   `json:"answerToOwner"`
	CompletedItems      []string `json:"completedItems"`
	BlockedItems        []string `json:"blockedItems"`
	UnresolvedDecisions []string `json:"unresolvedDecisions"`
}

type ExecutivePlanReport struct {
	Objective         string   `json:"objective"`
	SuccessCriteria   []string `json:"successCriteria"`
	GlobalConstraints []string `json:"globalConstraints"`
}

type DepartmentReviewItem struct {
	TaskID     int64    `json:"taskId"`
	Department string   `json:"department"`
	RoleID     string   `json:"roleId"`
	Verdict    string   `json:"verdict"`
	Findings   []string `json:"findings"`
}

type SpecialistAuditItem struct {
	TaskID     int64  `json:"taskId"`
	Department string `json:"department"`
	RoleID     string `json:"roleId"`
	RoleName   string `json:"roleName"`
	Title      string `json:"title"`
	TaskClass  string `json:"taskClass"`
	Status     string `json:"status"`
	Summary    string `json:"summary"`
}
