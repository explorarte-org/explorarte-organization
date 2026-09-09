package modelruntime

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	organizationregistry "github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

const (
	routingFileName    = "model-routing.yaml"
	maxRoutingFileSize = 1 << 20
)

type routingDocument struct {
	SchemaVersion     string                   `yaml:"schema_version" json:"schema_version"`
	DocumentStatus    string                   `yaml:"document_status" json:"document_status"`
	Policies          map[string]routingPolicy `yaml:"policies" json:"policies"`
	RoutingInvariants []string                 `yaml:"routing_invariants" json:"routing_invariants"`
}

type routingPolicy struct {
	Provider            string            `yaml:"provider" json:"provider"`
	Model               string            `yaml:"model" json:"model"`
	Transport           Transport         `yaml:"transport" json:"transport"`
	DirectHTTPForbidden bool              `yaml:"direct_http_forbidden,omitempty" json:"direct_http_forbidden,omitempty"`
	SourceOfDecision    string            `yaml:"source_of_decision,omitempty" json:"source_of_decision,omitempty"`
	LegacyConflicts     []string          `yaml:"legacy_conflicts,omitempty" json:"legacy_conflicts,omitempty"`
	ReasoningEffort     string            `yaml:"reasoning_effort,omitempty" json:"reasoning_effort,omitempty"`
	DecisionStatus      string            `yaml:"decision_status,omitempty" json:"decision_status,omitempty"`
	Schedule            string            `yaml:"schedule,omitempty" json:"schedule,omitempty"`
	Capabilities        []ModelCapability `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`

	// RoutingMode is the Dynamic Canonical Model Routing opt-in. Empty
	// (omitted) means RoutingModeStatic -- the entire policy shape and
	// materialization above is untouched for every policy that does not
	// set this. See RoutingModePool for the alternative.
	RoutingMode string             `yaml:"routing_mode,omitempty" json:"routing_mode,omitempty"`
	Selector    string             `yaml:"selector,omitempty" json:"selector,omitempty"`
	AllowPaid   bool               `yaml:"allow_paid,omitempty" json:"allow_paid,omitempty"`
	Candidates  []routingCandidate `yaml:"candidates,omitempty" json:"candidates,omitempty"`
}

// routingCandidate is one pool member. It carries exactly the identity a
// static policy carries (provider/model/transport) plus the two fields a
// selector needs to rank it: capacity_class and priority.
type routingCandidate struct {
	Provider      string    `yaml:"provider" json:"provider"`
	Model         string    `yaml:"model" json:"model"`
	Transport     Transport `yaml:"transport" json:"transport"`
	CapacityClass string    `yaml:"capacity_class" json:"capacity_class"`
	Priority      int       `yaml:"priority" json:"priority"`
}

const (
	RoutingModeStatic = "static"
	RoutingModePool   = "pool"
)

// validCapacityClasses is the closed set of capacity classes a routing
// candidate may declare. "paid" candidates additionally require the pool's
// own allow_paid: true -- declaring the class is not, by itself, permission
// to dispatch it.
var validCapacityClasses = map[string]bool{
	"free_daily":     true,
	"free_model":     true,
	"credit_monthly": true,
	"paid":           true,
}

// validRoutingSelectors is the closed set of selector implementations the
// kernel knows how to run (internal/modelrouting). An unknown selector_id
// fails closed at validation time, before any registry sync -- never at
// dispatch time.
var validRoutingSelectors = map[string]bool{
	"free_capacity_v1": true,
}

type CanonicalRouting struct {
	SchemaVersion  string                   `json:"schema_version"`
	DocumentStatus string                   `json:"document_status"`
	Policies       map[string]routingPolicy `json:"policies"`
	Invariants     []string                 `json:"routing_invariants"`
	Hash           string                   `json:"hash"`
	Path           string                   `json:"path"`
}

type RegistryPlan struct {
	OrganizationID         string
	OrganizationRevisionID int64
	CanonicalHash          string
	Providers              []Provider
	Profiles               []Profile
	Versions               []ProfileVersion
	CapabilitySnapshots    []CapabilitySnapshot
	Bindings               []RoleBinding
	RoutingPolicies        []RoutingPolicy
	RoutingCandidates      []RoutingCandidate
}

// RoutingPolicy is the materialized record of one routing_mode: pool policy.
// Static policies (the overwhelming majority) never produce a RoutingPolicy
// row -- they are fully described by RoleBinding, exactly as before this
// type existed.
type RoutingPolicy struct {
	OrganizationID         string
	OrganizationRevisionID int64
	PolicyID               string
	RoutingMode            string
	SelectorID             string
	AllowPaid              bool
	CanonicalHash          string
}

// RoutingCandidate is one pool member's materialized, FK-checked identity.
// ProfileID/ModelProfileVersionID name a real, independently materialized
// Profile+ProfileVersion (see synthesizeCandidateProfileID) -- never a
// shared row with another candidate or with a static policy.
type RoutingCandidate struct {
	OrganizationID         string
	OrganizationRevisionID int64
	PolicyID               string
	ProviderID             string
	ProviderModelID        string
	Transport              Transport
	CapacityClass          string
	Priority               int
	ProfileID              string
	ModelProfileVersionID  int64
	CandidateHash          string
}

type RegistryStatus struct {
	OrganizationID         string `json:"organization_id"`
	OrganizationRevisionID int64  `json:"organization_revision_id"`
	CanonicalHash          string `json:"canonical_hash"`
	MaterializedHash       string `json:"materialized_hash,omitempty"`
	Synchronized           bool   `json:"synchronized"`
	Providers              int    `json:"providers"`
	Profiles               int    `json:"profiles"`
	ProfileVersions        int    `json:"profile_versions"`
	Bindings               int    `json:"bindings"`
}

