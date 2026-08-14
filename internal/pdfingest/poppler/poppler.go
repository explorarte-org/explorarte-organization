// Package poppler is the only production implementation of
// pdfingest.Processor, and the only package in this codebase (besides the
// retired internal/modelruntime/adapter/alibabaclaude) allowed to invoke
// os/exec -- every command is built as an explicit argv slice, never a
// shell string, per the owner's decision (point 2 of the ingestion
// contract: no shell wrapper).
package poppler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/pdfingest"
)

const ParserName = "poppler"

// Config pins every external dependency this package touches: the three
// poppler-utils binaries by absolute path (resolved once at construction
// via exec.LookPath, never re-resolved per call against a possibly-
// different PATH later), a per-command timeout, and a working directory
// root for the temp files pdfseparate/pdftotext require (these are
// file-oriented CLI tools, not stdin/stdout pipelines, for the split
// step).
type Config struct {
	PdfseparateBinary string
	PdftotextBinary   string
	PdfinfoBinary     string
	// GsBinary (Ghostscript) is the fallback path for pages pdfseparate
	// amplifies -- see isPageAmplified's doc comment for why this is
	// sometimes necessary and not a quarantine condition.
	GsBinary       string
	CommandTimeout time.Duration
	WorkDir        string
	MaxPages       int
}

func DefaultConfig() (Config, error) {
	cfg := Config{CommandTimeout: 2 * time.Minute, WorkDir: os.TempDir(), MaxPages: 2000}
	var err error
	if cfg.PdfseparateBinary, err = exec.LookPath("pdfseparate"); err != nil {
		return Config{}, fmt.Errorf("poppler: pdfseparate not found in PATH: %w", err)
	}
	if cfg.PdftotextBinary, err = exec.LookPath("pdftotext"); err != nil {
		return Config{}, fmt.Errorf("poppler: pdftotext not found in PATH: %w", err)
	}
	if cfg.PdfinfoBinary, err = exec.LookPath("pdfinfo"); err != nil {
		return Config{}, fmt.Errorf("poppler: pdfinfo not found in PATH: %w", err)
	}
	if cfg.GsBinary, err = exec.LookPath("gs"); err != nil {
		return Config{}, fmt.Errorf("poppler: gs (ghostscript) not found in PATH: %w", err)
	}
	return cfg, nil
}

type Processor struct {
	cfg           Config
	parserVersion string
}

var _ pdfingest.Processor = (*Processor)(nil)

// New resolves and caches the installed poppler version once (point 9 of
// the ingestion contract: pin and record the parser version) by running
// `pdftotext -v`, which poppler prints to stderr regardless of exit code.
func New(cfg Config) (*Processor, error) {
	if cfg.PdfseparateBinary == "" || cfg.PdftotextBinary == "" || cfg.PdfinfoBinary == "" || cfg.GsBinary == "" {
		return nil, errors.New("poppler: all four binary paths are required")
	}
	if cfg.CommandTimeout <= 0 {
		return nil, errors.New("poppler: command timeout must be positive")
	}
	if cfg.MaxPages <= 0 {
		return nil, errors.New("poppler: max pages must be positive")
	}
	version, err := resolveVersion(cfg)
	if err != nil {
		return nil, fmt.Errorf("poppler: resolve version: %w", err)
	}
	return &Processor{cfg: cfg, parserVersion: version}, nil
}

var versionPattern = regexp.MustCompile(`pdftotext version (\S+)`)

func resolveVersion(cfg Config) (string, error) {
	cmd := exec.Command(cfg.PdftotextBinary, "-v")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run() // pdftotext -v exits non-zero; only stderr's text matters
	match := versionPattern.FindStringSubmatch(stderr.String())
	if match == nil {
		return "", fmt.Errorf("could not parse version from: %q", stderr.String())
	}
	return match[1], nil
}

