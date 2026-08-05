package document

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

const (
	defaultMaxYAMLDepth = 64
	defaultMaxYAMLNodes = 10000
)

func ParseStrictYAML(data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode YAML: %w", err)
	}
	if len(root.Content) == 0 {
		return nil, errors.New("YAML document is empty")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err == nil && len(extra.Content) > 0 {
		return nil, errors.New("multiple YAML documents are not allowed")
	}
	count := 0
	if err := validateNode(root.Content[0], 1, &count); err != nil {
		return nil, err
	}
	return root.Content[0], nil
}

func validateNode(node *yaml.Node, depth int, count *int) error {
	if node == nil {
		return errors.New("nil YAML node")
	}
	*count = *count + 1
	if *count > defaultMaxYAMLNodes {
		return fmt.Errorf("YAML exceeds %d nodes", defaultMaxYAMLNodes)
	}
	if depth > defaultMaxYAMLDepth {
		return fmt.Errorf("YAML exceeds depth %d", defaultMaxYAMLDepth)
	}
	if node.Kind == yaml.AliasNode || node.Alias != nil || node.Anchor != "" {
		return fmt.Errorf("YAML aliases and anchors are not allowed at line %d", node.Line)
	}
	if node.Kind == yaml.ScalarNode {
		switch node.Tag {
		case "", "!", "!!str", "!!bool", "!!int", "!!float", "!!null", "!!timestamp":
		default:
			return fmt.Errorf("unsupported YAML tag %q at line %d", node.Tag, node.Line)
		}
	}
	if node.Kind == yaml.MappingNode {
		if len(node.Content)%2 != 0 {
			return fmt.Errorf("invalid YAML mapping at line %d", node.Line)
		}
		seen := make(map[string]int, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || (key.Tag != "" && key.Tag != "!!str") {
				return fmt.Errorf("YAML mapping keys must be strings at line %d", key.Line)
			}
			if key.Value == "<<" {
				return fmt.Errorf("YAML merge keys are not allowed at line %d", key.Line)
			}
			if first, exists := seen[key.Value]; exists {
				return &DuplicateKeyError{Key: key.Value, FirstLine: first, Line: key.Line}
			}
			seen[key.Value] = key.Line
		}
	}
	for _, child := range node.Content {
		if err := validateNode(child, depth+1, count); err != nil {
			return err
		}
	}
	return nil
}

type DuplicateKeyError struct {
	Key       string
	FirstLine int
	Line      int
}

func (e *DuplicateKeyError) Error() string {
	return fmt.Sprintf("duplicate YAML key %q at line %d (first at line %d)", e.Key, e.Line, e.FirstLine)
}

func DecodeStringMap(node *yaml.Node) (map[string]any, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, errors.New("frontmatter must be a YAML mapping")
	}
	var value map[string]any
	if err := node.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode YAML mapping: %w", err)
	}
	if value == nil {
		value = map[string]any{}
	}
	return value, nil
}

func CanonicalYAML(data []byte) ([]byte, error) {
	node, err := ParseStrictYAML(data)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := encodeCanonical(&out, node); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func encodeCanonical(out *bytes.Buffer, node *yaml.Node) error {
	switch node.Kind {
	case yaml.MappingNode:
		type pair struct {
			key   string
			value *yaml.Node
		}
		pairs := make([]pair, 0, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			pairs = append(pairs, pair{key: node.Content[i].Value, value: node.Content[i+1]})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].key < pairs[j].key })
		out.WriteString("m{")
		for _, item := range pairs {
			writeLengthPrefixed(out, []byte(item.key))
			if err := encodeCanonical(out, item.value); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	case yaml.SequenceNode:
		out.WriteString("s[")
		for _, child := range node.Content {
			if err := encodeCanonical(out, child); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case yaml.ScalarNode:
		out.WriteString("v(")
		writeLengthPrefixed(out, []byte(normalizeTag(node.Tag)))
		writeLengthPrefixed(out, []byte(node.Value))
		out.WriteByte(')')
	default:
		return fmt.Errorf("unsupported YAML node kind %d", node.Kind)
	}
	return nil
}

func normalizeTag(tag string) string {
	if tag == "" || tag == "!" {
		return "!!str"
	}
	return tag
}

func writeLengthPrefixed(out *bytes.Buffer, value []byte) {
	out.WriteString(strconv.Itoa(len(value)))
	out.WriteByte(':')
	out.Write(value)
	out.WriteByte(';')
}
