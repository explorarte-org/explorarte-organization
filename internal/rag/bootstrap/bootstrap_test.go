package bootstrap

import "testing"

func TestActiveEmbeddingProfileDefaultsToGemini(t *testing.T) {
	t.Setenv("ORG_EMBEDDING_ACTIVE_PROFILE", "")
	profile, err := activeEmbeddingProfile()
	if err != nil {
		t.Fatal(err)
	}
	if profile != embeddingProfileGemini768 {
		t.Fatalf("profile=%q want %q", profile, embeddingProfileGemini768)
	}
}

func TestActiveEmbeddingProfileAcceptsBGEM3(t *testing.T) {
	t.Setenv("ORG_EMBEDDING_ACTIVE_PROFILE", embeddingProfileBGEM3Local)
	profile, err := activeEmbeddingProfile()
	if err != nil {
		t.Fatal(err)
	}
	if profile != embeddingProfileBGEM3Local {
		t.Fatalf("profile=%q want %q", profile, embeddingProfileBGEM3Local)
	}
}

func TestActiveEmbeddingProfileRejectsUnknownValue(t *testing.T) {
	t.Setenv("ORG_EMBEDDING_ACTIVE_PROFILE", "gemini-and-bge-m3-both-please")
	if _, err := activeEmbeddingProfile(); err == nil {
		t.Fatal("expected an unknown profile value to fail closed, not silently fall back")
	}
}
