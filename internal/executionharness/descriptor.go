package executionharness

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/contentpolicy"
)

var descriptorDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// BuildRunDescriptor derives the durable, secret-free descriptor from the
// exact RunSpec that validateSpec freezes. It intentionally retains the
// legacy IdentityDigest for replay compatibility while exposing the fields
// MemoryOS needs for reconstruction.
func BuildRunDescriptor(spec RunSpec) (RunDescriptor, error) {
	tools, identityDigest, err := validateSpec(spec)
	if err != nil {
		return RunDescriptor{}, err
	}
	refs, err := frozenToolRefs(tools)
	if err != nil {
		return RunDescriptor{}, fmt.Errorf("%w: freeze tool identities: %v", ErrInvalidRun, err)
	}
	descriptor := RunDescriptor{
		RunID:                spec.Identity.RunID,
		OrganizationID:       spec.Identity.OrganizationID,
		TaskID:               spec.Identity.TaskID,
		AttemptID:            spec.Identity.AttemptID,
		RoleID:               spec.Identity.RoleID,
		ExecutionPrincipalID: spec.Identity.ExecutionPrincipalID,
		ContextID:            spec.Context.ID,
		ContextVersion:       spec.Context.Version,
		ContextDigest:        spec.Context.Digest,
		ExecutionProfileID:   spec.Policy.ExecutionProfileID,
		ModelPolicyRef:       spec.Policy.ModelPolicyRef,
		BuildRef:             spec.Policy.BuildRef,
		MaxTurns:             spec.Policy.MaxTurns,
		MaxToolCalls:         spec.Policy.MaxToolCalls,
		FrozenTools:          refs,
		IdentityDigest:       identityDigest,
	}
	if err := descriptor.Validate(); err != nil {
		return RunDescriptor{}, err
	}
	return descriptor, nil
}

// DescriptorFromSpec is a descriptive alias for callers that prefer the
// projection terminology used by MemoryOS.
func DescriptorFromSpec(spec RunSpec) (RunDescriptor, error) {
	return BuildRunDescriptor(spec)
}

// Validate checks structural and metadata safety invariants without requiring
// the context body or any provider/runtime state. Errors never include the
// value of a metadata field, because a field may contain sensitive material.
func (d RunDescriptor) Validate() error {
	if err := validateDescriptorText(d.RunID, true, 200); err != nil {
		return fmt.Errorf("%w: run id", ErrInvalidRun)
	}
	if err := validateDescriptorText(d.OrganizationID, true, 200); err != nil {
		return fmt.Errorf("%w: organization id", ErrInvalidRun)
	}
	if d.TaskID <= 0 || d.AttemptID <= 0 {
		return fmt.Errorf("%w: descriptor task/attempt binding", ErrInvalidRun)
	}
	if err := validateDescriptorText(d.RoleID, true, 240); err != nil {
		return fmt.Errorf("%w: role id", ErrInvalidRun)
	}
	if err := validateDescriptorText(d.ExecutionPrincipalID, true, 240); err != nil {
		return fmt.Errorf("%w: execution principal id", ErrInvalidRun)
	}
	if err := validateDescriptorText(d.ContextID, true, 240); err != nil {
		return fmt.Errorf("%w: context id", ErrInvalidRun)
	}
	if err := validateDescriptorText(d.ContextVersion, true, 240); err != nil {
		return fmt.Errorf("%w: context version", ErrInvalidRun)
	}
	if err := validateDescriptorText(d.ExecutionProfileID, true, 240); err != nil {
		return fmt.Errorf("%w: execution profile", ErrInvalidRun)
	}
	if err := validateDescriptorText(d.ModelPolicyRef, true, 240); err != nil {
		return fmt.Errorf("%w: model policy", ErrInvalidRun)
	}
	if err := validateDescriptorText(d.BuildRef, false, 240); err != nil {
		return fmt.Errorf("%w: build ref", ErrInvalidRun)
	}
	for name, digest := range map[string]string{
		"context digest": d.ContextDigest, "identity digest": d.IdentityDigest,
	} {
		if !descriptorDigestPattern.MatchString(digest) {
			return fmt.Errorf("%w: invalid %s", ErrInvalidRun, name)
		}
	}
	if d.MaxTurns <= 0 || d.MaxToolCalls < 0 {
		return fmt.Errorf("%w: invalid descriptor limits", ErrInvalidRun)
	}
	seen := make(map[string]struct{}, len(d.FrozenTools))
	for _, tool := range d.FrozenTools {
		if err := validateDescriptorText(tool.Name, true, 128); err != nil || !toolNamePattern.MatchString(tool.Name) {
			return fmt.Errorf("%w: invalid frozen tool name", ErrInvalidRun)
		}
		if _, exists := seen[tool.Name]; exists {
			return fmt.Errorf("%w: duplicate frozen tool", ErrInvalidRun)
		}
		seen[tool.Name] = struct{}{}
		if !descriptorDigestPattern.MatchString(tool.DefinitionDigest) {
			return fmt.Errorf("%w: invalid frozen tool digest", ErrInvalidRun)
		}
	}
	return nil
}

func validateDescriptorText(value string, required bool, max int) error {
	trimmed := strings.TrimSpace(value)
	if (required && trimmed == "") || len(value) > max || strings.IndexByte(value, 0) >= 0 {
		return ErrInvalidRun
	}
	if assessment := contentpolicy.Analyze(value); assessment.HasCredentials() {
		return ErrInvalidRun
	}
	return nil
}

// CanonicalBytes returns the deterministic descriptor representation used for
// idempotency. Frozen tools are sorted in a copy, so reordering durable input
// cannot change the resulting bytes or digest.
func (d RunDescriptor) CanonicalBytes() ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	copyDescriptor := d
	copyDescriptor.FrozenTools = append([]FrozenToolRef(nil), d.FrozenTools...)
	if copyDescriptor.FrozenTools == nil {
		copyDescriptor.FrozenTools = []FrozenToolRef{}
	}
	sort.Slice(copyDescriptor.FrozenTools, func(i, j int) bool {
		return copyDescriptor.FrozenTools[i].Name < copyDescriptor.FrozenTools[j].Name
	})
	return json.Marshal(copyDescriptor)
}

// CanonicalDigest returns the SHA-256 digest of CanonicalBytes.
func (d RunDescriptor) CanonicalDigest() (string, error) {
	body, err := d.CanonicalBytes()
	if err != nil {
		return "", err
	}
	return sha256Bytes(body), nil
}

// DescriptorDigest is a function form of RunDescriptor.CanonicalDigest for
// adapters that do not need method syntax.
func DescriptorDigest(d RunDescriptor) (string, error) {
	return d.CanonicalDigest()
}

// ParseDescriptorDigest validates and decodes a canonical hex digest. It is
// useful to adapters that need to compare a stored digest without persisting
// descriptor bytes.
func ParseDescriptorDigest(value string) ([]byte, error) {
	if !descriptorDigestPattern.MatchString(value) {
		return nil, fmt.Errorf("%w: invalid descriptor digest", ErrRunDescriptorCorrupt)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid descriptor digest", ErrRunDescriptorCorrupt)
	}
	return decoded, nil
}