// encryptedPattern / malformedPattern classify poppler's stderr text into
// QuarantineReasons. These are necessarily best-effort string matches
// against a CLI's human-readable error output, not a stable API contract
// -- documented here as the single place to update if a poppler upgrade
// changes its wording (see the version pin above).
var (
	encryptedPattern = regexp.MustCompile(`(?i)(incorrect password|encrypted)`)
	malformedPattern = regexp.MustCompile(`(?i)(syntax (warning|error)|may not be a pdf file|damaged|couldn't read xref|invalid)`)
)

func classifyFailure(stderr string) *pdfingest.QuarantineError {
	switch {
	case encryptedPattern.MatchString(stderr):
		return &pdfingest.QuarantineError{Reason: pdfingest.QuarantineEncrypted, Detail: "poppler reported an encrypted/password-protected PDF"}
	case malformedPattern.MatchString(stderr):
		return &pdfingest.QuarantineError{Reason: pdfingest.QuarantineMalformed, Detail: "poppler reported a malformed or damaged PDF"}
	default:
		return &pdfingest.QuarantineError{Reason: pdfingest.QuarantineUnsupported, Detail: "poppler rejected the PDF: " + firstLine(stderr)}
	}
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

func (p *Processor) run(ctx context.Context, binary string, args ...string) (stdout []byte, stderr string, err error) {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.CommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, errBuf.String(), pdfingest.ErrTimeout
	}
	if runErr != nil {
		return nil, errBuf.String(), runErr
	}
	return outBuf.Bytes(), errBuf.String(), nil
}

var pageCountPattern = regexp.MustCompile(`(?m)^Pages:\s*(\d+)\s*$`)

