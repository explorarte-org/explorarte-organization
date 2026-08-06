package modelidentity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	PolicyFileName    = "model-execution-identity-policy.yaml"
	maxPolicyFileSize = 1 << 20
	maxPolicyDepth    = 24
	maxPolicyNodes    = 2000
)

var identifierPattern = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*$`)

type policyDocument struct {
	SchemaVersion       string    `yaml:"schema_version" json:"schema_version"`
	DocumentStatus      string    `yaml:"document_status" json:"document_status"`
	PolicyID            string    `yaml:"policy_id" json:"policy_id"`
	PolicyVersion       int       `yaml:"policy_version" json:"policy_version"`
	DefaultAction       string    `yaml:"default_action" json:"default_action"`
	Algorithm           Algorithm `yaml:"algorithm" json:"algorithm"`
	ChallengeTTLSeconds int       `yaml:"challenge_ttl_seconds" json:"challenge_ttl_seconds"`
	ClockSkewSeconds    int       `yaml:"clock_skew_seconds" json:"clock_skew_seconds"`
}

func LoadCanonicalPolicy(canonicalDir string) (CanonicalPolicy, error) {
	path := filepath.Join(strings.TrimSpace(canonicalDir), PolicyFileName)
	info, err := os.Stat(path)
	if err != nil {
		return CanonicalPolicy{}, fmt.Errorf("%w: %v", ErrInvalidPolicy, err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxPolicyFileSize {
		return CanonicalPolicy{}, fmt.Errorf("%w: policy file is not a bounded regular file", ErrInvalidPolicy)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return CanonicalPolicy{}, fmt.Errorf("%w: %v", ErrInvalidPolicy, err)
	}
	if err = validateYAML(body); err != nil {
		return CanonicalPolicy{}, fmt.Errorf("%w: %v", ErrInvalidPolicy, err)
	}
	var doc policyDocument
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err = decoder.Decode(&doc); err != nil {
		return CanonicalPolicy{}, fmt.Errorf("%w: %v", ErrInvalidPolicy, err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return CanonicalPolicy{}, fmt.Errorf("%w: multiple YAML documents are not allowed", ErrInvalidPolicy)
	}
	doc.SchemaVersion = strings.TrimSpace(doc.SchemaVersion)
	doc.DocumentStatus = strings.TrimSpace(doc.DocumentStatus)
	doc.PolicyID = strings.TrimSpace(doc.PolicyID)
	doc.DefaultAction = strings.TrimSpace(doc.DefaultAction)
	if doc.SchemaVersion != "1" || doc.DocumentStatus != "active" || !identifierPattern.MatchString(doc.PolicyID) || doc.PolicyVersion < 1 {
		return CanonicalPolicy{}, fmt.Errorf("%w: invalid metadata", ErrInvalidPolicy)
	}
	if doc.DefaultAction != "deny" || doc.Algorithm != AlgorithmEd25519 {
		return CanonicalPolicy{}, fmt.Errorf("%w: policy must be default-deny and Ed25519-only", ErrInvalidPolicy)
	}
	if doc.ChallengeTTLSeconds < 30 || doc.ChallengeTTLSeconds > 600 || doc.ClockSkewSeconds < 0 || doc.ClockSkewSeconds > 60 {
		return CanonicalPolicy{}, fmt.Errorf("%w: TTL or clock skew outside allowed range", ErrInvalidPolicy)
	}
	canonical, err := json.Marshal(doc)
	if err != nil {
		return CanonicalPolicy{}, err
	}
	sum := sha256.Sum256(canonical)
	return CanonicalPolicy{
		SchemaVersion: doc.SchemaVersion, DocumentStatus: doc.DocumentStatus,
		PolicyID: doc.PolicyID, PolicyVersion: doc.PolicyVersion, DefaultAction: doc.DefaultAction,
		Algorithm: doc.Algorithm, ChallengeTTLSeconds: doc.ChallengeTTLSeconds,
		ClockSkewSeconds: doc.ClockSkewSeconds, CanonicalHash: hex.EncodeToString(sum[:]), Path: path,
	}, nil
}

func validateYAML(body []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	var root yaml.Node
	if err := decoder.Decode(&root); err != nil {
		return err
	}
	count := 0
	return validateNode(&root, 0, &count)
}

func validateNode(node *yaml.Node, depth int, count *int) error {
	*count = *count + 1
	if *count > maxPolicyNodes {
		return errors.New("YAML node limit exceeded")
	}
	if depth > maxPolicyDepth {
		return errors.New("YAML depth limit exceeded")
	}
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return errors.New("YAML aliases and anchors are not allowed")
	}
	if node.Kind == yaml.MappingNode {
		seen := map[string]struct{}{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Value == "<<" {
				return errors.New("YAML merge keys are not allowed")
			}
			if _, ok := seen[key.Value]; ok {
				return fmt.Errorf("duplicate YAML key %q", key.Value)
			}
			seen[key.Value] = struct{}{}
		}
	}
	for _, child := range node.Content {
		if err := validateNode(child, depth+1, count); err != nil {
			return err
		}
	}
	return nil
}
