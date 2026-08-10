// Package pdfingest is the narrow boundary between the organization and
// PDF parsing (owner decision: PDF ingestion). PDFs are untrusted input
// and a genuinely complex parsing surface; nothing in cmd/orgd or
// internal/app ever imports this package or the poppler subpackage that
// implements it -- only a dedicated orgctl command does, mirroring how
// internal/modelruntime is only ever imported by `orgctl model worker
// run`. A malformed PDF can crash a parser; it must never be able to
// crash orgd.
package pdfingest

import (
	"context"
	"errors"
)

// TextExtractionStatus records what happened when this package tried to
// extract a page's text -- distinct from whether the page's bytes/embedding
// succeeded, since a scanned/visual page can be a perfectly good chunk with
// no extractable text at all (see the owner's decision: empty extracted
// text is not an ingestion failure).
type TextExtractionStatus string

const (
	TextExtractionOK          TextExtractionStatus = "ok"
	TextExtractionEmpty       TextExtractionStatus = "empty"
	TextExtractionUnavailable TextExtractionStatus = "unavailable"
)

func (s TextExtractionStatus) Valid() bool {
	switch s {
	case TextExtractionOK, TextExtractionEmpty, TextExtractionUnavailable:
		return true
	default:
		return false
	}
}

// Page is one page of a processed PDF: its own self-contained single-page
// PDF (PDFBytes/SHA256 -- what gets uploaded to Object Storage and later
// embedded multimodally) and its independently extracted text (what
// becomes a chunk's Content, and what dataclassifier.Detect scans).
type Page struct {
	PageNumber           int
	PDFBytes             []byte
	SHA256               string
	ExtractedText        string
	TextExtractionStatus TextExtractionStatus
}

// Result is one source PDF's full page-by-page breakdown, plus the parser
// identity every page in it was produced under -- required provenance
// (owner decision point 9): two years from now, a vector's lineage must be
// reconstructible from document SHA, page, page SHA, parser, and parser
// version, not inferred from a storage path.
type Result struct {
	Pages         []Page
	ParserName    string
	ParserVersion string
}

// QuarantineReason is deliberately a small, closed set: only conditions
// where retrying with the same bytes cannot help. A timeout is NOT one of
// these -- see ErrTimeout -- because a slow parse is not evidence the PDF
// itself is bad.
type QuarantineReason string

const (
	QuarantineMalformed   QuarantineReason = "malformed"
	QuarantineEncrypted   QuarantineReason = "encrypted"
	QuarantineUnsupported QuarantineReason = "unsupported"
)

func (r QuarantineReason) Valid() bool {
	switch r {
	case QuarantineMalformed, QuarantineEncrypted, QuarantineUnsupported:
		return true
	default:
		return false
	}
}

// QuarantineError signals fail-closed rejection of a source PDF: no
// knowledge candidate is ever proposed for it. Detail is safe to log --
// a classification of parser stderr, never raw PDF bytes or content that
// might itself be sensitive.
type QuarantineError struct {
	Reason QuarantineReason
	Detail string
}

func (e *QuarantineError) Error() string {
	return "pdfingest: quarantined (" + string(e.Reason) + "): " + e.Detail
}

var (
	ErrEmptySource = errors.New("pdfingest: source PDF is empty")
	// ErrTimeout means the parser did not finish before ctx's deadline --
	// a transient condition (load, an unusually large/complex PDF), not a
	// verdict on the PDF itself. Callers should retry, not quarantine.
	ErrTimeout = errors.New("pdfingest: parser timed out")
)

// Processor splits a source PDF into one self-contained PDF per page and
// extracts each page's text independently. It is the entire
// subprocess-touching surface of PDF ingestion in this codebase.
type Processor interface {
	Process(ctx context.Context, sourcePDF []byte) (Result, error)
}
