package registry

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
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

const (
	defaultMaxCanonicalFileSize int64 = 4 << 20
	maxYAMLDepth                      = 64
	maxYAMLNodes                      = 100000
)

var requiredCanonicalFiles = []string{
	"organization.yaml",
	"role-catalog.yaml",
	"leader-worker-map.yaml",
	"model-routing.yaml",
	"capability-matrix.yaml",
	"instruction-precedence.yaml",
	"decisions-required.yaml",
	"source-manifest.yaml",
}

type Loader struct {
	dir         string
	maxFileSize int64
}

func NewLoader(dir string) (*Loader, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("canonical directory cannot be empty")
	}
	return &Loader{dir: filepath.Clean(dir), maxFileSize: defaultMaxCanonicalFileSize}, nil
}

func (l *Loader) Directory() string { return l.dir }

func (l *Loader) Load() (Snapshot, ValidationReport, error) {
	documents, digests, err := l.readDocuments()
	if err != nil {
		return Snapshot{}, ValidationReport{}, err
	}
	normalizeDocuments(&documents)
	snapshot := materialize(documents)
	report := validateDocuments(documents, snapshot)
	if !report.Valid() {
		return Snapshot{}, report, &ValidationError{Report: report}
	}
	hashes, aggregate, err := hashDocuments(documents)
	if err != nil {
		return Snapshot{}, report, err
	}
	for index := range digests {
		digests[index].SemanticHash = hashes[digests[index].Path]
	}
	snapshot.Documents = digests
	snapshot.CanonicalHash = aggregate
	snapshot.Counts = countSnapshot(snapshot)
	return snapshot, report, nil
}

func (l *Loader) readDocuments() (parsedDocuments, []DocumentDigest, error) {
	var documents parsedDocuments
	digests := make([]DocumentDigest, 0, len(requiredCanonicalFiles))
	loads := []struct {
		name   string
		target any
		meta   func() (string, string)
	}{
		{"organization.yaml", &documents.Organization, func() (string, string) {
			return documents.Organization.SchemaVersion, documents.Organization.DocumentStatus
		}},
		{"role-catalog.yaml", &documents.Roles, func() (string, string) { return documents.Roles.SchemaVersion, documents.Roles.DocumentStatus }},
		{"leader-worker-map.yaml", &documents.LeaderWorker, func() (string, string) {
			return documents.LeaderWorker.SchemaVersion, documents.LeaderWorker.DocumentStatus
		}},
		{"model-routing.yaml", &documents.ModelRouting, func() (string, string) {
			return documents.ModelRouting.SchemaVersion, documents.ModelRouting.DocumentStatus
		}},
		{"capability-matrix.yaml", &documents.Capabilities, func() (string, string) {
			return documents.Capabilities.SchemaVersion, documents.Capabilities.DocumentStatus
		}},
		{"instruction-precedence.yaml", &documents.InstructionPrecedence, func() (string, string) {
			return documents.InstructionPrecedence.SchemaVersion, documents.InstructionPrecedence.DocumentStatus
		}},
		{"decisions-required.yaml", &documents.Decisions, func() (string, string) { return "", documents.Decisions.Status }},
		{"source-manifest.yaml", &documents.SourceManifest, func() (string, string) { return documents.SourceManifest.SchemaVersion, "" }},
	}
	for _, item := range loads {
		body, err := l.readFile(item.name)
		if err != nil {
			return parsedDocuments{}, nil, err
		}
		if err := decodeStrictYAML(body, item.target); err != nil {
			return parsedDocuments{}, nil, fmt.Errorf("parse %s: %w", item.name, err)
		}
		schemaVersion, documentStatus := item.meta()
		digests = append(digests, DocumentDigest{Path: item.name, SchemaVersion: schemaVersion, DocumentStatus: documentStatus})
	}
	return documents, digests, nil
}

func (l *Loader) readFile(name string) ([]byte, error) {
	if filepath.Base(name) != name {
		return nil, fmt.Errorf("canonical filename %q is invalid", name)
	}
	path := filepath.Join(l.dir, name)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat canonical document %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("canonical document %s is not a regular file", name)
	}
	if info.Size() <= 0 || info.Size() > l.maxFileSize {
		return nil, fmt.Errorf("canonical document %s size %d is outside allowed range", name, info.Size())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read canonical document %s: %w", name, err)
	}
	body = bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
	body = bytes.ReplaceAll(body, []byte("\r"), []byte("\n"))
	return body, nil
}

func decodeStrictYAML(body []byte, target any) error {
	if len(body) == 0 {
		return errors.New("document is empty")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	var root yaml.Node
	if err := decoder.Decode(&root); err != nil {
		return err
	}
	if err := validateYAMLNode(&root, 0, new(int)); err != nil {
		return err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple YAML documents are not allowed")
		}
		return fmt.Errorf("read trailing YAML: %w", err)
	}
	strict := yaml.NewDecoder(bytes.NewReader(body))
	strict.KnownFields(true)
	if err := strict.Decode(target); err != nil {
		return err
	}
	return nil
}

