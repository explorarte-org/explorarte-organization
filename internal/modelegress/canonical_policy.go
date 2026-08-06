package modelegress

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
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	PolicyFileName    = "model-egress-policy.yaml"
	maxPolicyFileSize = 1 << 20
	maxPolicyDepth    = 32
	maxPolicyNodes    = 10000
)

var canonicalIdentifierPattern = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*$`)

type policyDocument struct {
	SchemaVersion  string               `yaml:"schema_version" json:"schema_version"`
	DocumentStatus string               `yaml:"document_status" json:"document_status"`
	PolicyID       string               `yaml:"policy_id" json:"policy_id"`
	PolicyVersion  int                  `yaml:"policy_version" json:"policy_version"`
	DefaultAction  Effect               `yaml:"default_action" json:"default_action"`
	HardDenies     []HardDeny           `yaml:"hard_denies" json:"hard_denies"`
	Rules          []policyRuleDocument `yaml:"rules" json:"rules"`
}

type policyRuleDocument struct {
	ProviderID         string             `yaml:"provider_id" json:"provider_id"`
	DataClassification DataClassification `yaml:"data_classification" json:"data_classification"`
	Effect             Effect             `yaml:"effect" json:"effect"`
	ReasonCode         string             `yaml:"reason_code" json:"reason_code"`
}

type LoadOptions struct {
	KnownProviders      []string
	AllowExplicitAllows bool
}

func LoadCanonicalPolicy(canonicalDir string, options LoadOptions) (CanonicalPolicy, error) {
	canonicalDir = strings.TrimSpace(canonicalDir)
	if canonicalDir == "" {
		return CanonicalPolicy{}, fmt.Errorf("%w: canonical directory is empty", ErrInvalidPolicy)
	}
	path := filepath.Join(canonicalDir, PolicyFileName)
	info, err := os.Lstat(path)
	if err != nil {
		return CanonicalPolicy{}, fmt.Errorf("read model egress policy: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxPolicyFileSize {
		return CanonicalPolicy{}, fmt.Errorf("%w: policy file is not a valid regular file", ErrInvalidPolicy)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return CanonicalPolicy{}, fmt.Errorf("read model egress policy: %w", err)
	}
	body = bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
	body = bytes.ReplaceAll(body, []byte("\r"), []byte("\n"))
	var document policyDocument
	if err = decodeStrictPolicyYAML(body, &document); err != nil {
		return CanonicalPolicy{}, fmt.Errorf("%w: %v", ErrInvalidPolicy, err)
	}
	normalizePolicyDocument(&document)
	if err = validatePolicyDocument(document, options); err != nil {
		return CanonicalPolicy{}, err
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return CanonicalPolicy{}, fmt.Errorf("canonicalize model egress policy: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return CanonicalPolicy{
		SchemaVersion: document.SchemaVersion, DocumentStatus: document.DocumentStatus,
		PolicyID: document.PolicyID, PolicyVersion: document.PolicyVersion,
		DefaultAction: document.DefaultAction, HardDenies: document.HardDenies,
		Rules: policyRulesToDomain(document.Rules), CanonicalHash: hex.EncodeToString(digest[:]), Path: path,
	}, nil
}

func decodeStrictPolicyYAML(body []byte, target *policyDocument) error {
	if target == nil {
		return errors.New("policy target is nil")
	}
	if len(body) == 0 {
		return errors.New("document is empty")
	}
	if bytes.Contains(body, []byte("\t")) {
		return errors.New("tabs are not allowed in canonical YAML")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	var root yaml.Node
	if err := decoder.Decode(&root); err != nil {
		return err
	}
	count := 0
	if err := validatePolicyYAMLNode(&root, 0, &count); err != nil {
		return err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple YAML documents are not allowed")
		}
		return err
	}
	strict := yaml.NewDecoder(bytes.NewReader(body))
	strict.KnownFields(true)
	if err := strict.Decode(target); err != nil {
		return err
	}
	return nil
}

func validatePolicyYAMLNode(node *yaml.Node, depth int, count *int) error {
	if node == nil {
		return nil
	}
	*count = *count + 1
	if *count > maxPolicyNodes {
		return errors.New("YAML document exceeds node limit")
	}
	if depth > maxPolicyDepth {
		return errors.New("YAML document exceeds maximum depth")
	}
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return errors.New("YAML anchors and aliases are not allowed")
	}
	if node.Style&yaml.FlowStyle != 0 {
		return errors.New("YAML flow collections are not allowed")
	}
	if node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return errors.New("YAML block scalars are not allowed")
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			if index+1 >= len(node.Content) {
				return errors.New("invalid YAML mapping")
			}
			key := node.Content[index].Value
			if key == "<<" {
				return errors.New("YAML merge keys are not allowed")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate YAML key %q", key)
			}
			seen[key] = struct{}{}
		}
	}
	for _, child := range node.Content {
		if err := validatePolicyYAMLNode(child, depth+1, count); err != nil {
			return err
		}
	}
	return nil
}

func policyRulesToDomain(values []policyRuleDocument) []Rule {
	result := make([]Rule, 0, len(values))
	for _, value := range values {
		result = append(result, Rule{ProviderID: value.ProviderID, DataClassification: value.DataClassification, Effect: value.Effect, ReasonCode: value.ReasonCode})
	}
	return result
}

func normalizePolicyDocument(document *policyDocument) {
	document.SchemaVersion = strings.TrimSpace(document.SchemaVersion)
	document.DocumentStatus = strings.TrimSpace(document.DocumentStatus)
	document.PolicyID = strings.TrimSpace(document.PolicyID)
	for index := range document.HardDenies {
		document.HardDenies[index].DataClassification = DataClassification(strings.TrimSpace(string(document.HardDenies[index].DataClassification)))
		document.HardDenies[index].ReasonCode = strings.TrimSpace(document.HardDenies[index].ReasonCode)
	}
	for index := range document.Rules {
		document.Rules[index].ProviderID = strings.TrimSpace(document.Rules[index].ProviderID)
		document.Rules[index].DataClassification = DataClassification(strings.TrimSpace(string(document.Rules[index].DataClassification)))
		document.Rules[index].ReasonCode = strings.TrimSpace(document.Rules[index].ReasonCode)
	}
	sort.Slice(document.HardDenies, func(i, j int) bool {
		left := string(document.HardDenies[i].DataClassification) + "\x00" + document.HardDenies[i].ReasonCode
		right := string(document.HardDenies[j].DataClassification) + "\x00" + document.HardDenies[j].ReasonCode
		return left < right
	})
	sort.Slice(document.Rules, func(i, j int) bool {
		left := document.Rules[i].ProviderID + "\x00" + string(document.Rules[i].DataClassification) + "\x00" + string(document.Rules[i].Effect) + "\x00" + document.Rules[i].ReasonCode
		right := document.Rules[j].ProviderID + "\x00" + string(document.Rules[j].DataClassification) + "\x00" + string(document.Rules[j].Effect) + "\x00" + document.Rules[j].ReasonCode
		return left < right
	})
}

func validatePolicyDocument(document policyDocument, options LoadOptions) error {
	if document.SchemaVersion == "" || document.DocumentStatus == "" || document.PolicyID == "" {
		return fmt.Errorf("%w: policy metadata is incomplete", ErrInvalidPolicy)
	}
	if len(document.PolicyID) > 160 || !canonicalIdentifierPattern.MatchString(document.PolicyID) {
		return fmt.Errorf("%w: invalid policy_id", ErrInvalidPolicy)
	}
	if document.PolicyVersion <= 0 {
		return fmt.Errorf("%w: policy_version must be positive", ErrInvalidPolicy)
	}
	if document.DefaultAction != EffectDeny {
		return fmt.Errorf("%w: default_action must be deny", ErrInvalidPolicy)
	}
	providerSet := make(map[string]struct{}, len(options.KnownProviders))
	for _, provider := range options.KnownProviders {
		provider = strings.TrimSpace(provider)
		if provider != "" {
			if len(provider) > 160 || !canonicalIdentifierPattern.MatchString(provider) {
				return fmt.Errorf("%w: invalid known provider %q", ErrInvalidPolicy, provider)
			}
			providerSet[provider] = struct{}{}
		}
	}
	hardSeen := map[DataClassification]struct{}{}
	for _, hardDeny := range document.HardDenies {
		if hardDeny.DataClassification != ClassificationSecret && hardDeny.DataClassification != ClassificationClinical {
			return fmt.Errorf("%w: hard deny classification %q is not allowed", ErrInvalidPolicy, hardDeny.DataClassification)
		}
		if !validReasonCode(hardDeny.ReasonCode) {
			return fmt.Errorf("%w: invalid hard deny reason code", ErrInvalidPolicy)
		}
		if _, duplicate := hardSeen[hardDeny.DataClassification]; duplicate {
			return fmt.Errorf("%w: duplicate hard deny %q", ErrInvalidPolicy, hardDeny.DataClassification)
		}
		hardSeen[hardDeny.DataClassification] = struct{}{}
	}
	for _, required := range []DataClassification{ClassificationSecret, ClassificationClinical} {
		if _, exists := hardSeen[required]; !exists {
			return fmt.Errorf("%w: %s must be a hard deny", ErrInvalidPolicy, required)
		}
	}
	ruleSeen := map[string]struct{}{}
	for _, rule := range document.Rules {
		if _, exists := providerSet[rule.ProviderID]; !exists {
			return fmt.Errorf("%w: unknown provider %q", ErrInvalidPolicy, rule.ProviderID)
		}
		if !validDeclaredClassification(rule.DataClassification) {
			return fmt.Errorf("%w: unknown classification %q", ErrInvalidPolicy, rule.DataClassification)
		}
		if rule.DataClassification == ClassificationSecret || rule.DataClassification == ClassificationClinical {
			return fmt.Errorf("%w: %s is governed exclusively by hard_denies", ErrInvalidPolicy, rule.DataClassification)
		}
		if rule.Effect != EffectAllow && rule.Effect != EffectDeny {
			return fmt.Errorf("%w: invalid effect %q", ErrInvalidPolicy, rule.Effect)
		}
		if rule.Effect == EffectAllow && !options.AllowExplicitAllows {
			return fmt.Errorf("%w: productive policy cannot contain allow rules", ErrInvalidPolicy)
		}
		if !validReasonCode(rule.ReasonCode) {
			return fmt.Errorf("%w: invalid rule reason code", ErrInvalidPolicy)
		}
		key := rule.ProviderID + "\x00" + string(rule.DataClassification)
		if _, duplicate := ruleSeen[key]; duplicate {
			return fmt.Errorf("%w: duplicate provider/classification rule", ErrInvalidPolicy)
		}
		ruleSeen[key] = struct{}{}
	}
	return nil
}

func validDeclaredClassification(classification DataClassification) bool {
	switch classification {
	case ClassificationPublic, ClassificationSanitized, ClassificationOrganizational, ClassificationSecret, ClassificationClinical:
		return true
	default:
		return false
	}
}

func validReasonCode(value string) bool {
	return len(value) >= 1 && len(value) <= 120 && canonicalIdentifierPattern.MatchString(value)
}
