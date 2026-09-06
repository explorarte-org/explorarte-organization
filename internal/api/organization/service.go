package organization

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mireuz13/explorarte-organization/internal/agentbudget"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type TaskCreator interface {
	CreateTask(ctx context.Context, request tasks.CreateRequest, actorType, actorID string) (tasks.Task, bool, error)
}

type AcceptanceRecorder interface {
	RecordAcceptance(ctx context.Context, rootTaskID int64, criteria []executive.AcceptanceCriterion) error
}

type BudgetCreator interface {
	CreateRootBudget(ctx context.Context, organizationID string, rootTaskID int64, roleID string, limits agentbudget.Limits, now time.Time) (agentbudget.Budget, error)
}

type Service struct {
	pool        *pgxpool.Pool
	taskService TaskCreator
	acceptance  AcceptanceRecorder
	budgetStore BudgetCreator
	cfg         config.Config
	logger      *slog.Logger
}

func NewService(
	pool *pgxpool.Pool,
	taskService TaskCreator,
	acceptance AcceptanceRecorder,
	budgetStore BudgetCreator,
	cfg config.Config,
	logger *slog.Logger,
) (*Service, error) {
	if pool == nil {
		return nil, errors.New("organization api service requires a PostgreSQL pool")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		pool:        pool,
		taskService: taskService,
		acceptance:  acceptance,
		budgetStore: budgetStore,
		cfg:         cfg,
		logger:      logger,
	}, nil
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/organization/snapshot", s.HandleSnapshot)
	mux.HandleFunc("POST /api/organization/ceo/messages", s.HandleCEOMessage)
	mux.HandleFunc("POST /api/organization/missions", s.HandleCreateMission)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Default().Error("encode json response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}