func validateYAMLNode(node *yaml.Node, depth int, count *int) error {
	if node == nil {
		return nil
	}
	(*count)++
	if *count > maxYAMLNodes {
		return errors.New("YAML document exceeds node limit")
	}
	if depth > maxYAMLDepth {
		return errors.New("YAML document exceeds maximum depth")
	}
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return errors.New("YAML anchors and aliases are not allowed")
	}
	allowedTags := map[string]bool{
		"": true, "!!map": true, "!!seq": true, "!!str": true, "!!bool": true,
		"!!int": true, "!!null": true, "!!timestamp": true, "!!float": true,
	}
	if !allowedTags[node.Tag] {
		return fmt.Errorf("YAML tag %q is not allowed", node.Tag)
	}
	if node.Kind == yaml.MappingNode {
		for index := 0; index < len(node.Content); index += 2 {
			if index < len(node.Content) && node.Content[index].Value == "<<" {
				return errors.New("YAML merge keys are not allowed")
			}
		}
	}
	for _, child := range node.Content {
		if err := validateYAMLNode(child, depth+1, count); err != nil {
			return err
		}
	}
	return nil
}

func normalizeDocuments(documents *parsedDocuments) {
	sort.Slice(documents.Organization.OperationalDepartments, func(i, j int) bool {
		return documents.Organization.OperationalDepartments[i].ID < documents.Organization.OperationalDepartments[j].ID
	})
	sort.Slice(documents.Organization.TransversalUnits, func(i, j int) bool {
		return documents.Organization.TransversalUnits[i].ID < documents.Organization.TransversalUnits[j].ID
	})
	for index := range documents.Organization.TransversalUnits {
		sort.Strings(documents.Organization.TransversalUnits[index].Roles)
	}
	sort.Slice(documents.Roles.Roles, func(i, j int) bool { return documents.Roles.Roles[i].ID < documents.Roles.Roles[j].ID })
	for index := range documents.Roles.Roles {
		sort.Strings(documents.Roles.Roles[index].ReportsTo)
	}
	sort.Slice(documents.LeaderWorker.Departments, func(i, j int) bool {
		return documents.LeaderWorker.Departments[i].ID < documents.LeaderWorker.Departments[j].ID
	})
	for index := range documents.LeaderWorker.Departments {
		sort.Strings(documents.LeaderWorker.Departments[index].Workers)
	}
	for key := range documents.LeaderWorker.Transversal {
		values := documents.LeaderWorker.Transversal[key]
		sort.Strings(values)
		documents.LeaderWorker.Transversal[key] = values
	}
	for key, policy := range documents.ModelRouting.Policies {
		sort.Strings(policy.LegacyConflicts)
		documents.ModelRouting.Policies[key] = policy
	}
	sort.Strings(documents.ModelRouting.RoutingInvariants)
	sort.Slice(documents.Capabilities.Capabilities, func(i, j int) bool {
		return documents.Capabilities.Capabilities[i].ID < documents.Capabilities.Capabilities[j].ID
	})
	for key := range documents.Capabilities.Grants {
		sort.Strings(documents.Capabilities.Grants[key])
	}
	for key := range documents.Capabilities.HardDenies {
		sort.Strings(documents.Capabilities.HardDenies[key])
	}
	sort.Strings(documents.Capabilities.SkillLifecycle.States)
	sort.Strings(documents.Capabilities.SkillLifecycle.ActivationRequires)
	sort.Slice(documents.Capabilities.ImportedSkills, func(i, j int) bool {
		return documents.Capabilities.ImportedSkills[i].ID < documents.Capabilities.ImportedSkills[j].ID
	})
	sort.Slice(documents.InstructionPrecedence.AuthorityTiers, func(i, j int) bool {
		return documents.InstructionPrecedence.AuthorityTiers[i].Priority < documents.InstructionPrecedence.AuthorityTiers[j].Priority
	})
	for index := range documents.InstructionPrecedence.AuthorityTiers {
		sort.Strings(documents.InstructionPrecedence.AuthorityTiers[index].Content)
	}
	sort.Strings(documents.InstructionPrecedence.ConflictRules)
	sort.Strings(documents.Decisions.AcceptedFromOwner)
	sort.Slice(documents.Decisions.Open, func(i, j int) bool { return documents.Decisions.Open[i].ID < documents.Decisions.Open[j].ID })
	for index := range documents.SourceManifest.Files {
		documents.SourceManifest.Files[index].Path = stableSourceReference(documents.SourceManifest.Files[index].Path)
	}
	sort.Slice(documents.SourceManifest.Files, func(i, j int) bool {
		if documents.SourceManifest.Files[i].Path == documents.SourceManifest.Files[j].Path {
			return documents.SourceManifest.Files[i].SHA256 < documents.SourceManifest.Files[j].SHA256
		}
		return documents.SourceManifest.Files[i].Path < documents.SourceManifest.Files[j].Path
	})
	documents.SourceManifest.GeneratedAt = ""
}

