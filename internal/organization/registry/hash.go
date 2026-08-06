package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func hashDocuments(documents parsedDocuments) (map[string]string, string, error) {
	values := map[string]any{
		"organization.yaml":           documents.Organization,
		"role-catalog.yaml":           documents.Roles,
		"leader-worker-map.yaml":      documents.LeaderWorker,
		"model-routing.yaml":          documents.ModelRouting,
		"model-egress-policy.yaml":    documents.ModelEgress,
		"capability-matrix.yaml":      documents.Capabilities,
		"instruction-precedence.yaml": documents.InstructionPrecedence,
		"decisions-required.yaml":     documents.Decisions,
		"source-manifest.yaml":        documents.SourceManifest,
	}
	hashes := make(map[string]string, len(values))
	for _, name := range requiredCanonicalFiles {
		body, err := json.Marshal(values[name])
		if err != nil {
			return nil, "", fmt.Errorf("marshal canonical document %s: %w", name, err)
		}
		digest := sha256.Sum256(body)
		hashes[name] = hex.EncodeToString(digest[:])
	}
	var aggregate strings.Builder
	for _, name := range requiredCanonicalFiles {
		aggregate.WriteString(name)
		aggregate.WriteByte(0)
		aggregate.WriteString(hashes[name])
		aggregate.WriteByte('\n')
	}
	digest := sha256.Sum256([]byte(aggregate.String()))
	return hashes, hex.EncodeToString(digest[:]), nil
}

func countSnapshot(snapshot Snapshot) Counts {
	counts := Counts{Organizations: 1, Units: len(snapshot.Units), Roles: len(snapshot.Roles), ReportingRelationships: len(snapshot.ReportingLines)}
	for _, unit := range snapshot.Units {
		if unit.Operational {
			counts.OperationalUnits++
		} else {
			counts.TransversalUnits++
		}
	}
	for _, role := range snapshot.Roles {
		switch role.SourceStatus {
		case "imported_source":
			counts.ImportedRoles++
		case "proposed_profile_required":
			counts.ProposedRoles++
		}
	}
	return counts
}

func schemaVersions(snapshot Snapshot) map[string]string {
	result := make(map[string]string, len(snapshot.Documents))
	for _, document := range snapshot.Documents {
		if document.SchemaVersion != "" {
			result[document.Path] = document.SchemaVersion
		}
	}
	return result
}

func documentHashes(snapshot Snapshot) map[string]string {
	result := make(map[string]string, len(snapshot.Documents))
	for _, document := range snapshot.Documents {
		result[document.Path] = document.SemanticHash
	}
	return result
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