type RegistryDiff struct {
	CanonicalHash    string `json:"canonical_hash"`
	MaterializedHash string `json:"materialized_hash,omitempty"`
	Synchronized     bool   `json:"synchronized"`
	Providers        int    `json:"providers"`
	Profiles         int    `json:"profiles"`
	Versions         int    `json:"versions"`
	Bindings         int    `json:"bindings"`
}

type RegistrySyncResult struct {
	Applied                bool   `json:"applied"`
	NoOp                   bool   `json:"no_op"`
	CanonicalHash          string `json:"canonical_hash"`
	OrganizationRevisionID int64  `json:"organization_revision_id"`
	Providers              int    `json:"providers"`
	Profiles               int    `json:"profiles"`
	Versions               int    `json:"versions"`
	Bindings               int    `json:"bindings"`
	RoutingPolicies        int    `json:"routing_policies"`
	RoutingCandidates      int    `json:"routing_candidates"`
	// MissingProviderWallets names every dispatch-enabled provider this
	// sync's own plan requires that has no provider_wallets row at all
	// (G2-001) -- populated whenever a ProviderWalletProvisionChecker is
	// wired, regardless of NoOp/apply/dry-run, so this gap is visible at
	// sync time rather than on the first real dispatch attempt.
	MissingProviderWallets []string `json:"missing_provider_wallets,omitempty"`
}

type ResolvedBinding struct {
	Binding      RoleBinding        `json:"binding"`
	Profile      Profile            `json:"profile"`
	Version      ProfileVersion     `json:"version"`
	Capabilities CapabilitySnapshot `json:"capabilities"`
	Provider     Provider           `json:"provider"`
}

var profileAliases = map[string]string{
	"executive.ceo":     "ceo-primary",
	"department.leader": "leader-primary",
	"department.worker": "worker-default",
}

