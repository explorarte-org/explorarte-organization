package executive

import (
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

func TestTypedOutputSchemasAreModelRuntimeCompatible(t *testing.T) {
	now := time.Date(
		2026, time.August, 18,
		0, 0, 0, 0,
		time.UTC,
	)

	cases := []struct {
		name   string
		schema []byte
	}{
		{
			name:   "executive-plan",
			schema: executivePlanOutputSchema,
		},
		{
			name:   "department-plan",
			schema: departmentPlanOutputSchema,
		},
		{
			name:   "worker-result",
			schema: WorkerResultOutputSchemaFor(DefaultLimits()),
		},
		{
			name:   "department-review",
			schema: departmentReviewOutputSchema,
		},
		{
			name:   "executive-closure",
			schema: executiveClosureOutputSchema,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			command := modelruntime.CreateInvocationCommand{
				OrganizationID:    "explorarte",
				TaskID:            1,
				AttemptID:         1,
				SubjectRoleID:     "empresa/ceo",
				ContextSnapshotID: 1,
				Purpose:           "schema-compatibility-test",
				OutputMode:        modelruntime.OutputJSON,
				OutputSchema:      tc.schema,
				MaxOutputTokens:   128,
				ThinkingMode:      modelruntime.ThinkingOpaque,
				IdempotencyKey:    "schema-compat-" + tc.name,
				Deadline:          now.Add(time.Hour),
			}

			_, _, _, err := modelruntime.PrepareCreateCommand(
				command,
				now,
			)
			if err != nil {
				t.Fatalf(
					"Executive schema rejected by Model Runtime: %v",
					err,
				)
			}
		})
	}
}
