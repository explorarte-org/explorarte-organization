package gemini

import (
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
)

// renderPrompt applies PromptTemplateV1's deterministic query/document
// prefix. See the PromptTemplateV1 doc comment in config.go for why this
// exists and why bumping the version is the only safe way to change it.
func renderPrompt(templateVersion string, task embeddingruntime.TaskKind, text string) (string, error) {
	if templateVersion != PromptTemplateV1 {
		return "", fmt.Errorf("embeddingruntime gemini: unknown prompt template version %q", templateVersion)
	}
	switch task {
	case embeddingruntime.TaskQuery:
		return "task: search result | query: " + text, nil
	case embeddingruntime.TaskDocument:
		return "task: search result | document: " + text, nil
	default:
		return "", fmt.Errorf("embeddingruntime gemini: unknown task kind %q", task)
	}
}

// taskTypeField mirrors the same query/document distinction as the
// dedicated Google API field embedContentConfig.taskType. Google's
// documentation was inconsistent at implementation time about whether
// gemini-embedding-2 honors this field or only the prompt prefix above —
// this package sends both, which costs nothing if the field is ignored and
// covers the case where it is not.
func taskTypeField(task embeddingruntime.TaskKind) string {
	switch task {
	case embeddingruntime.TaskQuery:
		return "RETRIEVAL_QUERY"
	case embeddingruntime.TaskDocument:
		return "RETRIEVAL_DOCUMENT"
	default:
		return ""
	}
}
