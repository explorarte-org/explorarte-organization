package document

import (
	"bytes"
	"errors"
	"fmt"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type Markdown struct {
	Normalized     []byte
	Frontmatter    map[string]any
	FrontNode      *yaml.Node
	Body           []byte
	HasFrontmatter bool
}

func NormalizeText(data []byte) ([]byte, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("document is not valid UTF-8")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, errors.New("document contains a NUL byte")
	}
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	normalized = bytes.ReplaceAll(normalized, []byte("\r"), []byte("\n"))
	return normalized, nil
}

func ParseMarkdown(data []byte, requireFrontmatter bool) (Markdown, error) {
	normalized, err := NormalizeText(data)
	if err != nil {
		return Markdown{}, err
	}
	if !bytes.HasPrefix(normalized, []byte("---\n")) {
		if requireFrontmatter {
			return Markdown{}, errors.New("Markdown frontmatter is required")
		}
		return Markdown{Normalized: normalized, Body: normalized}, nil
	}
	end := bytes.Index(normalized[4:], []byte("\n---\n"))
	if end < 0 {
		return Markdown{}, errors.New("Markdown frontmatter closing delimiter is missing")
	}
	end += 4
	frontBytes := normalized[4:end]
	bodyStart := end + len("\n---\n")
	if len(bytes.TrimSpace(frontBytes)) == 0 {
		return Markdown{}, errors.New("Markdown frontmatter is empty")
	}
	node, err := ParseStrictYAML(frontBytes)
	if err != nil {
		return Markdown{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	front, err := DecodeStringMap(node)
	if err != nil {
		return Markdown{}, err
	}
	return Markdown{
		Normalized:     normalized,
		Frontmatter:    front,
		FrontNode:      node,
		Body:           normalized[bodyStart:],
		HasFrontmatter: true,
	}, nil
}
