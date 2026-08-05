package document

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

type Loader struct {
	root     string
	maxBytes int64
}

func NewLoader(root string, maxBytes int64) (*Loader, error) {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return nil, errors.New("context source root must be an absolute path")
	}
	if maxBytes <= 0 {
		return nil, errors.New("context source maximum must be positive")
	}
	clean := filepath.Clean(root)
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return nil, fmt.Errorf("resolve context source root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat context source root: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("context source root is not a directory")
	}
	return &Loader{root: resolved, maxBytes: maxBytes}, nil
}

func (l *Loader) Root() string { return l.root }

func (l *Loader) Load(ctx context.Context, relative string, maxBytes int64) (contextengine.LoadedDocument, error) {
	if err := ctx.Err(); err != nil {
		return contextengine.LoadedDocument{}, err
	}
	limit := l.maxBytes
	if maxBytes > 0 && maxBytes < limit {
		limit = maxBytes
	}
	resolved, err := resolveWithinRoot(l.root, relative)
	if err != nil {
		code := contextengine.ReasonSourcePathEscape
		if strings.Contains(err.Error(), "symlink") || strings.Contains(err.Error(), "resolved") {
			code = contextengine.ReasonSourceSymlinkEscape
		}
		return contextengine.LoadedDocument{}, &contextengine.RejectionError{Code: code, Reference: relative, Message: err.Error(), Cause: contextengine.ErrRejected}
	}
	preInfo, err := os.Stat(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return contextengine.LoadedDocument{}, contextengine.Reject(contextengine.ReasonSourceNotFound, relative, "source file not found")
		}
		return contextengine.LoadedDocument{}, fmt.Errorf("stat context source %q: %w", relative, err)
	}
	if !preInfo.Mode().IsRegular() {
		return contextengine.LoadedDocument{}, contextengine.Reject(contextengine.ReasonSourcePathEscape, relative, "source is not a regular file")
	}
	if preInfo.Size() > limit {
		return contextengine.LoadedDocument{}, contextengine.Reject(contextengine.ReasonSourceTooLarge, relative, "source exceeds configured maximum")
	}
	file, err := os.Open(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return contextengine.LoadedDocument{}, contextengine.Reject(contextengine.ReasonSourceNotFound, relative, "source file not found")
		}
		return contextengine.LoadedDocument{}, fmt.Errorf("open context source %q: %w", relative, err)
	}
	defer file.Close()
	if err = validateOpenedFile(l.root, file); err != nil {
		return contextengine.LoadedDocument{}, contextengine.Reject(contextengine.ReasonSourceSymlinkEscape, relative, err.Error())
	}
	info, err := file.Stat()
	if err != nil {
		return contextengine.LoadedDocument{}, fmt.Errorf("stat context source %q: %w", relative, err)
	}
	if info.Size() > limit {
		return contextengine.LoadedDocument{}, contextengine.Reject(contextengine.ReasonSourceTooLarge, relative, "source exceeds configured maximum")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return contextengine.LoadedDocument{}, fmt.Errorf("read context source %q: %w", relative, err)
	}
	if int64(len(data)) > limit {
		return contextengine.LoadedDocument{}, contextengine.Reject(contextengine.ReasonSourceTooLarge, relative, "source exceeds configured maximum")
	}
	normalized, err := NormalizeText(data)
	if err != nil {
		code := contextengine.ReasonInvalidUTF8
		if strings.Contains(err.Error(), "NUL") {
			code = contextengine.ReasonNULByteRejected
		}
		return contextengine.LoadedDocument{}, contextengine.Reject(code, relative, err.Error())
	}
	parsed, err := ParseMarkdown(normalized, false)
	if err != nil {
		return contextengine.LoadedDocument{}, contextengine.Reject(contextengine.ReasonInvalidFrontmatter, relative, err.Error())
	}
	digest := sha256.Sum256(normalized)
	return contextengine.LoadedDocument{
		Path:        relative,
		Content:     data,
		Normalized:  normalized,
		Hash:        hex.EncodeToString(digest[:]),
		Frontmatter: parsed.Frontmatter,
		Body:        parsed.Body,
	}, nil
}
