package corpuscensus

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/pdfingest"
)

// ValidationConfig bounds how long PDF validation may run per document
// (owner decision, Part B section 10: default bounded timeout + a single
// controlled retry at a longer bound, never unbounded processing). The
// 186-page, ~2m32s real-world case (2408.06292v3.pdf, found during the
// self-improving-agents canary) is exactly why RetryTimeout exists and is
// larger than DefaultTimeout, rather than simply raising DefaultTimeout
// for every document.
type ValidationConfig struct {
	DefaultTimeout time.Duration
	RetryTimeout   time.Duration
}

func DefaultValidationConfig() ValidationConfig {
	return ValidationConfig{DefaultTimeout: 2 * time.Minute, RetryTimeout: 5 * time.Minute}
}

// ValidatePDF runs internal/pdfingest's Processor (Poppler, reused
// unmodified -- this package never re-implements PDF parsing) against one
// local file and reduces the result to a PDFValidation summary plus the
// Decision this document warrants. It never uploads anything and never
// keeps page bytes beyond this call's stack.
func ValidatePDF(ctx context.Context, processor pdfingest.Processor, path string, cfg ValidationConfig) (PDFValidation, Decision, string) {
	body, err := os.ReadFile(path)
	if err != nil {
		return PDFValidation{Valid: false, ParserStatus: "read_error"}, DecisionInvalid, "could not read file: " + err.Error()
	}

	result, timeoutPolicy, err := runWithTimeoutPolicy(ctx, processor, body, cfg)
	if err != nil {
		var qerr *pdfingest.QuarantineError
		if errors.As(err, &qerr) {
			switch qerr.Reason {
			case pdfingest.QuarantineEncrypted:
				return PDFValidation{Valid: false, Encrypted: true, ParserStatus: "quarantined", QuarantineReason: string(qerr.Reason), TimeoutPolicy: TimeoutNone}, DecisionEncrypted, qerr.Detail
			default:
				return PDFValidation{Valid: false, ParserStatus: "quarantined", QuarantineReason: string(qerr.Reason), TimeoutPolicy: TimeoutNone}, DecisionInvalid, qerr.Detail
			}
		}
		if errors.Is(err, pdfingest.ErrTimeout) {
			return PDFValidation{Valid: false, ParserStatus: "timeout", TimeoutPolicy: timeoutPolicy}, DecisionTimeout, "parser exceeded both the default and retry timeout bounds"
		}
		if errors.Is(err, pdfingest.ErrEmptySource) {
			return PDFValidation{Valid: false, ParserStatus: "empty_source"}, DecisionInvalid, "source file is empty"
		}
		return PDFValidation{Valid: false, ParserStatus: "error"}, DecisionInvalid, err.Error()
	}

	emptyPages, refPages := 0, 0
	for _, page := range result.Pages {
		if page.TextExtractionStatus == pdfingest.TextExtractionEmpty {
			emptyPages++
		}
		if LooksLikeReferencesPage(page.PageNumber, len(result.Pages), page.ExtractedText) {
			refPages++
		}
	}

	validation := PDFValidation{
		Valid:           true,
		Pages:           len(result.Pages),
		EmptyTextPages:  emptyPages,
		ReferencesPages: refPages,
		ParserName:      result.ParserName,
		ParserVersion:   result.ParserVersion,
		ParserStatus:    "ok",
		TimeoutPolicy:   timeoutPolicy,
	}

	if len(result.Pages) > 0 && emptyPages == len(result.Pages) {
		return validation, DecisionReviewRequired, "all pages are visual/scanned with no extractable text -- valid PDF, flagged for review rather than auto-accepted since domain relevance cannot be text-verified yet"
	}
	return validation, DecisionAccepted, ""
}

// runWithTimeoutPolicy tries DefaultTimeout first; on ErrTimeout only
// (never on a QuarantineError -- retrying a malformed PDF cannot help),
// it retries once at RetryTimeout. This is the entire retry policy: at
// most one retry, always bounded, per owner decision section 10.
func runWithTimeoutPolicy(ctx context.Context, processor pdfingest.Processor, body []byte, cfg ValidationConfig) (pdfingest.Result, TimeoutPolicy, error) {
	firstCtx, cancel := context.WithTimeout(ctx, cfg.DefaultTimeout)
	result, err := processor.Process(firstCtx, body)
	cancel()
	if err == nil {
		return result, TimeoutNone, nil
	}
	if !errors.Is(err, pdfingest.ErrTimeout) {
		return pdfingest.Result{}, TimeoutNone, err
	}
	retryCtx, cancel := context.WithTimeout(ctx, cfg.RetryTimeout)
	result, err = processor.Process(retryCtx, body)
	cancel()
	if err == nil {
		return result, TimeoutRetryable, nil
	}
	if errors.Is(err, pdfingest.ErrTimeout) {
		return pdfingest.Result{}, TimeoutHard, err
	}
	return pdfingest.Result{}, TimeoutRetryable, err
}
