package poppler

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/pdfingest"
)

// newTestProcessor skips the test (not fails) when poppler-utils is not
// installed on the machine running `go test` -- these are real, live
// tests against the actual binaries, not mocks, so they only run where
// poppler is actually present (this repo's Docker build environments and
// the VPS both have it; a bare `go test ./...` elsewhere still passes
// cleanly).
func newTestProcessor(t *testing.T) *Processor {
	t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		t.Skip("poppler-utils not installed, skipping live poppler tests:", err)
	}
	cfg.CommandTimeout = 10 * time.Second
	proc, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return proc
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProcessSplitsTwoPagePDFAndExtractsPerPageText(t *testing.T) {
	proc := newTestProcessor(t)
	result, err := proc.Process(context.Background(), readFixture(t, "two-page.pdf"))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.ParserName != ParserName || result.ParserVersion == "" {
		t.Fatalf("parser identity=%+v", result)
	}
	if len(result.Pages) != 2 {
		t.Fatalf("pages=%d want 2", len(result.Pages))
	}
	if result.Pages[0].PageNumber != 1 || result.Pages[1].PageNumber != 2 {
		t.Fatalf("page numbers = %d, %d", result.Pages[0].PageNumber, result.Pages[1].PageNumber)
	}
	if result.Pages[0].ExtractedText != "Page one content here" {
		t.Fatalf("page 1 text=%q", result.Pages[0].ExtractedText)
	}
	if result.Pages[1].ExtractedText != "Page two content here" {
		t.Fatalf("page 2 text=%q", result.Pages[1].ExtractedText)
	}
	for i, page := range result.Pages {
		if page.TextExtractionStatus != pdfingest.TextExtractionOK {
			t.Fatalf("page %d status=%q want ok", i, page.TextExtractionStatus)
		}
		if len(page.PDFBytes) == 0 {
			t.Fatalf("page %d has no PDF bytes", i)
		}
		if len(page.SHA256) != 64 {
			t.Fatalf("page %d sha256=%q", i, page.SHA256)
		}
	}
	// Each page's bytes must be its OWN self-contained single-page PDF,
	// not a slice of the original — verify poppler itself can re-read it
	// independently (proves it is a valid, standalone PDF, not raw bytes
	// that merely look plausible).
	tmp := t.TempDir() + "/page1.pdf"
	if err := os.WriteFile(tmp, result.Pages[0].PDFBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := proc.run(context.Background(), proc.cfg.PdfinfoBinary, tmp); err != nil {
		t.Fatalf("separated page is not independently readable: %v", err)
	}
}

func TestProcessQuarantinesEncryptedPDF(t *testing.T) {
	proc := newTestProcessor(t)
	_, err := proc.Process(context.Background(), readFixture(t, "encrypted.pdf"))
	var quarantine *pdfingest.QuarantineError
	if !errors.As(err, &quarantine) {
		t.Fatalf("err=%v, want *QuarantineError", err)
	}
	if quarantine.Reason != pdfingest.QuarantineEncrypted {
		t.Fatalf("reason=%q want encrypted", quarantine.Reason)
	}
}

func TestProcessQuarantinesMalformedPDF(t *testing.T) {
	proc := newTestProcessor(t)
	_, err := proc.Process(context.Background(), readFixture(t, "malformed.pdf"))
	var quarantine *pdfingest.QuarantineError
	if !errors.As(err, &quarantine) {
		t.Fatalf("err=%v, want *QuarantineError", err)
	}
	if quarantine.Reason != pdfingest.QuarantineMalformed {
		t.Fatalf("reason=%q want malformed", quarantine.Reason)
	}
}

func TestProcessRecordsEmptyTextExtractionWithoutFailing(t *testing.T) {
	proc := newTestProcessor(t)
	result, err := proc.Process(context.Background(), readFixture(t, "no-text.pdf"))
	if err != nil {
		t.Fatalf("Process: %v — an image-only page must not be a processing failure", err)
	}
	if len(result.Pages) != 1 {
		t.Fatalf("pages=%d want 1", len(result.Pages))
	}
	page := result.Pages[0]
	if page.TextExtractionStatus != pdfingest.TextExtractionEmpty {
		t.Fatalf("status=%q want empty", page.TextExtractionStatus)
	}
	if page.ExtractedText != "" {
		t.Fatalf("extracted text=%q want empty string", page.ExtractedText)
	}
	if len(page.PDFBytes) == 0 || len(page.SHA256) != 64 {
		t.Fatalf("page bytes/sha256 must still be populated for a text-empty page: %+v", page)
	}
}

func TestProcessRejectsEmptySource(t *testing.T) {
	proc := newTestProcessor(t)
	_, err := proc.Process(context.Background(), nil)
	if !errors.Is(err, pdfingest.ErrEmptySource) {
		t.Fatalf("err=%v want ErrEmptySource", err)
	}
}

func TestProcessTimesOutOnAnUnreasonablyShortDeadline(t *testing.T) {
	cfg, err := DefaultConfig()
	if err != nil {
		t.Skip("poppler-utils not installed:", err)
	}
	cfg.CommandTimeout = time.Nanosecond
	proc, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = proc.Process(context.Background(), readFixture(t, "two-page.pdf"))
	if !errors.Is(err, pdfingest.ErrTimeout) {
		t.Fatalf("err=%v want ErrTimeout", err)
	}
}

// TestProcessIsIdempotentAcrossRepeatedCalls documents a real, verified
// constraint: poppler's pdfseparate output is NOT byte-for-byte
// deterministic across runs on identical input (it embeds something that
// varies -- observed in practice, not merely theoretical) even though the
// semantic content (extracted text, page count) is stable. This is why
// the ingestion orchestration's retry/duplicate-idempotency key (owner
// decision point 10) must be (source PDF SHA256, page number), never a
// re-derived page-PDF SHA256 -- two independent Process() calls on the
// same source legitimately produce different media_sha256 values for the
// "same" page.
func TestProcessIsIdempotentAcrossRepeatedCalls(t *testing.T) {
	proc := newTestProcessor(t)
	data := readFixture(t, "two-page.pdf")
	first, err := proc.Process(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	second, err := proc.Process(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Pages) != len(second.Pages) {
		t.Fatalf("page counts differ across runs: %d vs %d", len(first.Pages), len(second.Pages))
	}
	for i := range first.Pages {
		if first.Pages[i].ExtractedText != second.Pages[i].ExtractedText {
			t.Fatalf("page %d text differs across identical runs", i)
		}
		if first.Pages[i].TextExtractionStatus != second.Pages[i].TextExtractionStatus {
			t.Fatalf("page %d status differs across identical runs", i)
		}
		if len(first.Pages[i].SHA256) != 64 || len(second.Pages[i].SHA256) != 64 {
			t.Fatalf("page %d sha256 not populated on one of the runs", i)
		}
	}
}

func TestDefaultConfigFailsClosedWhenBinaryMissing(t *testing.T) {
	t.Setenv("PATH", "")
	if _, err := DefaultConfig(); err == nil {
		t.Fatal("expected DefaultConfig to fail when PATH has no poppler binaries")
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected New to reject a zero-value Config")
	}
}

func TestIsPageAmplifiedDetectsDisproportionatePages(t *testing.T) {
	// Real case this guards against (CUTOVER-DEPLOYMENT-REHEARSAL-003
	// audit corpus): an 11.5MB, 30-page source where pdfseparate carried
	// the entire document into a single "page", instead of that page's
	// fair share (~384KB).
	sourceLen := 11_567_785
	pageCount := 30
	amplifiedPage := make([]byte, 11_567_601)
	if !isPageAmplified(amplifiedPage, sourceLen, pageCount) {
		t.Fatal("expected the reproduced real-world amplified page to be detected")
	}
}

func TestIsPageAmplifiedAllowsOrdinaryPages(t *testing.T) {
	sourceLen := 11_567_785
	pageCount := 30
	fairShare := sourceLen / pageCount
	ordinaryPage := make([]byte, fairShare*2) // 2x fair share: normal variance, not amplification
	if isPageAmplified(ordinaryPage, sourceLen, pageCount) {
		t.Fatal("a page at 2x fair share should not be flagged as amplified")
	}
}

func TestIsPageAmplifiedIgnoresSmallFilesBelowFloor(t *testing.T) {
	// A tiny single-page source: fairShare*factor is smaller than the
	// floor, and the floor itself should not fire on ordinary small PDFs.
	sourceLen := 100_000
	pageCount := 1
	page := make([]byte, 150_000)
	if isPageAmplified(page, sourceLen, pageCount) {
		t.Fatal("pageCount<=1 must never be flagged as amplified -- there is nothing to compare it against")
	}
}

func TestStripNULBytesRemovesEmbeddedNUL(t *testing.T) {
	in := "Managing Procedural Memory\x00 in LLM Agents"
	want := "Managing Procedural Memory in LLM Agents"
	if got := stripNULBytes(in); got != want {
		t.Fatalf("stripNULBytes(%q) = %q, want %q", in, got, want)
	}
}

func TestStripNULBytesLeavesCleanTextUntouched(t *testing.T) {
	in := "no null bytes here at all"
	if got := stripNULBytes(in); got != in {
		t.Fatalf("stripNULBytes(%q) = %q, want unchanged", in, got)
	}
}

func TestProcessRebuildsAmplifiedPageViaGhostscript(t *testing.T) {
	proc := newTestProcessor(t)
	// Force every page to look "amplified" by dropping the floor to zero,
	// so this test exercises the real Ghostscript subprocess path against
	// the small checked-in fixture instead of needing an 11MB reproduction
	// of the original bloated-PDF case.
	originalFactor, originalFloor := pageAmplificationFactor, pageAmplificationFloor
	pageAmplificationFactor = 0
	pageAmplificationFloor = 0
	t.Cleanup(func() { pageAmplificationFactor, pageAmplificationFloor = originalFactor, originalFloor })

	source := readFixture(t, "two-page.pdf")
	result, err := proc.Process(context.Background(), source)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(result.Pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(result.Pages))
	}
	for _, page := range result.Pages {
		if len(page.SHA256) != 64 {
			t.Fatalf("page %d missing sha256 after ghostscript rebuild", page.PageNumber)
		}
		if page.ExtractedText == "" {
			t.Fatalf("page %d lost its extracted text after ghostscript rebuild", page.PageNumber)
		}
	}
}