func stableSourceReference(value string) string {
	value = strings.ReplaceAll(value, `\`, "/")
	if marker := strings.Index(value, "/organizacion/"); marker >= 0 {
		return "organizacion/" + strings.TrimPrefix(value[marker+len("/organizacion/"):], "/")
	}
	return filepath.Base(value)
}

func materialize(documents parsedDocuments) Snapshot {
	organizationID := documents.Organization.Organization.ID
	organization := Organization{
		ID: organizationID, DisplayName: documents.Organization.Organization.DisplayName,
		OwnerRoleID: documents.Organization.Organization.OwnerRoleID, CEORoleID: documents.Organization.Executive.CEORoleID,
		RuntimeTarget: documents.Organization.Organization.RuntimeTarget, StateStoreTarget: documents.Organization.Organization.StateStoreTarget,
		CellsAreExternal: documents.Organization.Organization.CellsAreExternal, SchemaVersion: documents.Organization.SchemaVersion,
		DocumentStatus: documents.Organization.DocumentStatus,
	}
	units := make([]Unit, 0, len(documents.Organization.OperationalDepartments)+len(documents.Organization.TransversalUnits))
	for _, department := range documents.Organization.OperationalDepartments {
		leader := department.LeaderRoleID
		agentPath := department.AgentPath
		units = append(units, Unit{OrganizationID: organizationID, ID: department.ID, DisplayName: displayNameFromID(department.ID),
			Kind: "operational_department", Operational: true, Leaderless: false, LeaderRoleID: &leader, AgentPath: &agentPath,
			CanonicalStatus: documents.Organization.DocumentStatus})
	}
	rolesByUnit := make(map[string][]roleDefinition)
	for _, role := range documents.Roles.Roles {
		rolesByUnit[role.Department] = append(rolesByUnit[role.Department], role)
	}
	for _, transversal := range documents.Organization.TransversalUnits {
		var agentPath *string
		for _, role := range rolesByUnit[transversal.ID] {
			if role.DepartmentAgentPath != nil {
				copyValue := *role.DepartmentAgentPath
				agentPath = &copyValue
				break
			}
		}
		var schedule, timezone *string
		if transversal.HourlySchedule != "" {
			value := transversal.HourlySchedule
			schedule = &value
		}
		if transversal.Timezone != "" {
			value := transversal.Timezone
			timezone = &value
		}
		units = append(units, Unit{OrganizationID: organizationID, ID: transversal.ID, DisplayName: displayNameFromID(transversal.ID),
			Kind: transversal.Kind, Operational: false, Leaderless: transversal.Leaderless, AgentPath: agentPath,
			Schedule: schedule, Timezone: timezone, CanonicalStatus: documents.Organization.DocumentStatus})
	}
	roles := make([]Role, 0, len(documents.Roles.Roles))
	reporting := make([]ReportingLine, 0)
	for _, source := range documents.Roles.Roles {
		enabled := source.Status == "imported_source"
		executable := enabled && source.RuntimeKind != "human"
		role := Role{
			OrganizationID: organizationID, ID: source.ID, UnitID: source.Department, RoleSlug: source.Role,
			DisplayName: source.DisplayName, RuntimeKind: source.RuntimeKind, AuthorityClass: source.AuthorityClass,
			CanonicalLeader: source.CanonicalLeader, LegacyFrontmatterLeader: source.LegacyFrontmatterLeader,
			ModelPolicy: cloneString(source.ModelPolicy), ProfilePath: cloneString(source.ProfilePath), DepartmentAgentPath: cloneString(source.DepartmentAgentPath),
			MemoryDomain: cloneString(source.MemoryDomain), MemoryPath: cloneString(source.MemoryPath), Summary: cloneString(source.Summary),
			ReportsToSourceText: cloneString(source.ReportsToSourceText), RAGTopicsSourceText: cloneString(source.RAGTopicsSourceText),
			SourceStatus: source.Status, Enabled: enabled, Executable: executable,
		}
		role.SourceHash = hashValue(roleForHash(role))
		roles = append(roles, role)
		for _, target := range source.ReportsTo {
			reporting = append(reporting, ReportingLine{OrganizationID: organizationID, RoleID: source.ID, ReportsToRoleID: target, Relationship: "reports_to"})
		}
	}
	sort.Slice(units, func(i, j int) bool { return units[i].ID < units[j].ID })
	sort.Slice(roles, func(i, j int) bool { return roles[i].ID < roles[j].ID })
	sort.Slice(reporting, func(i, j int) bool { return reportingKey(reporting[i]) < reportingKey(reporting[j]) })
	snapshot := Snapshot{Organization: organization, Units: units, Roles: roles, ReportingLines: reporting}
	snapshot.Counts = countSnapshot(snapshot)
	return snapshot
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func displayNameFromID(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '_' || r == '-' })
	for index, part := range parts {
		runes := []rune(part)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
			parts[index] = string(runes)
		}
	}
	return strings.Join(parts, " ")
}

func hashValue(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
