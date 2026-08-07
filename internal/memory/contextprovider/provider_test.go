package contextprovider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
)

type fakeRepository struct {
	entries map[string]memory.Entry
	listed  []memory.Entry
}

func (f *fakeRepository) CreateCandidate(context.Context, memory.Entry, string) (memory.Entry, bool, error) {
	return memory.Entry{}, false, errors.New("not implemented")
}
func (f *fakeRepository) Get(_ context.Context, id string) (memory.Entry, error) {
	entry, ok := f.entries[id]
	if !ok {
		return memory.Entry{}, memory.ErrEntryNotFound
	}
	return entry, nil
}
func (f *fakeRepository) Save(context.Context, memory.SaveCommand) (memory.Entry, error) {
	return memory.Entry{}, errors.New("not implemented")
}
func (f *fakeRepository) ListApproved(context.Context, memory.ApprovedFilter) ([]memory.Entry, error) {
	return append([]memory.Entry(nil), f.listed...), nil
}

func approvedEntry(id, role string, now time.Time) memory.Entry {
	reviewed := now.Add(time.Minute)
	return memory.Entry{
		ID:             id,
		OrganizationID: "explorarte",
		RoleID:         role,
		Category:       "incident_learning",
		Problem:        "A real failure was observed.",
		Correction:     "Use the verified corrective procedure.",
		SourceRunID:    42,
		EvidenceRefs:   []memory.EvidenceRef{{Reference: "evidence:42", Digest: "abc"}},
		Status:         memory.StatusApproved,
		ProposedBy:     role,
		ReviewerID:     "empresa/human",
		Admission: memory.AdmissionAttestation{
			DataClass:      memory.DataOrganizational,
			AttestedBy:     role,
			SourceBoundary: "organization",
			EvidenceRef:    "admission:42",
			AttestedAt:     now.Add(-time.Minute),
		},
		Revision:   2,
		CreatedAt:  now,
		UpdatedAt:  reviewed,
		ReviewedAt: &reviewed,
	}
}

func TestListApprovedProducesUntrustedNonGrantingMemory(t *testing.T) {
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	role := "ingenieria_ia/orquestador"
	entry := approvedEntry("mem-1", role, now)
	repository := &fakeRepository{entries: map[string]memory.Entry{entry.ID: entry}, listed: []memory.Entry{entry}}
	provider, err := New(repository, 5)
	if err != nil {
		t.Fatal(err)
	}
	records, err := provider.ListApproved(context.Background(), contextengine.BuildRequest{OrganizationID: "explorarte", ActorRoleID: role})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records=%d want 1", len(records))
	}
	record := records[0]
	if record.Kind != contextengine.SourceApprovedMemory || record.AuthorityTier != contextengine.TierApprovedMemory {
		t.Fatalf("unexpected source identity: %+v", record)
	}
	if record.InstructionClass != contextengine.InstructionData || record.TrustClass != contextengine.TrustUntrusted || record.MayGrantCapabilities {
		t.Fatalf("memory escaped untrusted-data boundary: %+v", record)
	}
	if record.DataClass != contextengine.DataOrganizational {
		t.Fatalf("data class=%s", record.DataClass)
	}
	if err := contextengine.ValidateSourceMetadata(record); err != nil {
		t.Fatalf("source metadata invalid: %v", err)
	}
}

func TestListApprovedRejectsRepositoryScopeLeak(t *testing.T) {
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	entry := approvedEntry("mem-1", "marketing/estratega_crecimiento", now)
	repository := &fakeRepository{entries: map[string]memory.Entry{entry.ID: entry}, listed: []memory.Entry{entry}}
	provider, err := New(repository, 5)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.ListApproved(context.Background(), contextengine.BuildRequest{OrganizationID: "explorarte", ActorRoleID: "ingenieria_ia/orquestador"})
	if err == nil {
		t.Fatal("provider accepted memory from another role")
	}
}

func TestValidateVersionDetectsDeprecationAndContentDrift(t *testing.T) {
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	role := "ingenieria_ia/orquestador"
	entry := approvedEntry("mem-1", role, now)
	repository := &fakeRepository{entries: map[string]memory.Entry{entry.ID: entry}, listed: []memory.Entry{entry}}
	provider, err := New(repository, 5)
	if err != nil {
		t.Fatal(err)
	}
	records, err := provider.ListApproved(context.Background(), contextengine.BuildRequest{OrganizationID: "explorarte", ActorRoleID: role})
	if err != nil {
		t.Fatal(err)
	}
	expected := records[0]
	if err := provider.ValidateVersion(context.Background(), expected); err != nil {
		t.Fatalf("unchanged approved memory drifted: %v", err)
	}

	deprecated := entry
	deprecated.Status = memory.StatusDeprecated
	deprecated.Revision++
	deprecated.UpdatedAt = deprecated.UpdatedAt.Add(time.Minute)
	repository.entries[entry.ID] = deprecated
	if err := provider.ValidateVersion(context.Background(), expected); err == nil {
		t.Fatal("deprecated memory did not invalidate snapshot")
	}

	changed := entry
	changed.Correction = "Different correction."
	repository.entries[entry.ID] = changed
	if err := provider.ValidateVersion(context.Background(), expected); err == nil {
		t.Fatal("changed memory did not invalidate snapshot")
	}
}

func TestProviderNeverMapsForbiddenDataClasses(t *testing.T) {
	for _, class := range []memory.DataClass{memory.DataClinical, memory.DataSecret} {
		if _, err := mapDataClass(class); err == nil {
			t.Fatalf("forbidden class %s unexpectedly mapped", class)
		}
	}
}
