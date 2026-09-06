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

type MissionsMetric struct {
	Active    int64 `json:"active"`
	Completed int64 `json:"completed"`
}

type LearningMetric struct {
	Episodes     int64 `json:"episodes"`
	Consolidated int64 `json:"consolidated"`
}

type SkillsMetric struct {
	Created int64 `json:"created"`
	Learned int64 `json:"learned"`
}

type MemoriesMetric struct {
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
	Status    string  `json:"status"` // "working", "reviewing", "idle", "blocked"
	Progress  float64 `json:"progress"` // 0 to 100
	MissionID string  `json:"missionId,omitempty"`
}

type Mission struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	Status         string  `json:"status"` // "active", "review", "completed"
	Department     string  `json:"department"`
	BudgetMicrousd int64   `json:"budgetMicrousd"`
	SpentMicrousd  int64   `json:"spentMicrousd"`
	Progress       float64 `json:"progress"`
	CompletedTasks int64   `json:"completedTasks"`
	TotalTasks     int64   `json:"totalTasks"`
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

type CEOMessageRequest struct {
	Message string `json:"message"`
}

type CEOMessageResponse struct {
	Message string `json:"message"`
}

type CreateMissionRequest struct {
	Objective      string `json:"objective"`
	BudgetMicrousd int64  `json:"budgetMicrousd"`
}

type CreateMissionResponse struct {
	Mission Mission `json:"mission"`
	Message string  `json:"message"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}
