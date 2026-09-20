package ceochat

import (
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// The task engine exempts CEO chat turns from scoping the work they request
// (tasks.TaskClassCEOChatTurn). The engine cannot import this package, so the
// two spellings are pinned here: if the chat turn class ever changes, the
// exemption must change with it or a failed turn would start withdrawing the
// Finance reviews it requested.
func TestChatTurnTaskClassIsTheOneTheTaskEngineExempts(t *testing.T) {
	if TaskClass != tasks.TaskClassCEOChatTurn {
		t.Fatalf("ceochat.TaskClass = %q but tasks.TaskClassCEOChatTurn = %q", TaskClass, tasks.TaskClassCEOChatTurn)
	}
}