// Process implements pdfingest.Processor. It never touches network, never
// invokes a shell, and confines all file I/O to a dedicated, cleaned-up
// temp subdirectory per call.
func (p *Processor) Process(ctx context.Context, sourcePDF []byte) (pdfingest.Result, error) {
	if len(sourcePDF) == 0 {
		return pdfingest.Result{}, pdfingest.ErrEmptySource
	}

	workDir, err := os.MkdirTemp(p.cfg.WorkDir, "pdfingest-*")
	if err != nil {
		return pdfingest.Result{}, fmt.Errorf("poppler: create work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	sourcePath := filepath.Join(workDir, "source.pdf")
	if err := os.WriteFile(sourcePath, sourcePDF, 0o600); err != nil {
		return pdfingest.Result{}, fmt.Errorf("poppler: write source PDF: %w", err)
	}

	infoOut, infoErr, err := p.run(ctx, p.cfg.PdfinfoBinary, sourcePath)
	if err != nil {
		if errors.Is(err, pdfingest.ErrTimeout) {
			return pdfingest.Result{}, pdfingest.ErrTimeout
		}
		return pdfingest.Result{}, classifyFailure(infoErr)
	}
	match := pageCountPattern.FindSubmatch(infoOut)
	if match == nil {
		return pdfingest.Result{}, &pdfingest.QuarantineError{Reason: pdfingest.QuarantineUnsupported, Detail: "pdfinfo output did not contain a Pages count"}
	}
	pageCount, err := strconv.Atoi(string(match[1]))
	if err != nil || pageCount <= 0 {
		return pdfingest.Result{}, &pdfingest.QuarantineError{Reason: pdfingest.QuarantineUnsupported, Detail: "pdfinfo reported a non-positive page count"}
	}
	if pageCount > p.cfg.MaxPages {
		return pdfingest.Result{}, &pdfingest.QuarantineError{Reason: pdfingest.QuarantineUnsupported, Detail: fmt.Sprintf("page count %d exceeds the configured maximum %d", pageCount, p.cfg.MaxPages)}
	}

	separatedPattern := filepath.Join(workDir, "page-%d.pdf")
	if _, sepErr, err := p.run(ctx, p.cfg.PdfseparateBinary, sourcePath, separatedPattern); err != nil {
		if errors.Is(err, pdfingest.ErrTimeout) {
			return pdfingest.Result{}, pdfingest.ErrTimeout
		}
		return pdfingest.Result{}, classifyFailure(sepErr)
	}

	entries, err := os.ReadDir(workDir)
	if err != nil {
		return pdfingest.Result{}, fmt.Errorf("poppler: list work dir: %w", err)
	}
	var pageFiles []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "page-") && strings.HasSuffix(entry.Name(), ".pdf") {
			pageFiles = append(pageFiles, entry.Name())
		}
	}
	sort.Slice(pageFiles, func(i, j int) bool { return pageNumberOf(pageFiles[i]) < pageNumberOf(pageFiles[j]) })
	if len(pageFiles) != pageCount {
		return pdfingest.Result{}, fmt.Errorf("poppler: pdfseparate produced %d files, pdfinfo reported %d pages", len(pageFiles), pageCount)
	}

	pages := make([]pdfingest.Page, 0, len(pageFiles))
	for _, name := range pageFiles {
		pagePath := filepath.Join(workDir, name)
		pageBytes, err := os.ReadFile(pagePath)
		if err != nil {
			return pdfingest.Result{}, fmt.Errorf("poppler: read separated page: %w", err)
		}
		if isPageAmplified(pageBytes, len(sourcePDF), pageCount) {
			rebuilt, gsErr := p.rebuildPageWithGhostscript(ctx, sourcePath, pageNumberOf(name), workDir)
			if gsErr != nil {
				if errors.Is(gsErr, pdfingest.ErrTimeout) {
					return pdfingest.Result{}, pdfingest.ErrTimeout
				}
				return pdfingest.Result{}, fmt.Errorf("poppler: ghostscript fallback for amplified page: %w", gsErr)
			}
			if err := os.WriteFile(pagePath, rebuilt, 0o600); err != nil {
				return pdfingest.Result{}, fmt.Errorf("poppler: write ghostscript-rebuilt page: %w", err)
			}
			pageBytes = rebuilt
		}
		sum := sha256.Sum256(pageBytes)
		text, status, err := p.extractText(ctx, pagePath)
		if err != nil {
			if errors.Is(err, pdfingest.ErrTimeout) {
				return pdfingest.Result{}, pdfingest.ErrTimeout
			}
			return pdfingest.Result{}, err
		}
		pages = append(pages, pdfingest.Page{
			PageNumber: pageNumberOf(name), PDFBytes: pageBytes, SHA256: hex.EncodeToString(sum[:]),
			ExtractedText: text, TextExtractionStatus: status,
		})
	}

	return pdfingest.Result{Pages: pages, ParserName: ParserName, ParserVersion: p.parserVersion}, nil
}

// pageAmplificationFactor and pageAmplificationFloor together decide when
// a single page pdfseparate produced is "amplified": some source PDFs
// (irregular xref/object structure -- poppler's "recursive dicts" warning
// is the usual tell) cause pdfseparate to carry the *entire* document's
// object graph (every embedded font/image referenced anywhere, not just
// on that page) into each single-page file, instead of just that page's
// own content. A well-formed multi-page PDF splits into pages roughly
// proportional to len(source)/pageCount; amplification means a page is
// several times larger than that fair share. Measured on a real case
// (CUTOVER-DEPLOYMENT-REHEARSAL-003 audit corpus, ICLR 2026 formatting):
// an 11.5MB, 30-page source produced a "page 15" that was itself 11.5MB
// -- i.e. the whole document duplicated once per page, ~345MB total for
// one paper. This is not evidence the PDF is malformed (QuarantineError
// would be the wrong response -- see pdfingest.QuarantineReason's own
// doc comment: retrying with the same bytes can help here, just not via
// pdfseparate), so it gets a fallback, not a rejection.
// var, not const: TestProcessRebuildsAmplifiedPageViaGhostscript overrides
// these to exercise the real Ghostscript subprocess path against a small
// fixture instead of needing an 11MB reproduction of the original case.
var (
	pageAmplificationFactor = 3
	pageAmplificationFloor  = 2 << 20 // 2MiB; below this, splitting overhead isn't worth a second parser pass
)

