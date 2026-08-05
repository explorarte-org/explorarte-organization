package contextengine

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	canonicalID   = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)
	roleID        = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*/[a-z0-9]+(?:[-_][a-z0-9]+)*$`)
	skillID       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	githubOrigin  = regexp.MustCompile(`^github:[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func ValidateBuildRequest(request BuildRequest) error {
	if !canonicalID.MatchString(request.OrganizationID) {
		return Reject(ReasonInvalidRequest, "organization_id", "organization ID is invalid")
	}
	if request.OrganizationRevisionID <= 0 {
		return Reject(ReasonInvalidRequest, "organization_revision_id", "organization revision must be positive")
	}
	if !roleID.MatchString(request.ActorRoleID) {
		return Reject(ReasonInvalidRequest, "actor_role_id", "actor role ID is invalid")
	}
	if length := len(strings.TrimSpace(request.Purpose)); length < 1 || length > 240 {
		return Reject(ReasonInvalidRequest, "purpose", "purpose length must be between 1 and 240 bytes")
	}
	if length := len(strings.TrimSpace(request.IdempotencyKey)); length < 1 || length > 200 {
		return Reject(ReasonInvalidRequest, "idempotency_key", "idempotency key length must be between 1 and 200 bytes")
	}
	for name, value := range map[string]string{"project_ref": request.ProjectRef, "task_ref": request.TaskRef, "correlation_id": request.CorrelationID, "causation_id": request.CausationID} {
		if len(value) > 240 || strings.ContainsRune(value, 0) {
			return Reject(ReasonInvalidRequest, name, name+" is invalid")
		}
	}
	seen := map[string]struct{}{}
	for _, id := range request.RequestedSkillIDs {
		if !skillID.MatchString(id) {
			return Reject(ReasonInvalidRequest, id, "requested skill ID is invalid")
		}
		if _, exists := seen[id]; exists {
			return Reject(ReasonStructuredConflict, id, "requested skill is duplicated")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func ValidateSourceMetadata(source SourceRecord) error {
	priority, ok := AuthorityPriority(source.AuthorityTier)
	if !ok || priority != source.AuthorityPriority {
		return Reject(ReasonPrecedenceInvalid, source.Reference, "authority tier and priority do not match")
	}
	if _, ok = RenderRank(source.AuthorityTier); !ok {
		return Reject(ReasonPrecedenceInvalid, source.Reference, "authority tier is unknown")
	}
	if !allowedInstructionClass(source.InstructionClass) || !allowedTrustClass(source.TrustClass) {
		return Reject(ReasonStructuredConflict, source.Reference, "instruction or trust class is invalid")
	}
	if err := ValidateDataClass(source.DataClass, source.Reference); err != nil {
		return err
	}
	if source.MayGrantCapabilities && source.AuthorityPriority != 1 && source.AuthorityPriority != 2 {
		return Reject(ReasonUnsafeInstructionSource, source.Reference, "source cannot grant capabilities at this authority priority")
	}
	if source.AuthorityPriority == 0 && source.MayGrantCapabilities {
		return Reject(ReasonUnsafeInstructionSource, source.Reference, "immutable safety does not grant capabilities")
	}
	if strings.TrimSpace(source.Reference) == "" || len(source.Reference) > 500 || strings.ContainsRune(source.Reference, 0) {
		return Reject(ReasonInvalidRequest, source.Reference, "source reference is invalid")
	}
	if source.ContentHash == "" || !digestPattern.MatchString(source.ContentHash) {
		return Reject(ReasonStructuredConflict, source.Reference, "source content hash is invalid")
	}
	if source.Included && len(source.Content) == 0 {
		return Reject(ReasonStructuredConflict, source.Reference, "included source has no content")
	}
	return nil
}

func ValidateDataClass(class DataClass, reference string) error {
	switch class {
	case DataPublic, DataOrganizational, DataSanitized:
		return nil
	case DataSecret:
		return Reject(ReasonSecretDataRejected, reference, "secret data is forbidden in context snapshots")
	case DataClinical:
		return Reject(ReasonClinicalDataRejected, reference, "clinical data is forbidden in context snapshots")
	default:
		return Reject(ReasonForbiddenDataClass, reference, "unknown or forbidden data class")
	}
}

func ValidateProfile(document LoadedDocument, expectedDepartment, expectedRole, expectedMemoryDomain string) error {
	if document.Frontmatter == nil {
		return Reject(ReasonInvalidFrontmatter, document.Path, "profile frontmatter is required")
	}
	department, err := requiredString(document.Frontmatter, "departamento")
	if err != nil {
		return Reject(ReasonInvalidFrontmatter, document.Path, err.Error())
	}
	role, err := requiredString(document.Frontmatter, "rol")
	if err != nil {
		return Reject(ReasonInvalidFrontmatter, document.Path, err.Error())
	}
	memory, err := requiredString(document.Frontmatter, "dominio_memoria")
	if err != nil {
		return Reject(ReasonInvalidFrontmatter, document.Path, err.Error())
	}
	if _, err = requiredValue(document.Frontmatter, "agente_base"); err != nil {
		return Reject(ReasonInvalidFrontmatter, document.Path, err.Error())
	}
	if department != expectedDepartment || role != expectedRole || memory != expectedMemoryDomain {
		return Reject(ReasonSourceIdentityMismatch, document.Path, fmt.Sprintf("profile identity is %s/%s memory=%s, expected %s/%s memory=%s", department, role, memory, expectedDepartment, expectedRole, expectedMemoryDomain))
	}
	if len(strings.TrimSpace(string(document.Body))) == 0 {
		return Reject(ReasonInvalidFrontmatter, document.Path, "profile body is empty")
	}
	return nil
}

func ValidateAgent(document LoadedDocument) error {
	if len(strings.TrimSpace(string(document.Normalized))) == 0 {
		return Reject(ReasonInvalidFrontmatter, document.Path, "AGENT document is empty")
	}
	return nil
}

func ValidateSkill(document LoadedDocument, expected SkillRecord) ([]string, error) {
	if document.Frontmatter == nil {
		return nil, Reject(ReasonInvalidFrontmatter, document.Path, "skill frontmatter is required")
	}
	name, err := requiredString(document.Frontmatter, "name")
	if err != nil {
		return nil, Reject(ReasonInvalidFrontmatter, document.Path, err.Error())
	}
	if !skillID.MatchString(name) || name != expected.ID {
		return nil, Reject(ReasonSourceIdentityMismatch, document.Path, "skill name does not match expected ID")
	}
	description, err := requiredString(document.Frontmatter, "description")
	if err != nil {
		return nil, Reject(ReasonInvalidFrontmatter, document.Path, err.Error())
	}
	if len(description) < 10 || len(description) > 2000 {
		return nil, Reject(ReasonInvalidFrontmatter, document.Path, "skill description must contain 10 to 2000 bytes")
	}
	department, err := requiredString(document.Frontmatter, "departamento")
	if err != nil {
		return nil, Reject(ReasonInvalidFrontmatter, document.Path, err.Error())
	}
	role, err := requiredString(document.Frontmatter, "rol")
	if err != nil {
		return nil, Reject(ReasonInvalidFrontmatter, document.Path, err.Error())
	}
	memory, err := requiredString(document.Frontmatter, "dominio_memoria")
	if err != nil {
		return nil, Reject(ReasonInvalidFrontmatter, document.Path, err.Error())
	}
	if department != expected.Department || department+"/"+role != expected.RoleID || memory != expected.MemoryDomain {
		return nil, Reject(ReasonSourceIdentityMismatch, document.Path, "skill owner or memory domain does not match canonical assignment")
	}
	origin, err := requiredString(document.Frontmatter, "origen")
	if err != nil {
		return nil, Reject(ReasonInvalidFrontmatter, document.Path, err.Error())
	}
	if origin != "interno" && !githubOrigin.MatchString(origin) {
		return nil, Reject(ReasonInvalidFrontmatter, document.Path, "skill origin must be interno or github:<owner>/<repo>")
	}
	protocol, err := requiredString(document.Frontmatter, "protocolo_base")
	if err != nil {
		return nil, Reject(ReasonInvalidFrontmatter, document.Path, err.Error())
	}
	switch protocol {
	case "verificacion_estado", "protocolo_verificacion", "none":
	default:
		return nil, Reject(ReasonInvalidFrontmatter, document.Path, "skill protocol_base is not recognized")
	}
	if verifier, exists := document.Frontmatter["verificador"]; exists && verifier != nil {
		switch value := verifier.(type) {
		case string:
			if strings.TrimSpace(value) == "" || len(value) > 240 {
				return nil, Reject(ReasonInvalidFrontmatter, document.Path, "skill verifier reference is invalid")
			}
		case map[string]any:
			if len(value) == 0 || len(value) > 16 {
				return nil, Reject(ReasonInvalidFrontmatter, document.Path, "skill verifier structure is invalid")
			}
		default:
			return nil, Reject(ReasonInvalidFrontmatter, document.Path, "skill verifier must be null, string, or mapping")
		}
	}
	if rawReferences, exists := document.Frontmatter["references"]; exists {
		var references []string
		switch value := rawReferences.(type) {
		case []any:
			for _, item := range value {
				text, ok := item.(string)
				if !ok {
					return nil, Reject(ReasonInvalidFrontmatter, document.Path, "skill references must be strings")
				}
				references = append(references, text)
			}
		case []string:
			references = append(references, value...)
		default:
			return nil, Reject(ReasonInvalidFrontmatter, document.Path, "skill references must be a sequence")
		}
		directory := filepath.Dir(document.Path)
		for _, reference := range references {
			if filepath.IsAbs(reference) || strings.ContainsRune(reference, 0) {
				return nil, Reject(ReasonSourcePathEscape, reference, "skill reference must be relative")
			}
			clean := filepath.Clean(reference)
			combined := filepath.Join(directory, clean)
			relative, relErr := filepath.Rel(directory, combined)
			if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return nil, Reject(ReasonSourcePathEscape, reference, "skill reference escapes skill directory")
			}
		}
	}
	if len(strings.TrimSpace(string(document.Body))) == 0 {
		return nil, Reject(ReasonInvalidFrontmatter, document.Path, "skill body is empty")
	}
	warnings := []string{}
	if lines := strings.Count(string(document.Normalized), "\n") + 1; lines > 500 {
		warnings = append(warnings, fmt.Sprintf("skill contains %d lines; manual review recommended", lines))
	}
	return warnings, nil
}

func ValidateSkillLifecycle(record SkillRecord) error {
	if record.Lifecycle != SkillActive {
		return Reject(ReasonSkillNotActive, record.ID, "skill is not active")
	}
	if !record.Assigned {
		return Reject(ReasonSkillNotAssigned, record.ID, "skill is not explicitly assigned to the role")
	}
	return nil
}

func NormalizeSkillIDs(ids []string) []string {
	values := append([]string(nil), ids...)
	sort.Strings(values)
	return values
}

func requiredString(values map[string]any, key string) (string, error) {
	value, err := requiredValue(values, key)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return "", fmt.Errorf("frontmatter field %s must be a non-empty string", key)
	}
	return strings.TrimSpace(text), nil
}

func requiredValue(values map[string]any, key string) (any, error) {
	value, exists := values[key]
	if !exists || value == nil {
		return nil, fmt.Errorf("frontmatter field %s is required", key)
	}
	return value, nil
}

func allowedInstructionClass(value InstructionClass) bool {
	switch value {
	case InstructionImmutableConstraint, InstructionOwnerConstraint, InstructionCanonicalPolicy, InstructionOrganizational, InstructionRole, InstructionScoped, InstructionProcedure, InstructionData:
		return true
	default:
		return false
	}
}

func allowedTrustClass(value TrustClass) bool {
	switch value {
	case TrustImmutable, TrustAuthoritative, TrustApproved, TrustScoped, TrustUntrusted:
		return true
	default:
		return false
	}
}

func IsOperational(err error) bool {
	if err == nil {
		return false
	}
	var rejection *RejectionError
	return !errors.As(err, &rejection) && !errors.Is(err, ErrIdempotencyConflict) && !errors.Is(err, ErrSnapshotNotFound) && !errors.Is(err, ErrSnapshotInvalidated) && !errors.Is(err, ErrSnapshotStale)
}
