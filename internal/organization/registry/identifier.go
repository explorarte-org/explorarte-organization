package registry

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

var identifierSegment = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)

func ValidateUnitID(value string) error { return validateIdentifier(value, 1) }
func ValidateRoleID(value string) error { return validateIdentifier(value, 2) }

func validateIdentifier(value string, segments int) error {
	if value == "" {
		return fmt.Errorf("identifier cannot be empty")
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("identifier %q is not normalized", value)
	}
	parts := strings.Split(value, "/")
	if len(parts) != segments {
		return fmt.Errorf("identifier %q must contain exactly %d segment(s)", value, segments)
	}
	for _, part := range parts {
		if !identifierSegment.MatchString(part) {
			return fmt.Errorf("identifier segment %q is invalid", part)
		}
	}
	return nil
}

func ValidateReferencePath(value string) error {
	if value == "" {
		return nil
	}
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("path contains NUL")
	}
	if strings.Contains(value, `\`) {
		return fmt.Errorf("path must use forward slashes")
	}
	if strings.HasPrefix(value, "/") {
		return fmt.Errorf("path must be relative")
	}
	if path.Clean(value) != value || value == "." || strings.HasPrefix(value, "../") || strings.Contains(value, "/../") {
		return fmt.Errorf("path %q is not a normalized relative reference", value)
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("path %q contains an invalid segment", value)
		}
	}
	return nil
}