func loadCanonicalRoutingDocument(canonicalDir string) (CanonicalRouting, error) {
	canonicalDir = strings.TrimSpace(canonicalDir)
	if canonicalDir == "" {
		return CanonicalRouting{}, fmt.Errorf("%w: canonical directory is empty", ErrInvalidRequest)
	}
	path := filepath.Join(canonicalDir, routingFileName)
	info, err := os.Lstat(path)
	if err != nil {
		return CanonicalRouting{}, fmt.Errorf("read canonical model routing: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxRoutingFileSize {
		return CanonicalRouting{}, fmt.Errorf("%w: model routing file is not a valid regular file", ErrInvalidRequest)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return CanonicalRouting{}, fmt.Errorf("read canonical model routing: %w", err)
	}
	body = bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
	body = bytes.ReplaceAll(body, []byte("\r"), []byte("\n"))

	var doc routingDocument
	if err = decodeStrictRouting(body, &doc); err != nil {
		return CanonicalRouting{}, fmt.Errorf("%w: parse model routing: %v", ErrInvalidRequest, err)
	}
	normalizeRouting(&doc)
	if err = validateRouting(doc); err != nil {
		return CanonicalRouting{}, err
	}
	canonical, err := CanonicalJSON(doc)
	if err != nil {
		return CanonicalRouting{}, fmt.Errorf("canonicalize model routing: %w", err)
	}
	return CanonicalRouting{
		SchemaVersion:  doc.SchemaVersion,
		DocumentStatus: doc.DocumentStatus,
		Policies:       doc.Policies,
		Invariants:     doc.RoutingInvariants,
		Hash:           SHA256Bytes(canonical),
		Path:           path,
	}, nil
}

func LoadCanonicalRouting(canonicalDir string) (CanonicalRouting, error) {
	routing, err := loadCanonicalRoutingDocument(canonicalDir)
	if err != nil {
		return CanonicalRouting{}, err
	}
	loader, err := organizationregistry.NewLoader(strings.TrimSpace(canonicalDir))
	if err != nil {
		return CanonicalRouting{}, fmt.Errorf("open canonical registry for model routing hash: %w", err)
	}
	snapshot, _, err := loader.Load()
	if err != nil {
		return CanonicalRouting{}, fmt.Errorf("load canonical registry for model routing hash: %w", err)
	}
	for _, document := range snapshot.Documents {
		if document.Path != routingFileName {
			continue
		}
		if len(document.SemanticHash) != 64 {
			return CanonicalRouting{}, fmt.Errorf("%w: canonical model routing semantic hash is invalid", ErrConflict)
		}
		routing.Hash = document.SemanticHash
		return routing, nil
	}
	return CanonicalRouting{}, fmt.Errorf("%w: canonical model routing digest is missing from organization registry", ErrConflict)
}

func decodeStrictRouting(body []byte, target *routingDocument) error {
	if target == nil {
		return errors.New("routing target is nil")
	}
	if len(body) == 0 {
		return errors.New("routing document is empty")
	}
	if bytes.Contains(body, []byte("\t")) {
		return errors.New("tabs are not allowed in canonical YAML")
	}
	if bytes.Contains(body, []byte("<<:")) || bytes.Contains(body, []byte("&")) || bytes.Contains(body, []byte("*")) {
		return errors.New("YAML anchors, aliases, and merge keys are forbidden")
	}

	target.Policies = make(map[string]routingPolicy)
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 1024), maxRoutingFileSize)
	section := ""
	policyID := ""
	listKey := ""
	topSeen := map[string]struct{}{}
	policySeen := map[string]map[string]struct{}{}
	// candidateSeen tracks fields already set on the CURRENT candidate
	// (the last entry of the current policy's Candidates list) -- reset
	// every time a new "- provider: ..." line starts one. Duplicate
	// detection for candidate fields mirrors policySeen for policy fields.
	var candidateSeen map[string]struct{}
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		raw := strings.TrimRight(scanner.Text(), " \r")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))

		if section == "invariants" {
			if indent != 0 && indent != 2 {
				return fmt.Errorf("line %d: invalid routing invariant indentation", lineNumber)
			}
			if !strings.HasPrefix(trimmed, "- ") {
				return fmt.Errorf("line %d: invalid routing invariant", lineNumber)
			}
			item, err := parseYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			if err != nil {
				return fmt.Errorf("line %d: %w", lineNumber, err)
			}
			target.RoutingInvariants = append(target.RoutingInvariants, item)
			continue
		}

		// candidates is a sequence of MAPS (provider/model/transport/
		// capacity_class/priority per entry), unlike legacy_conflicts/
		// capabilities below (a sequence of plain scalars) -- handled as
		// its own branch so the generic scalar-list branch never sees it.
		if section == "policies" && policyID != "" && listKey == "candidates" {
			if indent == 6 && strings.HasPrefix(trimmed, "- ") {
				rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				key, value, ok, err := splitYAMLScalar(rest)
				if err != nil {
					return fmt.Errorf("line %d: %w", lineNumber, err)
				}
				if !ok {
					return fmt.Errorf("line %d: invalid candidate field", lineNumber)
				}
				policy := target.Policies[policyID]
				policy.Candidates = append(policy.Candidates, routingCandidate{})
				target.Policies[policyID] = policy
				candidateSeen = map[string]struct{}{}
				if err := applyCandidateField(target.Policies[policyID].Candidates, len(target.Policies[policyID].Candidates)-1, key, value, candidateSeen, lineNumber); err != nil {
					return err
				}
				continue
			}
			if indent == 8 && !strings.HasPrefix(trimmed, "- ") {
				policy := target.Policies[policyID]
				if len(policy.Candidates) == 0 {
					return fmt.Errorf("line %d: candidate field without a leading \"- provider: ...\"", lineNumber)
				}
				key, value, ok, err := splitYAMLScalar(trimmed)
				if err != nil {
					return fmt.Errorf("line %d: %w", lineNumber, err)
				}
				if !ok {
					return fmt.Errorf("line %d: invalid candidate field", lineNumber)
				}
				if candidateSeen == nil {
					candidateSeen = map[string]struct{}{}
				}
				if err := applyCandidateField(policy.Candidates, len(policy.Candidates)-1, key, value, candidateSeen, lineNumber); err != nil {
					return err
				}
				continue
			}
			// Any other indent/shape falls through: it is either the end
			// of the candidates list (a new indent==4 policy field or
			// indent==2 policy declaration) or a genuine syntax error,
			// both handled correctly by the existing branches below.
		}

		if section == "policies" && policyID != "" && listKey != "" && (indent == 4 || indent == 6) && strings.HasPrefix(trimmed, "- ") {
			item, err := parseYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			if err != nil {
				return fmt.Errorf("line %d: %w", lineNumber, err)
			}
			policy := target.Policies[policyID]
			if listKey == "legacy_conflicts" {
				policy.LegacyConflicts = append(policy.LegacyConflicts, item)
			} else {
				policy.Capabilities = append(policy.Capabilities, ModelCapability(item))
			}
			target.Policies[policyID] = policy
			continue
		}

		if indent == 0 {
			policyID = ""
			listKey = ""
			key, value, ok, err := splitYAMLScalar(trimmed)
			if err != nil {
				return fmt.Errorf("line %d: %w", lineNumber, err)
			}
			if !ok {
				return fmt.Errorf("line %d: expected top-level key", lineNumber)
			}
			if _, exists := topSeen[key]; exists {
				return fmt.Errorf("line %d: duplicate top-level key %q", lineNumber, key)
			}
			topSeen[key] = struct{}{}
			switch key {
			case "schema_version":
				if value == "" {
					return fmt.Errorf("line %d: schema_version cannot be empty", lineNumber)
				}
				target.SchemaVersion = value
				section = ""
			case "document_status":
				if value == "" {
					return fmt.Errorf("line %d: document_status cannot be empty", lineNumber)
				}
				target.DocumentStatus = value
				section = ""
			case "policies":
				if value != "" {
					return fmt.Errorf("line %d: policies must be a mapping", lineNumber)
				}
				section = "policies"
			case "routing_invariants":
				if value != "" {
					return fmt.Errorf("line %d: routing_invariants must be a sequence", lineNumber)
				}
				section = "invariants"
			default:
				return fmt.Errorf("line %d: unknown top-level key %q", lineNumber, key)
			}
			continue
		}

		if section != "policies" {
			return fmt.Errorf("line %d: nested content outside policies", lineNumber)
		}
		if indent == 2 {
			if !strings.HasSuffix(trimmed, ":") || strings.HasPrefix(trimmed, "-") {
				return fmt.Errorf("line %d: invalid policy declaration", lineNumber)
			}
			policyID = strings.TrimSpace(strings.TrimSuffix(trimmed, ":"))
			if policyID == "" {
				return fmt.Errorf("line %d: empty policy ID", lineNumber)
			}
			if _, exists := target.Policies[policyID]; exists {
				return fmt.Errorf("line %d: duplicate policy %q", lineNumber, policyID)
			}
			target.Policies[policyID] = routingPolicy{}
			policySeen[policyID] = map[string]struct{}{}
			listKey = ""
			continue
		}
		if policyID == "" || indent != 4 {
			return fmt.Errorf("line %d: policy field without policy or invalid indentation", lineNumber)
		}
		key, value, ok, err := splitYAMLScalar(trimmed)
		if err != nil {
			return fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if !ok {
			return fmt.Errorf("line %d: invalid policy field", lineNumber)
		}
		if _, exists := policySeen[policyID][key]; exists {
			return fmt.Errorf("line %d: duplicate policy field %q", lineNumber, key)
		}
		policySeen[policyID][key] = struct{}{}
		policy := target.Policies[policyID]
		listKey = ""
		switch key {
		case "provider":
			policy.Provider = value
		case "model":
			policy.Model = value
		case "transport":
			policy.Transport = Transport(value)
		case "direct_http_forbidden":
			parsed, parseErr := strconv.ParseBool(value)
			if parseErr != nil {
				return fmt.Errorf("line %d: invalid boolean", lineNumber)
			}
			policy.DirectHTTPForbidden = parsed
		case "source_of_decision":
			policy.SourceOfDecision = value
		case "reasoning_effort":
			policy.ReasoningEffort = value
		case "decision_status":
			policy.DecisionStatus = value
		case "schedule":
			policy.Schedule = value
		case "routing_mode":
			policy.RoutingMode = value
		case "selector":
			policy.Selector = value
		case "allow_paid":
			parsed, parseErr := strconv.ParseBool(value)
			if parseErr != nil {
				return fmt.Errorf("line %d: invalid boolean", lineNumber)
			}
			policy.AllowPaid = parsed
		case "legacy_conflicts", "capabilities":
			if value != "" {
				return fmt.Errorf("line %d: %s must be a sequence", lineNumber, key)
			}
			listKey = key
		case "candidates":
			if value != "" {
				return fmt.Errorf("line %d: candidates must be a sequence", lineNumber)
			}
			listKey = key
		default:
			return fmt.Errorf("line %d: unknown policy field %q", lineNumber, key)
		}
		target.Policies[policyID] = policy
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

// applyCandidateField sets one field on candidates[index], mutating the
// backing array in place (routingCandidate slices always alias the copy
// stored in target.Policies[policyID] -- see the two call sites in
// decodeStrictRouting). seen tracks fields already set on THIS candidate,
// scoped and reset per "- provider: ..." line, mirroring policySeen for
// top-level policy fields.
func applyCandidateField(candidates []routingCandidate, index int, key, value string, seen map[string]struct{}, lineNumber int) error {
	if index < 0 || index >= len(candidates) {
		return fmt.Errorf("line %d: candidate field without a candidate", lineNumber)
	}
	if _, exists := seen[key]; exists {
		return fmt.Errorf("line %d: duplicate candidate field %q", lineNumber, key)
	}
	seen[key] = struct{}{}
	switch key {
	case "provider":
		candidates[index].Provider = value
	case "model":
		candidates[index].Model = value
	case "transport":
		candidates[index].Transport = Transport(value)
	case "capacity_class":
		candidates[index].CapacityClass = value
	case "priority":
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("line %d: invalid priority integer", lineNumber)
		}
		candidates[index].Priority = parsed
	default:
		return fmt.Errorf("line %d: unknown candidate field %q", lineNumber, key)
	}
	return nil
}

