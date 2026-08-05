package contextengine

import (
	"context"
	"encoding/json"
	"errors"
)

const PortableSchemaVersion = "explorarte.context.v1"

type PortableRenderer struct{}

func NewRenderer() *PortableRenderer { return &PortableRenderer{} }

type portableRender struct {
	SchemaVersion          string            `json:"schema_version"`
	SnapshotID             int64             `json:"snapshot_id"`
	OrganizationID         string            `json:"organization_id"`
	OrganizationRevisionID int64             `json:"organization_revision_id"`
	ActorRoleID            string            `json:"actor_role_id"`
	Purpose                string            `json:"purpose"`
	ProjectRef             string            `json:"project_ref,omitempty"`
	TaskRef                string            `json:"task_ref,omitempty"`
	RequestHash            string            `json:"request_hash"`
	PrecedenceHash         string            `json:"precedence_hash"`
	CanonicalBundleHash    string            `json:"canonical_bundle_hash"`
	RenderedHashBasis      string            `json:"rendered_hash_basis"`
	Segments               []portableSegment `json:"segments"`
}

type portableSegment struct {
	Ordinal              int              `json:"ordinal"`
	RenderOrdinal        int              `json:"render_ordinal"`
	AuthorityPriority    int              `json:"authority_priority"`
	AuthorityTier        AuthorityTier    `json:"authority_tier"`
	SourceKind           SourceKind       `json:"source_kind"`
	SourceReference      string           `json:"source_reference"`
	SourceVersion        string           `json:"source_version"`
	InstructionClass     InstructionClass `json:"instruction_class"`
	TrustClass           TrustClass       `json:"trust_class"`
	DataClass            DataClass        `json:"data_class"`
	MayGrantCapabilities bool             `json:"may_grant_capabilities"`
	Included             bool             `json:"included"`
	OmissionReason       string           `json:"omission_reason,omitempty"`
	ContentHash          string           `json:"content_hash"`
	ByteCount            int              `json:"byte_count"`
	Content              []byte           `json:"content,omitempty"`
}

func (r *PortableRenderer) Render(ctx context.Context, snapshot Snapshot) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if snapshot.Status == SnapshotInvalidated {
		return nil, ErrSnapshotInvalidated
	}
	if snapshot.Status != "" && snapshot.Status != SnapshotReady {
		return nil, errors.New("cannot render snapshot with unknown status")
	}
	segments := make([]portableSegment, len(snapshot.Segments))
	for index, source := range snapshot.Segments {
		segments[index] = portableSegment{
			Ordinal: source.Ordinal, RenderOrdinal: source.RenderOrdinal, AuthorityPriority: source.AuthorityPriority,
			AuthorityTier: source.AuthorityTier, SourceKind: source.SourceKind, SourceReference: source.SourceReference,
			SourceVersion: source.SourceVersion, InstructionClass: source.InstructionClass, TrustClass: source.TrustClass,
			DataClass: source.DataClass, MayGrantCapabilities: source.MayGrantCapabilities, Included: source.Included,
			OmissionReason: source.OmissionReason, ContentHash: source.ContentHash, ByteCount: source.ByteCount,
		}
		if source.Included {
			segments[index].Content = append([]byte(nil), source.Content...)
		}
	}
	payload := portableRender{
		SchemaVersion: PortableSchemaVersion, SnapshotID: snapshot.ID, OrganizationID: snapshot.OrganizationID,
		OrganizationRevisionID: snapshot.OrganizationRevisionID, ActorRoleID: snapshot.ActorRoleID, Purpose: snapshot.Purpose,
		ProjectRef: snapshot.ProjectRef, TaskRef: snapshot.TaskRef, RequestHash: snapshot.RequestHash,
		PrecedenceHash: snapshot.PrecedenceHash, CanonicalBundleHash: snapshot.CanonicalBundleHash,
		RenderedHashBasis: "sha256_of_this_exact_document_with_self_hash_excluded", Segments: segments,
	}
	return json.Marshal(payload)
}
