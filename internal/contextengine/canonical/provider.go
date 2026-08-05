package canonical

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine/document"
	"gopkg.in/yaml.v3"
)

type allowEntry struct {
	Name     string
	Tier     contextengine.AuthorityTier
	Class    contextengine.InstructionClass
	Trust    contextengine.TrustClass
	Data     contextengine.DataClass
	MayGrant bool
}

var allowlist = []allowEntry{
	{Name: "cell-boundaries.yaml", Tier: contextengine.TierImmutableSafety, Class: contextengine.InstructionImmutableConstraint, Trust: contextengine.TrustImmutable, Data: contextengine.DataOrganizational},
	{Name: "decisions-required.yaml", Tier: contextengine.TierOwnerDecisions, Class: contextengine.InstructionOwnerConstraint, Trust: contextengine.TrustAuthoritative, Data: contextengine.DataOrganizational},
	{Name: "organization.yaml", Tier: contextengine.TierCanonicalPolicies, Class: contextengine.InstructionCanonicalPolicy, Trust: contextengine.TrustAuthoritative, Data: contextengine.DataOrganizational},
	{Name: "role-catalog.yaml", Tier: contextengine.TierCanonicalPolicies, Class: contextengine.InstructionCanonicalPolicy, Trust: contextengine.TrustAuthoritative, Data: contextengine.DataOrganizational},
	{Name: "leader-worker-map.yaml", Tier: contextengine.TierCanonicalPolicies, Class: contextengine.InstructionCanonicalPolicy, Trust: contextengine.TrustAuthoritative, Data: contextengine.DataOrganizational},
	{Name: "model-routing.yaml", Tier: contextengine.TierCanonicalPolicies, Class: contextengine.InstructionCanonicalPolicy, Trust: contextengine.TrustAuthoritative, Data: contextengine.DataOrganizational},
	{Name: "capability-matrix.yaml", Tier: contextengine.TierCanonicalPolicies, Class: contextengine.InstructionCanonicalPolicy, Trust: contextengine.TrustAuthoritative, Data: contextengine.DataOrganizational, MayGrant: true},
	{Name: "instruction-precedence.yaml", Tier: contextengine.TierCanonicalPolicies, Class: contextengine.InstructionCanonicalPolicy, Trust: contextengine.TrustAuthoritative, Data: contextengine.DataOrganizational},
	{Name: "memory-policy.yaml", Tier: contextengine.TierCanonicalPolicies, Class: contextengine.InstructionCanonicalPolicy, Trust: contextengine.TrustAuthoritative, Data: contextengine.DataOrganizational},
	{Name: "reasoning-assurance.yaml", Tier: contextengine.TierCanonicalPolicies, Class: contextengine.InstructionCanonicalPolicy, Trust: contextengine.TrustAuthoritative, Data: contextengine.DataOrganizational},
	{Name: "architecture-characteristics.yaml", Tier: contextengine.TierCanonicalPolicies, Class: contextengine.InstructionCanonicalPolicy, Trust: contextengine.TrustAuthoritative, Data: contextengine.DataOrganizational},
}

type Provider struct {
	root     string
	maxBytes int64
}

func New(root string, maxBytes int64) (*Provider, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("canonical directory cannot be empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve canonical directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve canonical directory symlinks: %w", err)
	}
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	return &Provider{root: resolved, maxBytes: maxBytes}, nil
}