func splitYAMLScalar(text string) (string, string, bool, error) {
	index := strings.IndexByte(text, ':')
	if index < 1 {
		return "", "", false, nil
	}
	key := strings.TrimSpace(text[:index])
	value, err := parseYAMLScalar(strings.TrimSpace(text[index+1:]))
	if err != nil {
		return "", "", false, err
	}
	return key, value, true, nil
}

func parseYAMLScalar(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{") || value == "|" || value == ">" {
		return "", errors.New("flow collections and block scalars are not supported")
	}
	if value[0] == '"' {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("invalid quoted scalar: %w", err)
		}
		return unquoted, nil
	}
	if value[0] == '\'' {
		if len(value) < 2 || value[len(value)-1] != '\'' {
			return "", errors.New("invalid single-quoted scalar")
		}
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'"), nil
	}
	return value, nil
}

func validateRouting(d routingDocument) error {
	if strings.TrimSpace(d.SchemaVersion) == "" || strings.TrimSpace(d.DocumentStatus) == "" {
		return fmt.Errorf("%w: routing metadata missing", ErrInvalidRequest)
	}
	if len(d.Policies) == 0 {
		return fmt.Errorf("%w: no routing policies", ErrInvalidRequest)
	}
	for id, p := range d.Policies {
		if !roleIDPattern.MatchString(id) {
			return fmt.Errorf("%w: invalid policy identifier %q", ErrInvalidRequest, id)
		}
		for _, capability := range p.Capabilities {
			if len(capability) > 160 || !roleIDPattern.MatchString(string(capability)) {
				return fmt.Errorf("%w: invalid model capability %q", ErrInvalidRequest, capability)
			}
		}
		mode := p.RoutingMode
		if mode == "" {
			mode = RoutingModeStatic
		}
		switch mode {
		case RoutingModeStatic:
			if err := validateStaticPolicy(id, p); err != nil {
				return err
			}
		case RoutingModePool:
			if err := validatePoolPolicy(id, p); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: unknown routing_mode %q in policy %q", ErrInvalidRequest, p.RoutingMode, id)
		}
	}
	return nil
}