func isPageAmplified(pageBytes []byte, sourceLen, pageCount int) bool {
	if pageCount <= 1 || sourceLen <= 0 {
		return false
	}
	fairShare := sourceLen / pageCount
	threshold := fairShare * pageAmplificationFactor
	if threshold < pageAmplificationFloor {
		threshold = pageAmplificationFloor
	}
	return len(pageBytes) > threshold
}

// rebuildPageWithGhostscript re-renders exactly one page from the original
// (unseparated) source PDF via Ghostscript's pdfwrite device, which
// rebuilds the page's object graph from scratch instead of copying
// pdfseparate's carried-over one -- confirmed on the case above to shrink
// an amplified 11.5MB page to ~55KB. Only ever called as a fallback for
// isPageAmplified; the primary path stays pdfseparate for every ordinary
// PDF, unchanged.
func (p *Processor) rebuildPageWithGhostscript(ctx context.Context, sourcePath string, pageNum int, workDir string) ([]byte, error) {
	outPath := filepath.Join(workDir, fmt.Sprintf("gs-page-%d.pdf", pageNum))
	pageArg := strconv.Itoa(pageNum)
	_, stderr, err := p.run(ctx, p.cfg.GsBinary,
		"-sDEVICE=pdfwrite", "-dNOPAUSE", "-dBATCH", "-dQUIET",
		"-dFirstPage="+pageArg, "-dLastPage="+pageArg,
		"-sOutputFile="+outPath, sourcePath)
	if err != nil {
		if errors.Is(err, pdfingest.ErrTimeout) {
			return nil, pdfingest.ErrTimeout
		}
		return nil, fmt.Errorf("ghostscript: %s", firstLine(stderr))
	}
	rebuilt, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("read ghostscript output: %w", err)
	}
	return rebuilt, nil
}

// extractText never fails the page on its own: a pdftotext error against
// an already-separated, already-validated single-page PDF is treated as
// "unavailable" (owner decision point 7 — empty/unavailable extraction is
// not an ingestion failure), not propagated as a processing error, except
// for a genuine timeout.
func (p *Processor) extractText(ctx context.Context, pagePath string) (string, pdfingest.TextExtractionStatus, error) {
	out, _, err := p.run(ctx, p.cfg.PdftotextBinary, "-q", "-enc", "UTF-8", pagePath, "-")
	if err != nil {
		if errors.Is(err, pdfingest.ErrTimeout) {
			return "", "", pdfingest.ErrTimeout
		}
		return "", pdfingest.TextExtractionUnavailable, nil
	}
	// pdftotext appends a trailing form feed (\f, page-break convention)
	// after the last newline; trim both, not just \n.
	text := strings.TrimRight(string(out), "\n\f")
	text = stripNULBytes(text)
	if strings.TrimSpace(text) == "" {
		return "", pdfingest.TextExtractionEmpty, nil
	}
	return text, pdfingest.TextExtractionOK, nil
}

// stripNULBytes removes any embedded NUL byte pdftotext -enc UTF-8
// occasionally emits for certain malformed ligature/font edge cases --
// not a text-encoding error (the rest of the run is valid UTF-8), just a
// stray byte that carries no meaning. PostgreSQL's text type can never
// store one (INSERT fails with "invalid byte sequence for encoding
// UTF8: 0x00"), so this is the only place in the pipeline that could
// ever encounter it: strip it here, once, rather than let every
// downstream consumer (rag, memory, contextengine) reimplement the same
// defense.
func stripNULBytes(text string) string {
	if strings.IndexByte(text, 0) < 0 {
		return text
	}
	return strings.ReplaceAll(text, "\x00", "")
}

var pageFileNumberPattern = regexp.MustCompile(`page-(\d+)\.pdf`)

func pageNumberOf(name string) int {
	match := pageFileNumberPattern.FindStringSubmatch(name)
	if match == nil {
		return 0
	}
	n, _ := strconv.Atoi(match[1])
	return n
}
