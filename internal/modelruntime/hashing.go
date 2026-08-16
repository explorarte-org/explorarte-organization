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

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
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

func invocationRequestHash(c CreateInvocationCommand, revision int64, binding ResolvedBinding, caps []ModelCapability, schema []byte, modelInputDigest string, policyVersionID int64, policyHash string, identityPolicyVersionID int64, identityPolicyHash string, assignment modeldispatch.ResolvedAssignment) (string, error) {
	value := map[string]any{
		"organization_id":                      c.OrganizationID,
		"organization_revision_id":             revision,
		"task_id":                              c.TaskID,
		"attempt_id":                           c.AttemptID,
		"dispatcher_assignment_id":             assignment.Assignment.ID,
		"dispatcher_assignment_hash":           assignment.Assignment.AssignmentHash,
		"execution_principal_id":               assignment.Principal.ID,
		"execution_principal_key":              assignment.Principal.PrincipalKey,
		"dispatch_actor_role_id":               assignment.Principal.DispatchActorRoleID,
		"subject_role_id":                      c.SubjectRoleID,
		"context_snapshot_id":                  c.ContextSnapshotID,
		"model_input_digest":                   modelInputDigest,
		"purpose":                              strings.TrimSpace(c.Purpose),
		"profile_id":                           binding.Profile.ID,
		"profile_version_id":                   binding.Version.ID,
		"provider_id":                          binding.Version.ProviderID,
		"provider_model_id":                    binding.Version.ProviderModelID,
		"required_capabilities":                caps,
		"output_mode":                          c.OutputMode,
		"output_schema":                        json.RawMessage(schema),
		"max_output_tokens":                    c.MaxOutputTokens,
		"temperature":                          c.Temperature,
		"thinking_mode":                        c.ThinkingMode,
		"deadline":                             c.Deadline.UTC().Format(time.RFC3339Nano),
		"model_egress_policy_version_id":       policyVersionID,
		"model_egress_policy_hash":             policyHash,
		"execution_identity_policy_version_id": identityPolicyVersionID,
		"execution_identity_policy_hash":       identityPolicyHash,
	}
	body, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	return SHA256Bytes(body), nil
}

// ActionDigest binds authorization and egress evaluation to the invocation's
// full pinned identity: its request (which already covers the dispatcher
// assignment and execution principal), and its egress policy pin. Changing
// any of those changes the digest.
func ActionDigest(inv Invocation) (string, error) {
	if inv.ModelEgressPolicyVersionID == nil {
		return "", ErrEgressPolicyUnpinned
	}
	if inv.ExecutionIdentityPolicyVersionID == nil || inv.ExecutionIdentityPolicyHash == "" {
		return "", ErrExecutionIdentityUnpinned
	}
	if inv.DispatcherAssignmentID == nil || inv.ExecutionPrincipalID == nil {
		return "", ErrDispatcherAssignmentUnpinned
	}
	value := struct {
		InvocationID            int64  `json:"invocation_id"`
		RequestHash             string `json:"request_hash"`
		DispatcherAssignmentID  int64  `json:"dispatcher_assignment_id"`
		ExecutionPrincipalID    int64  `json:"execution_principal_id"`
		PolicyVersionID         int64  `json:"model_egress_policy_version_id"`
		PolicyHash              string `json:"model_egress_policy_hash"`
		IdentityPolicyVersionID int64  `json:"execution_identity_policy_version_id"`
		IdentityPolicyHash      string `json:"execution_identity_policy_hash"`
	}{inv.ID, inv.RequestHash, *inv.DispatcherAssignmentID, *inv.ExecutionPrincipalID, *inv.ModelEgressPolicyVersionID, inv.ModelEgressPolicyHash, *inv.ExecutionIdentityPolicyVersionID, inv.ExecutionIdentityPolicyHash}
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return SHA256Bytes(body), nil
}
