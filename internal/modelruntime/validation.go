package modelruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
)

var roleIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._/-][a-z0-9]+)*$`)
var toolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func PrepareCreateCommand(c CreateInvocationCommand, now time.Time) (CreateInvocationCommand, []ModelCapability, []byte, error) {
	c.OrganizationID = strings.TrimSpace(c.OrganizationID)
	c.DispatchActorRoleID = strings.TrimSpace(c.DispatchActorRoleID)
	c.SubjectRoleID = strings.TrimSpace(c.SubjectRoleID)
	c.Purpose = strings.TrimSpace(c.Purpose)
	c.IdempotencyKey = strings.TrimSpace(c.IdempotencyKey)
	c.CorrelationID = strings.TrimSpace(c.CorrelationID)
	c.CausationID = strings.TrimSpace(c.CausationID)
	if c.OrganizationID == "" || c.TaskID <= 0 || c.AttemptID <= 0 || c.ContextSnapshotID <= 0 {
		return c, nil, nil, fmt.Errorf("%w: organization, task, attempt and context are required", ErrInvalidRequest)
	}
	if !roleIDPattern.MatchString(c.DispatchActorRoleID) || !roleIDPattern.MatchString(c.SubjectRoleID) {
		return c, nil, nil, fmt.Errorf("%w: invalid role identifier", ErrInvalidRequest)
	}
	if c.DispatchActorRoleID != c.SubjectRoleID {
		return c, nil, nil, fmt.Errorf("%w: cross-role dispatch is not available in branch 09", ErrTaskAttemptRejected)
	}
	if len(c.Purpose) < 1 || len(c.Purpose) > 4000 {
		return c, nil, nil, fmt.Errorf("%w: purpose outside allowed length", ErrInvalidRequest)
	}
	if len(c.IdempotencyKey) < 1 || len(c.IdempotencyKey) > 200 {
		return c, nil, nil, fmt.Errorf("%w: idempotency key outside allowed length", ErrInvalidRequest)
	}
	if c.Deadline.IsZero() || !c.Deadline.After(now) {
		return c, nil, nil, fmt.Errorf("%w: deadline must be in the future", ErrInvalidRequest)
	}
	if c.Deadline.Sub(now) > 24*time.Hour {
		return c, nil, nil, fmt.Errorf("%w: deadline exceeds maximum horizon", ErrInvalidRequest)
	}
	if c.MaxOutputTokens < 1 || c.MaxOutputTokens > 1048576 {
		return c, nil, nil, fmt.Errorf("%w: max output tokens outside allowed range", ErrInvalidRequest)
	}
	if c.Temperature != nil && (*c.Temperature < 0 || *c.Temperature > 2) {
		return c, nil, nil, fmt.Errorf("%w: temperature outside allowed range", ErrInvalidRequest)
	}
	switch c.OutputMode {
	case OutputText, OutputJSON:
	default:
		return c, nil, nil, fmt.Errorf("%w: invalid output mode", ErrInvalidRequest)
	}
	switch c.ThinkingMode {
	case ThinkingDisabled, ThinkingOpaque:
	default:
		return c, nil, nil, fmt.Errorf("%w: invalid thinking mode", ErrInvalidRequest)
	}
	caps := normalizeCapabilities(c.RequiredCapabilities)
	for _, capability := range caps {
		if len(capability) > 160 || !roleIDPattern.MatchString(string(capability)) {
			return c, nil, nil, fmt.Errorf("%w: invalid model capability %q", ErrInvalidRequest, capability)
		}
	}
	schema, err := CanonicalizeRawJSON(c.OutputSchema)
	if err != nil {
		return c, nil, nil, fmt.Errorf("%w: invalid output schema: %v", ErrInvalidRequest, err)
	}
	if len(schema) > 65536 {
		return c, nil, nil, fmt.Errorf("%w: output schema too large", ErrInvalidRequest)
	}
	if c.OutputMode == OutputText && len(schema) > 0 {
		return c, nil, nil, fmt.Errorf("%w: output schema is only valid for JSON mode", ErrInvalidRequest)
	}
	if len(schema) > 0 {
		v, decodeErr := decodeJSONWithNumbers(schema)
		if decodeErr != nil {
			return c, nil, nil, decodeErr
		}
		m, ok := v.(map[string]any)
		if !ok {
			return c, nil, nil, fmt.Errorf("%w: schema must be an object", ErrInvalidRequest)
		}
		if err = validateSchemaDefinition(m, 0); err != nil {
			return c, nil, nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
	}
	c.RequiredCapabilities = caps
	c.OutputSchema = schema
	c.Deadline = c.Deadline.UTC()
	return c, caps, schema, nil
}

func validateTaskAttempt(ref TaskAttemptRef, c CreateInvocationCommand, now time.Time) error {
	if ref.TaskID != c.TaskID || ref.AttemptID != c.AttemptID {
		return fmt.Errorf("%w: task/attempt mismatch", ErrTaskAttemptRejected)
	}
	if ref.OrganizationID != c.OrganizationID {
		return fmt.Errorf("%w: organization mismatch", ErrTaskAttemptRejected)
	}
	if ref.AssignedRoleID != c.SubjectRoleID || ref.AssignedRoleID != c.DispatchActorRoleID {
		return fmt.Errorf("%w: dispatcher and subject must be assigned role", ErrTaskAttemptRejected)
	}
	if ref.TaskStatus != "running" || ref.AttemptStatus != "running" {
		return fmt.Errorf("%w: task and attempt must be running", ErrTaskAttemptRejected)
	}
	if strings.TrimSpace(ref.LeaseHolderID) == "" {
		return fmt.Errorf("%w: active lease holder is required", ErrTaskAttemptRejected)
	}
	if !ref.LeaseExpiresAt.After(now) {
		return fmt.Errorf("%w: task lease expired", ErrTaskAttemptRejected)
	}
	return nil
}
func validateContext(ref ContextSnapshotRef, c CreateInvocationCommand, revision int64) error {
	if ref.ID != c.ContextSnapshotID || ref.Status != "ready" {
		return fmt.Errorf("%w: context snapshot is not ready", ErrContextRejected)
	}
	if ref.OrganizationID != c.OrganizationID || ref.OrganizationRevisionID != revision {
		return fmt.Errorf("%w: context organization revision mismatch", ErrContextRejected)
	}
	if ref.ActorRoleID != c.SubjectRoleID {
		return fmt.Errorf("%w: context subject role mismatch", ErrContextRejected)
	}
	if ref.TaskRef != strconv.FormatInt(c.TaskID, 10) {
		return fmt.Errorf("%w: context task scope mismatch", ErrContextRejected)
	}
	return nil
}
func validateResolvedEgressPolicy(policy modelegress.ResolvedPolicy, organization OrganizationRef) error {
	if policy.Version.ID <= 0 || policy.Version.PolicyVersion <= 0 ||
		policy.Version.Status != "materialized" || policy.Version.OrganizationID != organization.ID ||
		policy.OrganizationRevisionID != organization.RevisionID || policy.DefaultAction != modelegress.EffectDeny ||
		policy.CanonicalHash == "" || policy.Version.CanonicalHash != policy.CanonicalHash ||
		policy.CanonicalHash != organization.ModelEgressPolicyHash {
		return fmt.Errorf("%w: egress policy revision drift", ErrEgressPolicyUnpinned)
	}
	return nil
}

func capabilitiesSatisfy(available, required []ModelCapability) bool {
	set := map[ModelCapability]struct{}{}
	for _, v := range available {
		set[v] = struct{}{}
	}
	for _, v := range required {
		if _, ok := set[v]; !ok {
			return false
		}
	}
	return true
}

func validateSchemaDefinition(schema map[string]any, depth int) error {
	if depth > 20 {
		return fmt.Errorf("schema depth exceeded")
	}
	if raw, ok := schema["type"]; ok {
		t, ok := raw.(string)
		if !ok || !validSchemaType(t) {
			return fmt.Errorf("unsupported schema type")
		}
	}
	if raw, ok := schema["required"]; ok {
		items, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("required must be array")
		}
		seen := map[string]struct{}{}
		for _, item := range items {
			s, ok := item.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return fmt.Errorf("required entries must be strings")
			}
			if _, ok = seen[s]; ok {
				return fmt.Errorf("duplicate required property")
			}
			seen[s] = struct{}{}
		}
	}
	if raw, ok := schema["properties"]; ok {
		props, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("properties must be object")
		}
		for name, v := range props {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("empty property name")
			}
			child, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("property schema must be object")
			}
			if err := validateSchemaDefinition(child, depth+1); err != nil {
				return err
			}
		}
	}
	if raw, ok := schema["items"]; ok {
		child, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("items must be object")
		}
		if err := validateSchemaDefinition(child, depth+1); err != nil {
			return err
		}
	}
	if raw, ok := schema["additionalProperties"]; ok {
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("additionalProperties must be boolean")
		}
	}
	allowed := map[string]struct{}{"type": {}, "required": {}, "properties": {}, "items": {}, "additionalProperties": {}, "enum": {}, "description": {}}
	for k := range schema {
		if _, ok := allowed[k]; !ok {
			return fmt.Errorf("unsupported schema keyword %q", k)
		}
	}
	return nil
}
func validSchemaType(t string) bool {
	switch t {
	case "object", "array", "string", "number", "integer", "boolean", "null":
		return true
	}
	return false
}
func validateAgainstSchema(value any, schema map[string]any, path string) error {
	if raw, ok := schema["type"]; ok {
		if !matchesType(value, raw.(string)) {
			return fmt.Errorf("%s has wrong type", path)
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		body, _ := CanonicalJSON(value)
		found := false
		for _, candidate := range enum {
			other, _ := CanonicalJSON(candidate)
			if bytes.Equal(body, other) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s is not in enum", path)
		}
	}
	obj, object := value.(map[string]any)
	if object {
		if required, ok := schema["required"].([]any); ok {
			for _, item := range required {
				if _, exists := obj[item.(string)]; !exists {
					return fmt.Errorf("%s.%s is required", path, item)
				}
			}
		}
		props, _ := schema["properties"].(map[string]any)
		additional := true
		if flag, ok := schema["additionalProperties"].(bool); ok {
			additional = flag
		}
		for key, v := range obj {
			child, known := props[key]
			if !known {
				if !additional {
					return fmt.Errorf("%s.%s is not allowed", path, key)
				}
				continue
			}
			if err := validateAgainstSchema(v, child.(map[string]any), path+"."+key); err != nil {
				return err
			}
		}
	}
	if arr, ok := value.([]any); ok {
		if child, exists := schema["items"].(map[string]any); exists {
			for i, item := range arr {
				if err := validateAgainstSchema(item, child, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func matchesType(v any, t string) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		switch v.(type) {
		case json.Number, float64:
			return true
		default:
			return false
		}
	case "integer":
		switch number := v.(type) {
		case json.Number:
			rational, ok := new(big.Rat).SetString(number.String())
			return ok && rational.IsInt()
		case float64:
			return number == float64(int64(number))
		default:
			return false
		}
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	}
	return false
}
func sortedCapabilities(values []ModelCapability) []ModelCapability {
	r := append([]ModelCapability(nil), values...)
	sort.Slice(r, func(i, j int) bool { return r[i] < r[j] })
	return r
}
