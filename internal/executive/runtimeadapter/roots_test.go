package runtimeadapter

import (
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

func TestIsExecutiveRoot_IdentificationAndExclusion(t *testing.T) {
	ownerRole := executive.OwnerRoleID
	ceoRole := executive.CEORoleID
	otherRole := "negocio/director_negocio"
	corr := "exec-corr:test-123"
	emptyCorr := ""

	validReqs := []tasks.Requirement{
		{Key: "executive_closure_verified", Type: tasks.RequirementResult, Required: true},
	}

	tests := []struct {
		name   string
		detail tasks.TaskDetail
		want   bool
	}{
		{
			name: "valid executive campaign root",
			detail: tasks.TaskDetail{
				Task: tasks.Task{
					TaskClass:         executive.TaskClassOwnerGoal,
					AssignedRoleID:    ceoRole,
					RequestedByRoleID: &ownerRole,
					CorrelationID:     &corr,
				},
				Requirements: validReqs,
			},
			want: true,
		},
		{
			name: "ceo chat turn task must NEVER be picked up",
			detail: tasks.TaskDetail{
				Task: tasks.Task{
					TaskClass:         "executive.ceo_chat_turn",
					AssignedRoleID:    ceoRole,
					RequestedByRoleID: &ownerRole,
					CorrelationID:     &corr,
				},
				Requirements: validReqs,
			},
			want: false,
		},
		{
			name: "coordination plan task excluded",
			detail: tasks.TaskDetail{
				Task: tasks.Task{
					TaskClass:         "coordination.ceo_plan",
					AssignedRoleID:    ceoRole,
					RequestedByRoleID: &ownerRole,
					CorrelationID:     &corr,
				},
				Requirements: validReqs,
			},
			want: false,
		},
		{
			name: "missing correlation id excluded",
			detail: tasks.TaskDetail{
				Task: tasks.Task{
					TaskClass:         executive.TaskClassOwnerGoal,
					AssignedRoleID:    ceoRole,
					RequestedByRoleID: &ownerRole,
					CorrelationID:     nil,
				},
				Requirements: validReqs,
			},
			want: false,
		},
		{
			name: "empty correlation id excluded",
			detail: tasks.TaskDetail{
				Task: tasks.Task{
					TaskClass:         executive.TaskClassOwnerGoal,
					AssignedRoleID:    ceoRole,
					RequestedByRoleID: &ownerRole,
					CorrelationID:     &emptyCorr,
				},
				Requirements: validReqs,
			},
			want: false,
		},
		{
			name: "wrong assigned role excluded",
			detail: tasks.TaskDetail{
				Task: tasks.Task{
					TaskClass:         executive.TaskClassOwnerGoal,
					AssignedRoleID:    otherRole,
					RequestedByRoleID: &ownerRole,
					CorrelationID:     &corr,
				},
				Requirements: validReqs,
			},
			want: false,
		},
		{
			name: "wrong requested by role excluded",
			detail: tasks.TaskDetail{
				Task: tasks.Task{
					TaskClass:         executive.TaskClassOwnerGoal,
					AssignedRoleID:    ceoRole,
					RequestedByRoleID: &otherRole,
					CorrelationID:     &corr,
				},
				Requirements: validReqs,
			},
			want: false,
		},
		{
			name: "missing executive closure requirement excluded",
			detail: tasks.TaskDetail{
				Task: tasks.Task{
					TaskClass:         executive.TaskClassOwnerGoal,
					AssignedRoleID:    ceoRole,
					RequestedByRoleID: &ownerRole,
					CorrelationID:     &corr,
				},
				Requirements: []tasks.Requirement{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isExecutiveRoot(tt.detail)
			if got != tt.want {
				t.Errorf("isExecutiveRoot() = %v, want %v", got, tt.want)
			}
		})
	}
}
