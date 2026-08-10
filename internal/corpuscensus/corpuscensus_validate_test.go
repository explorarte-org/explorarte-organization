package corpuscensus

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/pdfingest/poppler"
)

// newTestProcessor reuses internal/pdfingest/poppler's own fixtures
// (internal/pdfingest/poppler/testdata) rather than duplicating small PDF
// binaries -- these tests skip cleanly (not fail) when poppler-utils
// genuinely is not installed, matching that package's own convention.
func newTestProcessor(t *testing.T) (*poppler.Processor, string) {
	t.Helper()
	cfg, err := poppler.DefaultConfig()
	if err != nil {
		t.Skipf("poppler-utils not available: %v", err)
	}
	processor, err := poppler.New(cfg)
	if err != nil {
		t.Skipf("poppler-utils not available: %v", err)
	}
	return processor, filepath.Join("..", "pdfingest", "poppler", "testdata")
}

func TestValidatePDFAcceptsValidTwoPagePDF(t *testing.T) {
	processor, dir := newTestProcessor(t)
	validation, decision, reason := ValidatePDF(context.Background(), processor, filepath.Join(dir, "two-page.pdf"), DefaultValidationConfig())
	if decision != DecisionAccepted {
		t.Fatalf("decision=%s reason=%q", decision, reason)
	}
	if !validation.Valid || validation.Pages != 2 {
		t.Fatalf("validation=%+v", validation)
	}
}

func TestValidatePDFQuarantinesEncryptedPDF(t *testing.T) {
	processor, dir := newTestProcessor(t)
	validation, decision, _ := ValidatePDF(context.Background(), processor, filepath.Join(dir, "encrypted.pdf"), DefaultValidationConfig())
	if decision != DecisionEncrypted {
		t.Fatalf("decision=%s", decision)
	}
	if !validation.Encrypted {
		t.Fatalf("validation.Encrypted=false, expected true")
	}
}

func TestValidatePDFQuarantinesMalformedPDF(t *testing.T) {
	processor, dir := newTestProcessor(t)
	validation, decision, _ := ValidatePDF(context.Background(), processor, filepath.Join(dir, "malformed.pdf"), DefaultValidationConfig())
	if decision != DecisionInvalid {
		t.Fatalf("decision=%s", decision)
	}
	if validation.Valid {
		t.Fatal("expected Valid=false for a malformed PDF")
	}
}

func TestValidatePDFFlagsAllEmptyTextAsReviewRequired(t *testing.T) {
	processor, dir := newTestProcessor(t)
	validation, decision, reason := ValidatePDF(context.Background(), processor, filepath.Join(dir, "no-text.pdf"), DefaultValidationConfig())
	if decision != DecisionReviewRequired {
		t.Fatalf("decision=%s reason=%q", decision, reason)
	}
	if !validation.Valid {
		t.Fatal("a scanned/visual PDF is still a VALID pdf -- empty text is not an ingestion failure")
	}
	if validation.EmptyTextPages != validation.Pages {
		t.Fatalf("EmptyTextPages=%d Pages=%d, expected all pages empty", validation.EmptyTextPages, validation.Pages)
	}
}
