package embeddingruntime

import "testing"

func TestEmbedItemValidText(t *testing.T) {
	item := EmbedItem{Key: "a", Text: "hola", Task: TaskDocument}
	if !item.Valid() {
		t.Fatal("expected valid text item")
	}
	if item.IsMedia() {
		t.Fatal("text item must not report IsMedia")
	}
}

func TestEmbedItemValidMedia(t *testing.T) {
	item := EmbedItem{Key: "a", MimeType: "application/pdf", Data: []byte("%PDF"), Task: TaskDocument}
	if !item.Valid() {
		t.Fatal("expected valid media item")
	}
	if !item.IsMedia() {
		t.Fatal("media item must report IsMedia")
	}
}

func TestEmbedItemInvalidMissingKey(t *testing.T) {
	item := EmbedItem{Text: "hola", Task: TaskDocument}
	if item.Valid() {
		t.Fatal("expected invalid item with missing key")
	}
}

func TestEmbedItemInvalidBadTask(t *testing.T) {
	item := EmbedItem{Key: "a", Text: "hola", Task: "bogus"}
	if item.Valid() {
		t.Fatal("expected invalid item with bad task")
	}
}

func TestEmbedItemInvalidBothTextAndMedia(t *testing.T) {
	item := EmbedItem{Key: "a", Text: "hola", MimeType: "application/pdf", Data: []byte("%PDF"), Task: TaskDocument}
	if item.Valid() {
		t.Fatal("expected invalid item with both text and media set")
	}
}

func TestEmbedItemInvalidNeitherTextNorMedia(t *testing.T) {
	item := EmbedItem{Key: "a", Task: TaskDocument}
	if item.Valid() {
		t.Fatal("expected invalid item with neither text nor media set")
	}
}

func TestEmbedItemInvalidMimeTypeWithoutData(t *testing.T) {
	item := EmbedItem{Key: "a", MimeType: "application/pdf", Task: TaskDocument}
	if item.Valid() {
		t.Fatal("expected invalid item with mime type but no data")
	}
}

func TestEmbedItemInvalidDataWithoutMimeType(t *testing.T) {
	item := EmbedItem{Key: "a", Data: []byte("%PDF"), Task: TaskDocument}
	if item.Valid() {
		t.Fatal("expected invalid item with data but no mime type")
	}
}
