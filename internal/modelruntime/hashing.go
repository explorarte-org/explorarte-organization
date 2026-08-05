package modelruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

func SHA256Bytes(body []byte) string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }
func CanonicalJSON(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var decoded any
	if err = decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return encodeCanonical(decoded)
}
func encodeCanonical(value any) ([]byte, error) {
	switch typed := value.(type) {
	case nil, bool, string, float64, json.Number:
		return json.Marshal(typed)
	case []any:
		var b bytes.Buffer
		b.WriteByte('[')
		for i, v := range typed {
			if i > 0 {
				b.WriteByte(',')
			}
			p, e := encodeCanonical(v)
			if e != nil {
				return nil, e
			}
			b.Write(p)
		}
		b.WriteByte(']')
		return b.Bytes(), nil
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for k := range typed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b bytes.Buffer
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			b.Write(kb)
			b.WriteByte(':')
			p, e := encodeCanonical(typed[k])
			if e != nil {
				return nil, e
			}
			b.Write(p)
		}
		b.WriteByte('}')
		return b.Bytes(), nil
	default:
		return nil, fmt.Errorf("unsupported canonical JSON type %T", value)
	}
}
func CanonicalizeRawJSON(raw json.RawMessage) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var trailing any
	if err := dec.Decode(&trailing); err == nil {
		return nil, fmt.Errorf("trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return nil, err
	}
	return canonicalNumberJSON(v)
}
func canonicalNumberJSON(v any) ([]byte, error) {
	switch x := v.(type) {
	case json.Number:
		// encoding/json already validated the JSON number syntax. Preserve the
		// exact lexeme so large, valid numbers do not lose precision.
		return []byte(x.String()), nil
	case []any:
		var b bytes.Buffer
		b.WriteByte('[')
		for i, z := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			p, e := canonicalNumberJSON(z)
			if e != nil {
				return nil, e
			}
			b.Write(p)
		}
		b.WriteByte(']')
		return b.Bytes(), nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b bytes.Buffer
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			b.Write(kb)
			b.WriteByte(':')
			p, e := canonicalNumberJSON(x[k])
			if e != nil {
				return nil, e
			}
			b.Write(p)
		}
		b.WriteByte('}')
		return b.Bytes(), nil
	default:
		return json.Marshal(x)
	}
}

func invocationRequestHash(c CreateInvocationCommand, revision int64, binding ResolvedBinding, caps []ModelCapability, schema []byte) (string, error) {
	value := map[string]any{"organization_id": c.OrganizationID, "organization_revision_id": revision, "task_id": c.TaskID, "attempt_id": c.AttemptID, "dispatch_actor_role_id": c.DispatchActorRoleID, "subject_role_id": c.SubjectRoleID, "context_snapshot_id": c.ContextSnapshotID, "purpose": strings.TrimSpace(c.Purpose), "profile_id": binding.Profile.ID, "profile_version_id": binding.Version.ID, "provider_id": binding.Version.ProviderID, "provider_model_id": binding.Version.ProviderModelID, "required_capabilities": caps, "output_mode": c.OutputMode, "output_schema": json.RawMessage(schema), "max_output_tokens": c.MaxOutputTokens, "temperature": c.Temperature, "thinking_mode": c.ThinkingMode, "deadline": c.Deadline.UTC().Format(time.RFC3339Nano)}
	body, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	return SHA256Bytes(body), nil
}
func ActionDigest(inv Invocation) (string, error) {
	value := map[string]any{"invocation_id": inv.ID, "organization_id": inv.OrganizationID, "organization_revision_id": inv.OrganizationRevisionID, "task_id": inv.TaskID, "attempt_id": inv.AttemptID, "dispatch_actor_role_id": inv.DispatchActorRoleID, "subject_role_id": inv.SubjectRoleID, "context_snapshot_id": inv.ContextSnapshotID, "model_profile_id": inv.ModelProfileID, "model_profile_version_id": inv.ModelProfileVersionID, "provider_id": inv.ProviderID, "provider_model_id": inv.ProviderModelID, "required_capabilities": inv.RequiredCapabilities, "output_mode": inv.OutputMode, "output_schema": inv.OutputSchema, "max_output_tokens": inv.MaxOutputTokens, "temperature": inv.Temperature, "thinking_mode": inv.ThinkingMode, "deadline": inv.Deadline.UTC().Format(time.RFC3339Nano)}
	body, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	return SHA256Bytes(body), nil
}
