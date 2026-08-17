package tasks

import "regexp"

// TaskClassGeneralWork is the safe explicit default for a newly created
// task whose caller did not supply a TaskClass (M1.3 section 4): never
// left empty for a newly persisted row.
const TaskClassGeneralWork = "general.work"

// TaskClassLegacyUnspecified is the durable migration value for a
// historical task created before TaskClass existed (M1.3 section 3). It
// is a stored FACT about a pre-M1.3 row, never a runtime classifier: no
// productive code path is permitted to assign this value to a NEWLY
// created task.
const TaskClassLegacyUnspecified = "legacy.unspecified"

// taskClassPattern is the one validated canonical syntax for TaskClass
// (M1.3 section 4): lowercase, dotted semantic identifier form, at least
// two segments (so a bare word can never silently pass as a class),
// alnum/underscore within a segment, no whitespace. A TaskClass string is
// classification metadata, never authority -- this pattern only bounds
// syntax, it grants nothing.
var taskClassPattern = regexp.MustCompile(`^[a-z0-9]+(?:_[a-z0-9]+)*(?:\.[a-z0-9]+(?:_[a-z0-9]+)*)+$`)

const maxTaskClassBytes = 100

// ValidTaskClass reports whether s is syntactically a valid TaskClass:
// non-empty, bounded, lowercase dotted identifier form. It does not, and
// must never, consult any registry, role, catalog, or model output --
// syntax only.
func ValidTaskClass(s string) bool {
	return len(s) > 0 && len(s) <= maxTaskClassBytes && taskClassPattern.MatchString(s)
}