func (p *Provider) Load(ctx context.Context) (contextengine.CanonicalBundle, error) {
	if err := ctx.Err(); err != nil {
		return contextengine.CanonicalBundle{}, err
	}
	sources := make([]contextengine.CanonicalSource, 0, len(allowlist))
	semanticByName := make(map[string]string, len(allowlist))
	for _, entry := range allowlist {
		if err := ctx.Err(); err != nil {
			return contextengine.CanonicalBundle{}, err
		}
		raw, err := p.read(entry.Name)
		if err != nil {
			return contextengine.CanonicalBundle{}, err
		}
		canonicalBytes, err := document.CanonicalYAML(raw)
		if err != nil {
			return contextengine.CanonicalBundle{}, contextengine.Reject(contextengine.ReasonCanonicalBundleDrift, entry.Name, err.Error())
		}
		semanticHash := contextengine.DigestCanonicalBytes(canonicalBytes)
		normalized, err := document.NormalizeText(raw)
		if err != nil {
			return contextengine.CanonicalBundle{}, contextengine.Reject(contextengine.ReasonCanonicalBundleDrift, entry.Name, err.Error())
		}
		version, err := schemaVersion(raw)
		if err != nil {
			return contextengine.CanonicalBundle{}, contextengine.Reject(contextengine.ReasonCanonicalBundleDrift, entry.Name, err.Error())
		}
		sources = append(sources, contextengine.CanonicalSource{
			LogicalName:      "docs/canonical/" + entry.Name,
			Version:          version,
			Tier:             entry.Tier,
			InstructionClass: entry.Class,
			TrustClass:       entry.Trust,
			DataClass:        entry.Data,
			MayGrant:         entry.MayGrant,
			Content:          normalized,
			ContentHash:      contextengine.DigestCanonicalBytes(normalized),
			SemanticHash:     semanticHash,
		})
		semanticByName[entry.Name] = semanticHash
		if entry.Name == "instruction-precedence.yaml" {
			if err := validatePrecedence(raw); err != nil {
				return contextengine.CanonicalBundle{}, err
			}
		}
	}
	bundleHash := digestBundle(sources)
	return contextengine.CanonicalBundle{PrecedenceHash: semanticByName["instruction-precedence.yaml"], BundleHash: bundleHash, Sources: sources}, nil
}

func (p *Provider) Validate(ctx context.Context, precedenceHash, bundleHash string) error {
	bundle, err := p.Load(ctx)
	if err != nil {
		return err
	}
	if bundle.PrecedenceHash != precedenceHash {
		return contextengine.Reject(contextengine.ReasonPrecedenceHashMismatch, "instruction-precedence.yaml", "precedence hash changed")
	}
	if bundle.BundleHash != bundleHash {
		return contextengine.Reject(contextengine.ReasonCanonicalBundleDrift, "docs/canonical", "canonical bundle changed")
	}
	return nil
}

func (p *Provider) read(name string) ([]byte, error) {
	path := filepath.Join(p.root, name)
	rel, err := filepath.Rel(p.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, contextengine.Reject(contextengine.ReasonSourcePathEscape, name, "canonical path escapes root")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat canonical document %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("canonical document %s is not regular", name)
	}
	if info.Size() > p.maxBytes {
		return nil, contextengine.Reject(contextengine.ReasonSourceTooLarge, name, "canonical document too large")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read canonical document %s: %w", name, err)
	}
	return raw, nil
}

func schemaVersion(raw []byte) (string, error) {
	node, err := document.ParseStrictYAML(raw)
	if err != nil {
		return "", err
	}
	if node.Kind != yaml.MappingNode {
		return "", errors.New("canonical document root must be a mapping")
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == "schema_version" {
			version := strings.TrimSpace(node.Content[i+1].Value)
			if version == "" {
				return "", errors.New("schema_version is empty")
			}
			return version, nil
		}
	}
	// decisions-required is owner material and predates schema_version. Preserve an explicit stable legacy version.
	return "legacy-v1", nil
}

func validatePrecedence(raw []byte) error {
	node, err := document.ParseStrictYAML(raw)
	if err != nil {
		return contextengine.Reject(contextengine.ReasonPrecedenceInvalid, "instruction-precedence.yaml", err.Error())
	}
	var definition contextengine.PrecedenceDefinition
	if err = node.Decode(&definition); err != nil {
		return contextengine.Reject(contextengine.ReasonPrecedenceInvalid, "instruction-precedence.yaml", err.Error())
	}
	return contextengine.ValidatePrecedence(definition)
}

func digestBundle(sources []contextengine.CanonicalSource) string {
	values := append([]contextengine.CanonicalSource(nil), sources...)
	sort.Slice(values, func(i, j int) bool { return values[i].LogicalName < values[j].LogicalName })
	var out bytes.Buffer
	for _, source := range values {
		for _, value := range []string{source.LogicalName, source.Version, source.SemanticHash, string(source.Tier), string(source.InstructionClass), string(source.TrustClass), string(source.DataClass), strconv.FormatBool(source.MayGrant)} {
			out.WriteString(strconv.Itoa(len(value)))
			out.WriteByte(':')
			out.WriteString(value)
			out.WriteByte(';')
		}
	}
	sum := sha256.Sum256(out.Bytes())
	return hex.EncodeToString(sum[:])
}
