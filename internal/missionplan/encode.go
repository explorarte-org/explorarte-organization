package missionplan

import (
	"encoding/json"

	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
)

func encodePlan(plan coderunner.Plan) ([]byte, error) {
	return json.Marshal(plan)
}
