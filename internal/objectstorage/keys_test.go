package objectstorage

import "testing"

const (
	testSourceSHA = "93b9a5429155bfcee56fd1f993b106cc588f138b27598b0e066d8e1af21bf086"
	testMediaSHA  = "b51ec014b15f942551263d605b3587db5a6610a7132655696f94f8e43fa9b474"
)

func TestSourceObjectKeyUsesFullSHA256(t *testing.T) {
	key, err := SourceObjectKey(testSourceSHA)
	if err != nil {
		t.Fatalf("SourceObjectKey: %v", err)
	}
	want := "raw/" + testSourceSHA + ".pdf"
	if key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}
}

func TestSourceObjectKeyRejectsTruncatedPrefix(t *testing.T) {
	if _, err := SourceObjectKey(testSourceSHA[:12]); err == nil {
		t.Fatalf("expected error for a 12-char prefix instead of a full SHA-256")
	}
}

func TestSourceObjectKeyRejectsUppercaseOrGarbage(t *testing.T) {
	cases := []string{
		"",
		"not-hex",
		"A1B2C3D4E5F60718293A4B5C6D7E8F90112233445566778899AABBCCDDEEFF",
		testSourceSHA + "f", // 65 chars
	}
	for _, c := range cases {
		if _, err := SourceObjectKey(c); err == nil {
			t.Fatalf("expected error for invalid sha256 %q", c)
		}
	}
}

func TestParserRunManifestKeyIsStableAndDeterministic(t *testing.T) {
	key1, err := ParserRunManifestKey(testSourceSHA, "poppler", "24.02")
	if err != nil {
		t.Fatalf("ParserRunManifestKey: %v", err)
	}
	key2, err := ParserRunManifestKey(testSourceSHA, "poppler", "24.02")
	if err != nil {
		t.Fatalf("ParserRunManifestKey: %v", err)
	}
	if key1 != key2 {
		t.Fatalf("key not deterministic: %q vs %q", key1, key2)
	}
	want := "manifests/parser-runs/" + testSourceSHA + "/poppler/24.02/manifest.json"
	if key1 != want {
		t.Fatalf("key = %q, want %q", key1, want)
	}
}

func TestParserRunManifestKeyChangesWithParserVersion(t *testing.T) {
	keyV1, err := ParserRunManifestKey(testSourceSHA, "poppler", "24.02")
	if err != nil {
		t.Fatalf("ParserRunManifestKey: %v", err)
	}
	keyV2, err := ParserRunManifestKey(testSourceSHA, "poppler", "24.03")
	if err != nil {
		t.Fatalf("ParserRunManifestKey: %v", err)
	}
	if keyV1 == keyV2 {
		t.Fatalf("expected different keys for different parser_version, got same key %q", keyV1)
	}
}

func TestParserRunManifestKeyRejectsBadInputs(t *testing.T) {
	if _, err := ParserRunManifestKey("bad-sha", "poppler", "24.02"); err == nil {
		t.Fatalf("expected error for invalid source sha256")
	}
	if _, err := ParserRunManifestKey(testSourceSHA, "", "24.02"); err == nil {
		t.Fatalf("expected error for empty parser name")
	}
	if _, err := ParserRunManifestKey(testSourceSHA, "poppler/../evil", "24.02"); err == nil {
		t.Fatalf("expected error for parser name with path traversal characters")
	}
}

func TestPageObjectKeyIncludesFullProvenanceTuple(t *testing.T) {
	key, err := PageObjectKey(testSourceSHA, "poppler", "24.02", 1, testMediaSHA)
	if err != nil {
		t.Fatalf("PageObjectKey: %v", err)
	}
	want := "pages/" + testSourceSHA + "/poppler/24.02/page-0001-" + testMediaSHA + ".pdf"
	if key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}
}

func TestPageObjectKeyDiffersWhenMediaSHA256Differs(t *testing.T) {
	// Two runs of the same source+parser+version+page producing different
	// (non-deterministic pdfseparate) bytes must land on different keys,
	// never overwrite one another.
	mediaA := testMediaSHA
	mediaB := testSourceSHA // any other distinct valid sha256 hex value

	keyA, err := PageObjectKey(testSourceSHA, "poppler", "24.02", 3, mediaA)
	if err != nil {
		t.Fatalf("PageObjectKey: %v", err)
	}
	keyB, err := PageObjectKey(testSourceSHA, "poppler", "24.02", 3, mediaB)
	if err != nil {
		t.Fatalf("PageObjectKey: %v", err)
	}
	if keyA == keyB {
		t.Fatalf("expected distinct keys for distinct media SHA-256, got same key %q", keyA)
	}
}

func TestPageObjectKeyRejectsInvalidPageNumber(t *testing.T) {
	if _, err := PageObjectKey(testSourceSHA, "poppler", "24.02", 0, testMediaSHA); err == nil {
		t.Fatalf("expected error for page number 0")
	}
	if _, err := PageObjectKey(testSourceSHA, "poppler", "24.02", -1, testMediaSHA); err == nil {
		t.Fatalf("expected error for negative page number")
	}
}

func TestPageObjectKeyRejectsInvalidMediaSHA256(t *testing.T) {
	if _, err := PageObjectKey(testSourceSHA, "poppler", "24.02", 1, "not-a-sha256"); err == nil {
		t.Fatalf("expected error for invalid media sha256")
	}
}
