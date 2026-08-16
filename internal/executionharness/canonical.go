package executionharness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

var (
	toolNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
	toolCallIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
)

func sha256Bytes(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func canonicalJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, fmt.Errorf("trailing JSON value")
	} else if err != io.EOF {
		return nil, err
	}
	return json.Marshal(value)
}

func normalizeTools(tools []ToolDefinition) ([]ToolDefinition, error) {
	normalized := make([]ToolDefinition, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for i, tool := range tools {
		tool.Name = strings.TrimSpace(tool.Name)
		if !toolNamePattern.MatchString(tool.Name) {
			return nil, fmt.Errorf("%w: invalid tool name", ErrInvalidRun)
		}
		if _, exists := seen[tool.Name]; exists {
			return nil, fmt.Errorf("%w: duplicate tool %s", ErrInvalidRun, tool.Name)
		}
		seen[tool.Name] = struct{}{}
		schema, err := canonicalJSON(tool.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("%w: tool %s input schema: %v", ErrInvalidRun, tool.Name, err)
		}
		tool.InputSchema = schema
		normalized[i] = tool
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Name < normalized[j].Name })
	return normalized, nil
}

func validateSpec(spec RunSpec) ([]ToolDefinition, string, error) {
	i := spec.Identity
	if strings.TrimSpace(i.RunID) == "" || strings.TrimSpace(i.OrganizationID) == "" || i.TaskID <= 0 ||
		i.AttemptID <= 0 || strings.TrimSpace(i.RoleID) == "" || strings.TrimSpace(i.ExecutionPrincipalID) == "" ||
		strings.TrimSpace(i.CorrelationID) == "" || strings.TrimSpace(spec.LeaseToken) == "" {
		return nil, "", fmt.Errorf("%w: incomplete run or workflow binding", ErrInvalidRun)
	}
	if spec.Policy.MaxTurns <= 0 || spec.Policy.MaxToolCalls < 0 ||
		strings.TrimSpace(spec.Policy.ExecutionProfileID) == "" || strings.TrimSpace(spec.Policy.ModelPolicyRef) == "" {
		return nil, "", fmt.Errorf("%w: invalid run policy", ErrInvalidRun)
	}
	if strings.TrimSpace(spec.Context.ID) == "" || strings.TrimSpace(spec.Context.Version) == "" ||
		len(spec.Context.Digest) != 64 || sha256Bytes([]byte(spec.Context.Content)) != spec.Context.Digest {
		return nil, "", fmt.Errorf("%w: context identity or digest mismatch", ErrInvalidRun)
	}
	tools, err := normalizeTools(spec.Tools)
	if err != nil {
		return nil, "", err
	}
	frozen := struct {
		Identity       RunIdentity      `json:"identity"`
		LeaseTokenHash string           `json:"lease_token_hash"`
		ContextID      string           `json:"context_id"`
		ContextVersion string           `json:"context_version"`
		ContextDigest  string           `json:"context_digest"`
		Tools          []ToolDefinition `json:"tools"`
		Policy         RunPolicy        `json:"policy"`
	}{i, sha256Bytes([]byte(spec.LeaseToken)), spec.Context.ID, spec.Context.Version, spec.Context.Digest, tools, spec.Policy}
	body, err := json.Marshal(frozen)
	if err != nil {
		return nil, "", err
	}
	return tools, sha256Bytes(body), nil
}
