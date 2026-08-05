package document

import (
	"errors"
	"strings"
	"testing"
)

func TestParseMarkdownStrictFrontmatter(t *testing.T) {
	parsed, err := ParseMarkdown([]byte("---\ndepartamento: ingenieria_ia\nrol: qa\n---\r\n# Body\r\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Frontmatter["rol"] != "qa" || strings.Contains(string(parsed.Normalized), "\r") {
		t.Fatalf("parsed=%+v normalized=%q", parsed.Frontmatter, parsed.Normalized)
	}
}

func TestParseMarkdownRejectsMissingDuplicateAliasInvalidUTF8AndNUL(t *testing.T) {
	tests := map[string][]byte{
		"missing":   []byte("# body"),
		"duplicate": []byte("---\nrol: qa\nrol: frontend\n---\nbody"),
		"alias":     []byte("---\nrol: &x qa\nother: *x\n---\nbody"),
		"nul":       []byte{'-', '-', '-', '\n', 'x', ':', ' ', 0, '\n', '-', '-', '-', '\n'},
		"utf8":      []byte{0xff, 0xfe},
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseMarkdown(data, true)
			if err == nil {
				t.Fatal("expected error")
			}
			var duplicate *DuplicateKeyError
			if name == "duplicate" && !errors.As(err, &duplicate) {
				t.Fatalf("duplicate type=%T %v", err, err)
			}
		})
	}
}

func TestCanonicalYAMLDoesNotDependOnMappingOrder(t *testing.T) {
	left, err := CanonicalYAML([]byte("a: 1\nb: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	right, err := CanonicalYAML([]byte("b: 2\na: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(left) != string(right) {
		t.Fatalf("canonical mismatch\n%x\n%x", left, right)
	}
}
