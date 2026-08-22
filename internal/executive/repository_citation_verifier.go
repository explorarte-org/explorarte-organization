package executive

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// citationPattern matches a repository citation as the evidence package emits
// it: repository://<repo>@<sha>/<path>#L<start>-L<end>.
var citationPattern = regexp.MustCompile(`repository://[A-Za-z0-9._-]+@[0-9a-f]{40}/[^\s"'` + "`" + `,;)\]]+#L\d+-L\d+`)

// SnapshotSourceReader reports the sources a context snapshot actually
// carried, and whether each one reached the model.
type SnapshotSourceReader interface {
	SnapshotSources(ctx context.Context, snapshotID int64) ([]SnapshotSource, error)
}

// SnapshotSource is one source of a durable context snapshot.
type SnapshotSource struct {
	Kind      string
	Reference string
	Version   string
	// Included is whether it survived assembly. A source that was known and
	// then dropped for budget is not something the model read.
	Included bool
}

// VerifiedCitation is a repository citation the host has confirmed was really
// in front of the model that used it.
//
// TaskID and InvocationID are part of the fact, not decoration. "R is
// authorized" is not a statement anything can act on: authorization belongs to
// a citation AND the model that used it, and dropping the second half is how a
// claim by a designer who never saw a file would inherit the grounding of one
// who did.
type VerifiedCitation struct {
	Reference    string
	BaseSHA      string
	TaskID       int64
	InvocationID int64
	// ResultDigest names the exact bytes the citation was found in. Without
	// it, a reference extracted from one version of a deliverable could be
	// published under the digest of another.
	ResultDigest string
}

// VerifyRepositoryCitations answers the only questions a host can answer about
// a citation, and refuses to answer any others.
//
// It does not judge whether "this function has two responsibilities" is true.
// That is the reviewer's work, and a host that started ruling on it would be
// quietly replacing an adversarial judgement with a mechanical one. What the
// host can settle is provenance, and provenance is exactly what a reviewer
// cannot check for itself:
//
//	did this citation exist at all
//	was it in the context THIS model was given
//	was it repository evidence rather than something else
//	did it cite the commit the design is about
//	did it actually reach the model, rather than being dropped
//
// Everything the reviewer then says rests on evidence nobody invented.
func (o *Orchestrator) VerifyRepositoryCitations(ctx context.Context, sources SnapshotSourceReader, snapshotID int64, baseSHA, text string, taskID, invocationID int64, resultDigest string) ([]VerifiedCitation, error) {
	if sources == nil || snapshotID <= 0 || baseSHA == "" {
		return nil, nil
	}
	available, err := sources.SnapshotSources(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	// Only sources that were repository evidence, about this commit, and
	// actually included. Each condition removes a different way a citation
	// could look real and not be.
	genuine := map[string]struct{}{}
	for _, source := range available {
		if source.Kind != "repository_evidence" {
			continue
		}
		if source.Version != baseSHA {
			continue
		}
		if !source.Included {
			continue
		}
		genuine[source.Reference] = struct{}{}
	}

	seen := map[string]struct{}{}
	verified := make([]VerifiedCitation, 0, len(genuine))
	for _, candidate := range citationPattern.FindAllString(text, -1) {
		if _, already := seen[candidate]; already {
			continue
		}
		if _, real := genuine[candidate]; !real {
			continue
		}
		seen[candidate] = struct{}{}
		verified = append(verified, VerifiedCitation{
			Reference: candidate, BaseSHA: baseSHA,
			TaskID: taskID, InvocationID: invocationID, ResultDigest: resultDigest,
		})
	}
	sort.Slice(verified, func(i, j int) bool { return verified[i].Reference < verified[j].Reference })
	return verified, nil
}

// RepositoryCitationsIn extracts the citations a text claims, verified or not.
//
// Used to tell "cited nothing" from "cited something that does not exist",
// which are different failures and deserve different findings.
func RepositoryCitationsIn(text string) []string {
	found := citationPattern.FindAllString(text, -1)
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(found))
	for _, candidate := range found {
		if _, already := seen[candidate]; already {
			continue
		}
		seen[candidate] = struct{}{}
		unique = append(unique, candidate)
	}
	sort.Strings(unique)
	return unique
}

// authorizedEvidenceRefs renders verified citations for the review bundle.
//
// References only. The reviewer receives what it may check a claim against and
// never the organizational source behind it: its context admits public and
// sanitized data, and widening that so it could read code would be an egress
// decision made as a side effect of a convenience.
func authorizedEvidenceRefs(verified []VerifiedCitation) []string {
	refs := make([]string, 0, len(verified))
	for _, citation := range verified {
		refs = append(refs, citation.Reference)
	}
	return refs
}

// describeUnverified is for the finding a reviewer emits when a claim rests on
// nothing.
func describeUnverified(claimed []string, verified []VerifiedCitation) string {
	if len(claimed) == 0 {
		return ""
	}
	good := map[string]struct{}{}
	for _, citation := range verified {
		good[citation.Reference] = struct{}{}
	}
	var bad []string
	for _, candidate := range claimed {
		if _, ok := good[candidate]; !ok {
			bad = append(bad, candidate)
		}
	}
	if len(bad) == 0 {
		return ""
	}
	return strings.Join(bad, ", ")
}

var _ = fmt.Sprintf
