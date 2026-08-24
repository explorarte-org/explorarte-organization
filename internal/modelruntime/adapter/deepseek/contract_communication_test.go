package deepseek

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

// GUARD A -- CONTRACT COMMUNICATION, at the prompt boundary.
//
// The schema Executive hands a department_worker run is appended to the
// prompt verbatim (json_object mode has no provider-side schema guarantee on
// this endpoint). R7's model produced 5750-byte summaries because no limit
// had ever reached it. This proves the REAL rendered request for a
// department_worker contract carries, for summary: the maxLength keyword and
// the explicit UTF-8 byte rule -- both required, because maxLength counts
// code points and does not by itself express the host's <=4000 bytes.
func TestDepartmentWorkerPromptCarriesTheByteLimitContract(t *testing.T) {
	request := validRequest(time.Now().Add(time.Minute))
	request.OutputMode = modelruntime.OutputJSON
	request.OutputSchema = executive.WorkerResultOutputSchemaFor(executive.DefaultLimits())
	body, err := encodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Messages) == 0 {
		t.Fatal("rendered request carries no messages")
	}
	prompt := payload.Messages[0].Content
	for _, want := range []string{
		`"maxLength": 4000`,
		"UTF-8 encoded representation must not exceed 4000 bytes",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("department_worker prompt is missing %q:\n%s", want, prompt)
		}
	}
}