func validateStaticPolicy(id string, p routingPolicy) error {
	// A static policy is fully described by provider/model/transport. Pool
	// fields present on a static policy are ambiguous configuration, not a
	// harmless no-op -- reject rather than silently ignore.
	if p.Selector != "" || p.AllowPaid || len(p.Candidates) > 0 {
		return fmt.Errorf("%w: policy %q is static but sets pool-only fields (selector/allow_paid/candidates)", ErrInvalidRequest, id)
	}
	if strings.TrimSpace(p.Provider) == "" || len(p.Provider) > 160 {
		return fmt.Errorf("%w: invalid provider in policy %q", ErrInvalidRequest, id)
	}
	if strings.TrimSpace(p.Model) == "" || len(p.Model) > 240 {
		return fmt.Errorf("%w: invalid model in policy %q", ErrInvalidRequest, id)
	}
	switch p.Transport {
	case TransportCLI, TransportHTTP, TransportFake:
	default:
		return fmt.Errorf("%w: unsupported transport %q", ErrInvalidRequest, p.Transport)
	}
	if p.DirectHTTPForbidden && p.Transport == TransportHTTP {
		return fmt.Errorf("%w: direct HTTP forbidden policy uses HTTP", ErrInvalidRequest)
	}
	if (p.Transport == TransportFake) != (p.Provider == "test.fake") {
		return fmt.Errorf("%w: fake transport is restricted to provider test.fake", ErrInvalidRequest)
	}
	return nil
}

func validatePoolPolicy(id string, p routingPolicy) error {
	// A pool policy carries no top-level provider/model of its own -- that
	// would be a second, ambiguous source of truth alongside its
	// candidates. Selection is exclusively through the candidate set.
	if p.Provider != "" || p.Model != "" {
		return fmt.Errorf("%w: policy %q is a pool but also sets a static provider/model", ErrInvalidRequest, id)
	}
	if !validRoutingSelectors[p.Selector] {
		return fmt.Errorf("%w: policy %q has unknown or missing selector %q", ErrInvalidRequest, id, p.Selector)
	}
	if len(p.Candidates) == 0 {
		return fmt.Errorf("%w: pool policy %q has no candidates", ErrInvalidRequest, id)
	}
	seen := map[string]bool{}
	for i, c := range p.Candidates {
		if strings.TrimSpace(c.Provider) == "" || len(c.Provider) > 160 {
			return fmt.Errorf("%w: pool policy %q candidate %d has an invalid provider", ErrInvalidRequest, id, i)
		}
		if strings.TrimSpace(c.Model) == "" || len(c.Model) > 240 {
			return fmt.Errorf("%w: pool policy %q candidate %d has an invalid model", ErrInvalidRequest, id, i)
		}
		switch c.Transport {
		case TransportCLI, TransportHTTP, TransportFake:
		default:
			return fmt.Errorf("%w: pool policy %q candidate %d has unsupported transport %q", ErrInvalidRequest, id, i, c.Transport)
		}
		if (c.Transport == TransportFake) != (c.Provider == "test.fake") {
			return fmt.Errorf("%w: pool policy %q candidate %d: fake transport is restricted to provider test.fake", ErrInvalidRequest, id, i)
		}
		if !validCapacityClasses[c.CapacityClass] {
			return fmt.Errorf("%w: pool policy %q candidate %d has unknown capacity_class %q", ErrInvalidRequest, id, i, c.CapacityClass)
		}
		if c.CapacityClass == "paid" && !p.AllowPaid {
			return fmt.Errorf("%w: pool policy %q candidate %d is capacity_class paid but the pool does not set allow_paid", ErrInvalidRequest, id, i)
		}
		key := c.Provider + "\x00" + c.Model
		if seen[key] {
			return fmt.Errorf("%w: pool policy %q has a duplicate candidate provider=%s model=%s", ErrInvalidRequest, id, c.Provider, c.Model)
		}
		seen[key] = true
	}
	return nil
}

func normalizeRouting(d *routingDocument) {
	d.SchemaVersion = strings.TrimSpace(d.SchemaVersion)
	d.DocumentStatus = strings.TrimSpace(d.DocumentStatus)
	for id, p := range d.Policies {
		p.Provider = strings.TrimSpace(p.Provider)
		p.Model = strings.TrimSpace(p.Model)
		p.SourceOfDecision = strings.TrimSpace(p.SourceOfDecision)
		p.ReasoningEffort = strings.TrimSpace(p.ReasoningEffort)
		p.DecisionStatus = strings.TrimSpace(p.DecisionStatus)
		p.Schedule = strings.TrimSpace(p.Schedule)
		p.Capabilities = normalizeCapabilities(p.Capabilities)
		for i := range p.LegacyConflicts {
			p.LegacyConflicts[i] = strings.TrimSpace(p.LegacyConflicts[i])
		}
		sort.Strings(p.LegacyConflicts)
		p.RoutingMode = strings.TrimSpace(p.RoutingMode)
		p.Selector = strings.TrimSpace(p.Selector)
		for i := range p.Candidates {
			p.Candidates[i].Provider = strings.TrimSpace(p.Candidates[i].Provider)
			p.Candidates[i].Model = strings.TrimSpace(p.Candidates[i].Model)
			p.Candidates[i].CapacityClass = strings.TrimSpace(p.Candidates[i].CapacityClass)
		}
		// Deterministic candidate order for stable hashing, independent of
		// each candidate's own Priority (a ranking input, not an ordering
		// of this slice).
		sort.Slice(p.Candidates, func(i, j int) bool {
			if p.Candidates[i].Provider != p.Candidates[j].Provider {
				return p.Candidates[i].Provider < p.Candidates[j].Provider
			}
			return p.Candidates[i].Model < p.Candidates[j].Model
		})
		d.Policies[id] = p
	}
	for i := range d.RoutingInvariants {
		d.RoutingInvariants[i] = strings.TrimSpace(d.RoutingInvariants[i])
	}
	sort.Strings(d.RoutingInvariants)
}

