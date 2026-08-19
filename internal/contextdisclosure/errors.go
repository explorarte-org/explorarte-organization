package contextdisclosure

import "errors"

// errOutOfBoundsRange is SliceInput.Validate's sentinel for a nonsensical
// range (negative offset, non-positive length) -- DESIGN.md §17's
// INVALID_REQUEST case for "Read exceeds bounds." Unexported: this is a
// pure-syntax validation detail for this package's own operation input
// types, not part of the frozen ContextToolResult/Outcome vocabulary a
// caller should match against -- a later slice translates this into
// Outcome/OutcomeInvalidRequest via NewDeniedResult, never surfaces it
// directly.
var errOutOfBoundsRange = errors.New("contextdisclosure: slice range must have offset >= 0 and length > 0")
