package contextengine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
)

func DigestCanonicalBytes(canonical []byte) string { return digest(canonical) }
func DigestMarkdown(normalized []byte) string      { return digest(normalized) }

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

type CanonicalBuildRequest struct {
	Request             BuildRequest
	PrecedenceHash      string
	CanonicalBundleHash string
	Sources             []SourceRecord
	MaxTotalBytes       int
	MaxSegmentBytes     int
	MaxSegments         int
	MaxSkills           int
	MaxMemorySegments   int
	MaxRAGSegments      int
}

func DigestBuildRequest(input CanonicalBuildRequest) string {
	var out bytes.Buffer
	writeField(&out, "organization_id", input.Request.OrganizationID)
	writeInt(&out, "organization_revision_id", input.Request.OrganizationRevisionID)
	writeField(&out, "actor_role_id", input.Request.ActorRoleID)
	writeField(&out, "purpose", input.Request.Purpose)
	writeField(&out, "project_ref", input.Request.ProjectRef)
	writeField(&out, "task_ref", input.Request.TaskRef)
	skills := append([]string(nil), input.Request.RequestedSkillIDs...)
	sort.Strings(skills)
	for _, skill := range skills {
		writeField(&out, "requested_skill_id", skill)
	}
	writeField(&out, "precedence_hash", input.PrecedenceHash)
	writeField(&out, "canonical_bundle_hash", input.CanonicalBundleHash)
	writeInt(&out, "max_total_bytes", int64(input.MaxTotalBytes))
	writeInt(&out, "max_segment_bytes", int64(input.MaxSegmentBytes))
	writeInt(&out, "max_segments", int64(input.MaxSegments))
	writeInt(&out, "max_skills", int64(input.MaxSkills))
	writeInt(&out, "max_memory_segments", int64(input.MaxMemorySegments))
	writeInt(&out, "max_rag_segments", int64(input.MaxRAGSegments))
	sources := append([]SourceRecord(nil), input.Sources...)
	sort.Slice(sources, func(i, j int) bool {
		ri, _ := RenderRank(sources[i].AuthorityTier)
		rj, _ := RenderRank(sources[j].AuthorityTier)
		if ri != rj {
			return ri < rj
		}
		if sources[i].Reference != sources[j].Reference {
			return sources[i].Reference < sources[j].Reference
		}
		return sources[i].ContentHash < sources[j].ContentHash
	})
	for _, source := range sources {
		writeField(&out, "source_kind", string(source.Kind))
		writeField(&out, "source_reference", source.Reference)
		writeField(&out, "source_version", source.Version)
		writeField(&out, "source_tier", string(source.AuthorityTier))
		writeInt(&out, "source_priority", int64(source.AuthorityPriority))
		writeField(&out, "source_instruction_class", string(source.InstructionClass))
		writeField(&out, "source_trust", string(source.TrustClass))
		writeField(&out, "source_data_class", string(source.DataClass))
		writeBool(&out, "source_may_grant", source.MayGrantCapabilities)
		writeField(&out, "source_hash", source.ContentHash)
	}
	return digest(out.Bytes())
}

func writeField(out *bytes.Buffer, name, value string) {
	out.WriteString(strconv.Itoa(len(name)))
	out.WriteByte(':')
	out.WriteString(name)
	out.WriteByte('=')
	out.WriteString(strconv.Itoa(len(value)))
	out.WriteByte(':')
	out.WriteString(value)
	out.WriteByte(';')
}

func writeInt(out *bytes.Buffer, name string, value int64) {
	writeField(out, name, strconv.FormatInt(value, 10))
}
func writeBool(out *bytes.Buffer, name string, value bool) {
	writeField(out, name, strconv.FormatBool(value))
}
