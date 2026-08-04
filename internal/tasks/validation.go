package tasks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxJSONInput = 1 << 20

var (
	identifierPattern = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)
	rolePattern       = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*/[a-z0-9]+(?:[-_][a-z0-9]+)*$`)
	digestPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	forbiddenKeys     = map[string]struct{}{
		"authority": {}, "authority_class": {}, "model": {}, "model_policy": {},
		"capability": {}, "capabilities": {}, "tool": {}, "tools": {},
		"profile": {}, "profile_path": {}, "memory": {}, "memory_path": {},
		"shell": {}, "container": {}, "containers": {}, "k8s": {}, "kubernetes": {},
	}
)

func DecodeCreateRequest(r io.Reader) (CreateRequest, error) {
	var request CreateRequest
	if err := decodeStrict(r, &request, true); err != nil {
		return CreateRequest{}, err
	}
	return request, nil
}

func DecodeAttemptResult(r io.Reader) (AttemptResult, error) {
	var result AttemptResult
	if err := decodeStrict(r, &result, false); err != nil {
		return AttemptResult{}, err
	}
	return result, nil
}

func DecodeRequirementSpec(r io.Reader) (RequirementSpec, error) {
	var spec RequirementSpec
	if err := decodeStrict(r, &spec, false); err != nil {
		return RequirementSpec{}, err
	}
	return spec, nil
}

func decodeStrict(r io.Reader, target any, inspectForbidden bool) error {
	if r == nil {
		return fmt.Errorf("%w: JSON input is required", ErrInvalidInput)
	}
	body, err := io.ReadAll(io.LimitReader(r, maxJSONInput+1))
	if err != nil {
		return fmt.Errorf("%w: read JSON: %v", ErrInvalidInput, err)
	}
	if len(body) == 0 || len(bytes.TrimSpace(body)) == 0 {
		return fmt.Errorf("%w: JSON input is empty", ErrInvalidInput)
	}
	if len(body) > maxJSONInput {
		return fmt.Errorf("%w: JSON input exceeds %d bytes", ErrInvalidInput, maxJSONInput)
	}
	if inspectForbidden {
		var raw any
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if err := decoder.Decode(&raw); err != nil {
			return fmt.Errorf("%w: decode JSON: %v", ErrInvalidInput, err)
		}
		if key := findForbiddenKey(raw); key != "" {
			return fmt.Errorf("%w: %q", ErrForbiddenField, key)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrInvalidInput, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: multiple JSON values are not allowed", ErrInvalidInput)
		}
		return fmt.Errorf("%w: trailing JSON: %v", ErrInvalidInput, err)
	}
	return nil
}

func findForbiddenKey(value any) string {
	switch item := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(item))
		for key := range item {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if _, forbidden := forbiddenKeys[normalized]; forbidden {
				return key
			}
			if nested := findForbiddenKey(item[key]); nested != "" {
				return nested
			}
		}
	case []any:
		for _, child := range item {
			if nested := findForbiddenKey(child); nested != "" {
				return nested
			}
		}
	}
	return ""
}

func NormalizeCreateRequest(request CreateRequest) CreateRequest {
	request.OrganizationID = strings.TrimSpace(request.OrganizationID)
	request.RequestedByRoleID = strings.TrimSpace(request.RequestedByRoleID)
	request.AssignedRoleID = strings.TrimSpace(request.AssignedRoleID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.Title = strings.TrimSpace(request.Title)
	request.Instructions = strings.TrimSpace(request.Instructions)
	request.CorrelationID = strings.TrimSpace(request.CorrelationID)
	request.CausationID = strings.TrimSpace(request.CausationID)
	if request.AcceptanceCriteria == nil {
		request.AcceptanceCriteria = []string{}
	}
	for index := range request.AcceptanceCriteria {
		request.AcceptanceCriteria[index] = strings.TrimSpace(request.AcceptanceCriteria[index])
	}
	for index := range request.Requirements {
		requirement := &request.Requirements[index]
		requirement.Key = strings.TrimSpace(requirement.Key)
		requirement.Description = strings.TrimSpace(requirement.Description)
	}
	return request
}

func ValidateCreateRequest(request CreateRequest) error {
	if request.OrganizationID != "" && !identifierPattern.MatchString(request.OrganizationID) {
		return fmt.Errorf("%w: organization_id is invalid", ErrInvalidInput)
	}
	if request.RequestedByRoleID != "" && !rolePattern.MatchString(request.RequestedByRoleID) {
		return fmt.Errorf("%w: requested_by_role_id is invalid", ErrInvalidInput)
	}
	if !rolePattern.MatchString(request.AssignedRoleID) {
		return fmt.Errorf("%w: assigned_role_id is invalid", ErrInvalidInput)
	}
	if length := len(request.IdempotencyKey); length < 1 || length > 200 {
		return fmt.Errorf("%w: idempotency_key must contain 1 to 200 bytes", ErrInvalidInput)
	}
	if length := len(request.Title); length < 1 || length > 240 {
		return fmt.Errorf("%w: title must contain 1 to 240 bytes", ErrInvalidInput)
	}
	if length := len(request.Instructions); length < 1 || length > 65536 {
		return fmt.Errorf("%w: instructions must contain 1 to 65536 bytes", ErrInvalidInput)
	}
	if request.Priority < -100000 || request.Priority > 100000 {
		return fmt.Errorf("%w: priority is outside the supported range", ErrInvalidInput)
	}
	if request.MaxAttempts < 1 || request.MaxAttempts > 100 {
		return fmt.Errorf("%w: max_attempts must be between 1 and 100", ErrInvalidInput)
	}
	if len(request.CorrelationID) > 200 || len(request.CausationID) > 200 {
		return fmt.Errorf("%w: correlation_id and causation_id must be at most 200 bytes", ErrInvalidInput)
	}
	if len(request.AcceptanceCriteria) > 100 {
		return fmt.Errorf("%w: at most 100 acceptance criteria are allowed", ErrInvalidInput)
	}
	for index, criterion := range request.AcceptanceCriteria {
		if criterion == "" || len(criterion) > 4000 {
			return fmt.Errorf("%w: acceptance_criteria[%d] must contain 1 to 4000 bytes", ErrInvalidInput, index)
		}
	}
	seenDependencies := make(map[int64]struct{}, len(request.Dependencies))
	for _, dependency := range request.Dependencies {
		if dependency <= 0 {
			return fmt.Errorf("%w: dependency IDs must be positive", ErrInvalidInput)
		}
		if _, duplicate := seenDependencies[dependency]; duplicate {
			return fmt.Errorf("%w: dependency %d is duplicated", ErrInvalidInput, dependency)
		}
		seenDependencies[dependency] = struct{}{}
	}
	if len(request.Requirements) > 100 {
		return fmt.Errorf("%w: at most 100 requirements are allowed", ErrInvalidInput)
	}
	seenRequirements := make(map[string]struct{}, len(request.Requirements))
	for _, requirement := range request.Requirements {
		if err := ValidateRequirementSpec(requirement); err != nil {
			return err
		}
		if _, duplicate := seenRequirements[requirement.Key]; duplicate {
			return fmt.Errorf("%w: requirement key %q is duplicated", ErrInvalidInput, requirement.Key)
		}
		seenRequirements[requirement.Key] = struct{}{}
	}
	return nil
}

func ValidateRequirementSpec(spec RequirementSpec) error {
	if len(spec.Key) < 1 || len(spec.Key) > 120 || !identifierPattern.MatchString(spec.Key) {
		return fmt.Errorf("%w: requirement key is invalid", ErrInvalidInput)
	}
	if !spec.Type.Valid() {
		return fmt.Errorf("%w: requirement type %q is invalid", ErrInvalidInput, spec.Type)
	}
	if len(spec.Description) < 1 || len(spec.Description) > 4000 {
		return fmt.Errorf("%w: requirement description must contain 1 to 4000 bytes", ErrInvalidInput)
	}
	return nil
}

func ValidateEvidence(command RecordEvidenceCommand) error {
	if command.TaskID <= 0 {
		return fmt.Errorf("%w: task ID must be positive", ErrInvalidInput)
	}
	if command.RequirementID != nil && *command.RequirementID <= 0 {
		return fmt.Errorf("%w: requirement ID must be positive", ErrInvalidInput)
	}
	if command.Satisfies && command.RequirementID == nil {
		return fmt.Errorf("%w: satisfying evidence requires a requirement ID", ErrInvalidInput)
	}
	if !command.Type.Valid() {
		return fmt.Errorf("%w: evidence type is invalid", ErrInvalidInput)
	}
	if length := len(strings.TrimSpace(command.Reference)); length < 1 || length > 2048 {
		return fmt.Errorf("%w: evidence reference must contain 1 to 2048 bytes", ErrInvalidInput)
	}
	if command.Digest != "" && !digestPattern.MatchString(command.Digest) {
		return fmt.Errorf("%w: evidence digest must be lowercase SHA-256", ErrInvalidInput)
	}
	if length := len(strings.TrimSpace(command.RecordedBy)); length < 1 || length > 200 {
		return fmt.Errorf("%w: recorded_by must contain 1 to 200 bytes", ErrInvalidInput)
	}
	metadata, err := json.Marshal(command.Metadata)
	if err != nil {
		return fmt.Errorf("%w: evidence metadata must be valid JSON: %v", ErrInvalidInput, err)
	}
	if len(metadata) > 65536 {
		return fmt.Errorf("%w: evidence metadata exceeds 65536 bytes", ErrInvalidInput)
	}
	return nil
}

func HashCreateRequest(request CreateRequest) (string, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("hash task request: %w", err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func NormalizeAvailableAt(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC().Round(0)
	return &normalized
}