// NormalizeCapabilities is the canonical representation of a capability set:
// trimmed, empty entries dropped, deduplicated, sorted. It is exported because
// callers that derive a durable identity from a capability set -- an
// idempotency contract, a digest -- must derive it from the same bytes this
// package persists, not from whatever the caller happened to pass in.
func NormalizeCapabilities(values []ModelCapability) []ModelCapability {
	return normalizeCapabilities(values)
}

func normalizeCapabilities(values []ModelCapability) []ModelCapability {
	seen := map[string]struct{}{}
	result := make([]ModelCapability, 0, len(values))
	for _, value := range values {
		capability := strings.TrimSpace(string(value))
		if capability == "" {
			continue
		}
		if _, exists := seen[capability]; exists {
			continue
		}
		seen[capability] = struct{}{}
		result = append(result, ModelCapability(capability))
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func profileIDForPolicy(policy string) string {
	if alias, ok := profileAliases[policy]; ok {
		return alias
	}
	return policy
}

// compiledAdapterAvailability lists, in this exact literal form, every
// provider/transport pair with a compiled adapter. scripts/check-model-runtime-fitness.sh
// greps for these conditions verbatim; keep the phrasing (policy.Transport
// == ... && policy.Provider == ...) in sync with the fitness script if this
// set ever changes.
func compiledAdapterAvailability(policy routingPolicy) (AdapterStatus, bool) {
	switch {
	case policy.Transport == TransportFake && policy.Provider == "test.fake":
		return AdapterAvailable, true
	case policy.Transport == TransportHTTP && policy.Provider == "openai_compatible":
		return AdapterAvailable, true
	case policy.Transport == TransportHTTP && policy.Provider == "deepseek":
		return AdapterAvailable, true
	case policy.Transport == TransportHTTP && policy.Provider == "cloudflare_workers_ai":
		return AdapterAvailable, true
	case policy.Transport == TransportHTTP && policy.Provider == "gemini":
		return AdapterAvailable, true
	case policy.Transport == TransportHTTP && policy.Provider == "openai_responses":
		return AdapterAvailable, true
	case policy.Transport == TransportHTTP && policy.Provider == "mistral":
		// Investigator capacity candidate -- internal/modelruntime/adapter/mistral.
		return AdapterAvailable, true
	case policy.Transport == TransportHTTP && policy.Provider == "xai":
		// Adversarial reviewer provider -- internal/modelruntime/adapter/xai.
		// Compiled and dispatchable does not mean reachable: the reviewer role
		// is still disabled in the canonical catalog, the adapter is disabled
		// unless its environment is configured, and the egress gate requires
		// ScopeAdversarialReview.
		return AdapterAvailable, true
	default:
		return AdapterUnavailable, false
	}
}

// synthesizeCandidateProfileID names one pool candidate's independently
// materialized profile. It is index-based (never provider/model text)
// specifically so it never has to satisfy roleIDPattern or worry about
// characters a real provider/model id might contain (e.g. Cloudflare's
// "@cf/zai-org/glm-4.7-flash"). model_profiles.id has no charset
// constraint at the DB layer (see migrations/000007), only a length CHECK.
func synthesizeCandidateProfileID(policyID string, index int) string {
	return fmt.Sprintf("%s~pool~%d", policyID, index)
}

// registerProvider applies the SAME dedup/conflict-check every provider
// (static or pool candidate) goes through: same transport/direct-HTTP/
// adapter-status/dispatch-enabled must agree everywhere that provider ID
// is referenced across this organization revision's whole routing document.
func registerProvider(providers map[string]Provider, id string, transport Transport, directHTTPForbidden bool, org OrganizationRef, routingHash string, adapterStatus AdapterStatus, dispatchEnabled bool) (Provider, error) {
	providerHashBody, err := CanonicalJSON(map[string]any{
		"provider":              id,
		"transport":             transport,
		"direct_http_forbidden": directHTTPForbidden,
		"organization_revision": org.RevisionID,
		"canonical_hash":        routingHash,
	})
	if err != nil {
		return Provider{}, err
	}
	provider := Provider{
		OrganizationID:         org.ID,
		ID:                     id,
		Transport:              transport,
		AdapterStatus:          adapterStatus,
		DispatchEnabled:        dispatchEnabled,
		DirectHTTPForbidden:    directHTTPForbidden,
		CanonicalHash:          SHA256Bytes(providerHashBody),
		OrganizationRevisionID: org.RevisionID,
	}
	if existing, ok := providers[id]; ok {
		if existing.Transport != provider.Transport || existing.DirectHTTPForbidden != provider.DirectHTTPForbidden || existing.AdapterStatus != provider.AdapterStatus || existing.DispatchEnabled != provider.DispatchEnabled {
			return Provider{}, fmt.Errorf("%w: provider %s has conflicting transport policies", ErrInvalidRequest, id)
		}
	} else {
		providers[id] = provider
	}
	return provider, nil
}

func BuildRegistryPlan(roles []RoleRef, org OrganizationRef, routing CanonicalRouting) (RegistryPlan, error) {
	if strings.TrimSpace(org.ID) == "" || org.RevisionID <= 0 {
		return RegistryPlan{}, fmt.Errorf("%w: organization revision is required", ErrInvalidRequest)
	}
	if org.ModelRoutingHash != "" && org.ModelRoutingHash != routing.Hash {
		return RegistryPlan{}, fmt.Errorf("%w: model routing canonical hash does not match organization revision", ErrConflict)
	}
	plan := RegistryPlan{
		OrganizationID:         org.ID,
		OrganizationRevisionID: org.RevisionID,
		CanonicalHash:          routing.Hash,
	}

	policyIDs := make([]string, 0, len(routing.Policies))
	for id := range routing.Policies {
		policyIDs = append(policyIDs, id)
	}
	sort.Strings(policyIDs)

	providers := map[string]Provider{}
	versionsByPolicy := map[string]ProfileVersion{}
	profilesSeen := map[string]string{}
	poolPolicyIDs := map[string]bool{}
	for _, policyID := range policyIDs {
		policy := routing.Policies[policyID]
		mode := policy.RoutingMode
		if mode == "" {
			mode = RoutingModeStatic
		}

		if mode == RoutingModePool {
			poolPolicyIDs[policyID] = true
			selectorHashBody, err := CanonicalJSON(map[string]any{
				"routing_mode":          mode,
				"selector":              policy.Selector,
				"allow_paid":            policy.AllowPaid,
				"candidates":            policy.Candidates,
				"organization_revision": org.RevisionID,
				"canonical_hash":        routing.Hash,
			})
			if err != nil {
				return RegistryPlan{}, err
			}
			plan.RoutingPolicies = append(plan.RoutingPolicies, RoutingPolicy{
				OrganizationID:         org.ID,
				OrganizationRevisionID: org.RevisionID,
				PolicyID:               policyID,
				RoutingMode:            mode,
				SelectorID:             policy.Selector,
				AllowPaid:              policy.AllowPaid,
				CanonicalHash:          SHA256Bytes(selectorHashBody),
			})

			for idx, cand := range policy.Candidates {
				candAdapterStatus, candDispatchEnabled := compiledAdapterAvailability(routingPolicy{Provider: cand.Provider, Transport: cand.Transport})
				if _, err := registerProvider(providers, cand.Provider, cand.Transport, false, org, routing.Hash, candAdapterStatus, candDispatchEnabled); err != nil {
					return RegistryPlan{}, err
				}

				candidateProfileID := synthesizeCandidateProfileID(policyID, idx)
				plan.Profiles = append(plan.Profiles, Profile{OrganizationID: org.ID, ID: candidateProfileID, PolicyID: candidateProfileID})

				versionBody, err := CanonicalJSON(map[string]any{
					"organization_revision_id": org.RevisionID,
					"canonical_document_hash":  routing.Hash,
					"policy_id":                candidateProfileID,
					"profile_id":               candidateProfileID,
					"provider_id":              cand.Provider,
					"provider_model_id":        cand.Model,
					"transport":                cand.Transport,
					"capabilities":             policy.Capabilities,
					"adapter_status":           candAdapterStatus,
					"dispatch_enabled":         candDispatchEnabled,
				})
				if err != nil {
					return RegistryPlan{}, err
				}
				plan.Versions = append(plan.Versions, ProfileVersion{
					OrganizationID:         org.ID,
					ProfileID:              candidateProfileID,
					OrganizationRevisionID: org.RevisionID,
					CanonicalDocumentHash:  routing.Hash,
					VersionHash:            SHA256Bytes(versionBody),
					ProviderID:             cand.Provider,
					ProviderModelID:        cand.Model,
					Transport:              cand.Transport,
					AdapterStatus:          candAdapterStatus,
					DispatchEnabled:        candDispatchEnabled,
				})

				capabilityBody, err := CanonicalJSON(policy.Capabilities)
				if err != nil {
					return RegistryPlan{}, err
				}
				plan.CapabilitySnapshots = append(plan.CapabilitySnapshots, CapabilitySnapshot{
					OrganizationID: org.ID,
					ProfileID:      candidateProfileID,
					Capabilities:   policy.Capabilities,
					CapabilityHash: SHA256Bytes(capabilityBody),
				})

				candHashBody, err := CanonicalJSON(map[string]any{
					"policy_id":         policyID,
					"provider_id":       cand.Provider,
					"provider_model_id": cand.Model,
					"transport":         cand.Transport,
					"capacity_class":    cand.CapacityClass,
					"priority":          cand.Priority,
					"profile_id":        candidateProfileID,
				})
				if err != nil {
					return RegistryPlan{}, err
				}
				plan.RoutingCandidates = append(plan.RoutingCandidates, RoutingCandidate{
					OrganizationID:         org.ID,
					OrganizationRevisionID: org.RevisionID,
					PolicyID:               policyID,
					ProviderID:             cand.Provider,
					ProviderModelID:        cand.Model,
					Transport:              cand.Transport,
					CapacityClass:          cand.CapacityClass,
					Priority:               cand.Priority,
					ProfileID:              candidateProfileID,
					CandidateHash:          SHA256Bytes(candHashBody),
				})
			}
			continue
		}

		// mode == RoutingModeStatic: unchanged from the pre-pool behavior.
		adapterStatus, dispatchEnabled := compiledAdapterAvailability(policy)
		if _, err := registerProvider(providers, policy.Provider, policy.Transport, policy.DirectHTTPForbidden, org, routing.Hash, adapterStatus, dispatchEnabled); err != nil {
			return RegistryPlan{}, err
		}

		profileID := profileIDForPolicy(policyID)
		if existingPolicy, exists := profilesSeen[profileID]; exists && existingPolicy != policyID {
			return RegistryPlan{}, fmt.Errorf("%w: profile alias %s is shared by policies %s and %s", ErrInvalidRequest, profileID, existingPolicy, policyID)
		}
		profilesSeen[profileID] = policyID
		plan.Profiles = append(plan.Profiles, Profile{OrganizationID: org.ID, ID: profileID, PolicyID: policyID})

		versionBody, err := CanonicalJSON(map[string]any{
			"organization_revision_id": org.RevisionID,
			"canonical_document_hash":  routing.Hash,
			"policy_id":                policyID,
			"profile_id":               profileID,
			"provider_id":              policy.Provider,
			"provider_model_id":        policy.Model,
			"transport":                policy.Transport,
			"reasoning_effort":         policy.ReasoningEffort,
			"decision_status":          policy.DecisionStatus,
			"capabilities":             policy.Capabilities,
			"adapter_status":           adapterStatus,
			"dispatch_enabled":         dispatchEnabled,
		})
		if err != nil {
			return RegistryPlan{}, err
		}
		version := ProfileVersion{
			OrganizationID:         org.ID,
			ProfileID:              profileID,
			OrganizationRevisionID: org.RevisionID,
			CanonicalDocumentHash:  routing.Hash,
			VersionHash:            SHA256Bytes(versionBody),
			ProviderID:             policy.Provider,
			ProviderModelID:        policy.Model,
			Transport:              policy.Transport,
			ReasoningEffort:        policy.ReasoningEffort,
			DecisionStatus:         policy.DecisionStatus,
			AdapterStatus:          adapterStatus,
			DispatchEnabled:        dispatchEnabled,
		}
		versionsByPolicy[policyID] = version
		plan.Versions = append(plan.Versions, version)

		capabilityBody, err := CanonicalJSON(policy.Capabilities)
		if err != nil {
			return RegistryPlan{}, err
		}
		plan.CapabilitySnapshots = append(plan.CapabilitySnapshots, CapabilitySnapshot{
			OrganizationID: org.ID,
			ProfileID:      profileID,
			Capabilities:   policy.Capabilities,
			CapabilityHash: SHA256Bytes(capabilityBody),
		})
	}

	for _, provider := range providers {
		plan.Providers = append(plan.Providers, provider)
	}
	sort.Slice(plan.Providers, func(i, j int) bool { return plan.Providers[i].ID < plan.Providers[j].ID })
	sort.Slice(plan.Profiles, func(i, j int) bool { return plan.Profiles[i].ID < plan.Profiles[j].ID })
	sort.Slice(plan.Versions, func(i, j int) bool { return plan.Versions[i].ProfileID < plan.Versions[j].ProfileID })
	sort.Slice(plan.CapabilitySnapshots, func(i, j int) bool {
		return plan.CapabilitySnapshots[i].ProfileID < plan.CapabilitySnapshots[j].ProfileID
	})
	sort.Slice(plan.RoutingPolicies, func(i, j int) bool { return plan.RoutingPolicies[i].PolicyID < plan.RoutingPolicies[j].PolicyID })
	sort.Slice(plan.RoutingCandidates, func(i, j int) bool {
		if plan.RoutingCandidates[i].PolicyID != plan.RoutingCandidates[j].PolicyID {
			return plan.RoutingCandidates[i].PolicyID < plan.RoutingCandidates[j].PolicyID
		}
		return plan.RoutingCandidates[i].ProfileID < plan.RoutingCandidates[j].ProfileID
	})

	sortedRoles := append([]RoleRef(nil), roles...)
	sort.Slice(sortedRoles, func(i, j int) bool { return sortedRoles[i].ID < sortedRoles[j].ID })
	for _, role := range sortedRoles {
		if role.ModelPolicy == "" {
			continue
		}
		if poolPolicyIDs[role.ModelPolicy] {
			// Pool policies are resolved at Create() time via
			// RoutingPolicy/RoutingCandidate, never through a static
			// RoleBinding row -- there is deliberately no single version
			// to bind a pool role to.
			continue
		}
		version, ok := versionsByPolicy[role.ModelPolicy]
		if !ok {
			return RegistryPlan{}, fmt.Errorf("%w: role %s references unknown model policy %s", ErrInvalidRequest, role.ID, role.ModelPolicy)
		}
		profileID := profileIDForPolicy(role.ModelPolicy)
		bindingBody, err := CanonicalJSON(map[string]any{
			"organization_id":          org.ID,
			"organization_revision_id": org.RevisionID,
			"role_id":                  role.ID,
			"policy_id":                role.ModelPolicy,
			"profile_id":               profileID,
			"version_hash":             version.VersionHash,
		})
		if err != nil {
			return RegistryPlan{}, err
		}
		plan.Bindings = append(plan.Bindings, RoleBinding{
			OrganizationID:         org.ID,
			OrganizationRevisionID: org.RevisionID,
			RoleID:                 role.ID,
			PolicyID:               role.ModelPolicy,
			ProfileID:              profileID,
			BindingHash:            SHA256Bytes(bindingBody),
			Active:                 role.Enabled && role.Executable,
		})
	}
	return plan, nil
}
