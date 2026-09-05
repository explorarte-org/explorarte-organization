package contextprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// DivergenceRecord represents a metadata-only observation of a difference
// between Primary and Shadow skill providers.
// It never records SKILL.md bodies, prompts, or context content.
type DivergenceRecord struct {
	OrganizationID    string    `json:"organization_id"`
	DivergenceKey     string    `json:"divergence_key,omitempty"`
	RoleID            string    `json:"role_id"`
	SkillID           string    `json:"skill_id,omitempty"`
	Operation         string    `json:"operation"`
	Field             string    `json:"field,omitempty"`
	PrimaryValue      string    `json:"primary_value,omitempty"`
	ShadowValue       string    `json:"shadow_value,omitempty"`
	PrimaryVersion    string    `json:"primary_version,omitempty"`
	ShadowVersion     string    `json:"shadow_version,omitempty"`
	PrimarySourceHash string    `json:"primary_source_hash,omitempty"`
	ShadowSourceHash  string    `json:"shadow_source_hash,omitempty"`
	Reason            string    `json:"reason"`
	FirstObservedAt   time.Time `json:"first_observed_at,omitempty"`
	LastObservedAt    time.Time `json:"last_observed_at,omitempty"`
	ObservationCount  int64     `json:"observation_count,omitempty"`
	ObservedAt        time.Time `json:"observed_at"`
}

// ComputeDivergenceKey computes an immutable canonical SHA-256 hash representing the exact
// divergence identity across all relevant fields.
func ComputeDivergenceKey(r DivergenceRecord) string {
	payload := fmt.Sprintf("org=%s|role=%s|skill=%s|op=%s|field=%s|pv=%s|sv=%s|pver=%s|sver=%s|phash=%s|shash=%s",
		strings.TrimSpace(r.OrganizationID),
		strings.TrimSpace(r.RoleID),
		strings.TrimSpace(r.SkillID),
		strings.TrimSpace(r.Operation),
		strings.TrimSpace(r.Field),
		strings.TrimSpace(r.PrimaryValue),
		strings.TrimSpace(r.ShadowValue),
		strings.TrimSpace(r.PrimaryVersion),
		strings.TrimSpace(r.ShadowVersion),
		strings.TrimSpace(r.PrimarySourceHash),
		strings.TrimSpace(r.ShadowSourceHash),
	)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

type DivergenceRecorder interface {
	RecordDivergence(ctx context.Context, record DivergenceRecord) error
	ListDivergences(ctx context.Context, filters ...any) ([]DivergenceRecord, error)
}

type MemoryDivergenceRecorder struct {
	mu          sync.Mutex
	byKey       map[string]*DivergenceRecord
	divergences []DivergenceRecord
}

func NewMemoryDivergenceRecorder() *MemoryDivergenceRecorder {
	return &MemoryDivergenceRecorder{
		byKey:       make(map[string]*DivergenceRecord),
		divergences: []DivergenceRecord{},
	}
}

func (m *MemoryDivergenceRecorder) RecordDivergence(_ context.Context, d DivergenceRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	obsAt := d.LastObservedAt
	if obsAt.IsZero() {
		obsAt = d.ObservedAt
	}
	if obsAt.IsZero() {
		obsAt = now
	}
	d.ObservedAt = obsAt
	d.LastObservedAt = obsAt

	key := d.DivergenceKey
	if key == "" {
		key = ComputeDivergenceKey(d)
		d.DivergenceKey = key
	}

	if existing, found := m.byKey[key]; found {
		existing.LastObservedAt = obsAt
		existing.ObservedAt = obsAt
		existing.ObservationCount++
		return nil
	}

	d.FirstObservedAt = obsAt
	if d.ObservationCount <= 0 {
		d.ObservationCount = 1
	}
	copied := d
	m.byKey[key] = &copied
	m.divergences = append(m.divergences, d)
	return nil
}

func (m *MemoryDivergenceRecorder) ListDivergences(_ context.Context, filters ...any) ([]DivergenceRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]DivergenceRecord, 0, len(m.byKey))
	for _, rec := range m.byKey {
		out = append(out, *rec)
	}
	return out, nil
}

// ParityProvider runs Primary and Shadow providers.
// Primary strictly governs all outputs, context, and authority.
// Shadow comparisons record divergences metadata-only into Sink without altering runtime output.
//
// Invariant: Recorder persistence is best-effort relative to execution authority.
// Failures to record parity telemetry MUST NOT fail the Primary execution path,
// MUST NOT cause Canonical fallback, and MUST NOT grant authority to the Shadow provider.
type ParityProvider struct {
	Primary        contextengine.SkillProvider
	Shadow         contextengine.SkillProvider
	Sink           DivergenceRecorder
	OrganizationID string
}

func NewParityProvider(primary, shadow contextengine.SkillProvider, sink DivergenceRecorder, organizationID ...string) (*ParityProvider, error) {
	if primary == nil {
		return nil, fmt.Errorf("primary skill provider is required")
	}
	orgID := ""
	if len(organizationID) > 0 {
		orgID = organizationID[0]
	}
	return &ParityProvider{
		Primary:        primary,
		Shadow:         shadow,
		Sink:           sink,
		OrganizationID: orgID,
	}, nil
}

func (p *ParityProvider) ListActiveForRole(ctx context.Context, organizationID, roleID string) ([]contextengine.SkillRecord, error) {
	primaryRecords, primaryErr := p.Primary.ListActiveForRole(ctx, organizationID, roleID)

	if p.Shadow != nil && p.Sink != nil {
		shadowRecords, shadowErr := p.Shadow.ListActiveForRole(ctx, organizationID, roleID)
		p.compareLists(ctx, organizationID, roleID, primaryRecords, shadowRecords, primaryErr, shadowErr)
	}

	return primaryRecords, primaryErr
}

func (p *ParityProvider) GetActiveForRole(ctx context.Context, organizationID, roleID, skillID string) (contextengine.SkillRecord, error) {
	primaryRecord, primaryErr := p.Primary.GetActiveForRole(ctx, organizationID, roleID, skillID)

	if p.Shadow != nil && p.Sink != nil {
		shadowRecord, shadowErr := p.Shadow.GetActiveForRole(ctx, organizationID, roleID, skillID)
		p.compareSingle(ctx, organizationID, roleID, skillID, primaryRecord, shadowRecord, primaryErr, shadowErr)
	}

	return primaryRecord, primaryErr
}

func (p *ParityProvider) ValidateVersion(ctx context.Context, expected contextengine.SkillRecord) error {
	primaryErr := p.Primary.ValidateVersion(ctx, expected)

	if p.Shadow != nil && p.Sink != nil {
		shadowErr := p.Shadow.ValidateVersion(ctx, expected)
		if (primaryErr == nil && shadowErr != nil) || (primaryErr != nil && shadowErr == nil) {
			pErrStr := "nil"
			if primaryErr != nil {
				pErrStr = primaryErr.Error()
			}
			sErrStr := "nil"
			if shadowErr != nil {
				sErrStr = shadowErr.Error()
			}
			rec := DivergenceRecord{
				OrganizationID:    p.OrganizationID,
				RoleID:            expected.RoleID,
				SkillID:           expected.ID,
				Operation:         "ValidateVersion",
				Field:             "ValidateVersion",
				PrimaryValue:      pErrStr,
				ShadowValue:       sErrStr,
				PrimaryVersion:    expected.Version,
				ShadowVersion:     expected.Version,
				PrimarySourceHash: expected.SourceHash,
				ShadowSourceHash:  expected.SourceHash,
				Reason:            fmt.Sprintf("validation error mismatch: primary=%s shadow=%s", pErrStr, sErrStr),
				ObservedAt:        time.Now().UTC(),
			}
			rec.DivergenceKey = ComputeDivergenceKey(rec)
			_ = p.Sink.RecordDivergence(ctx, rec)
		}
	}

	return primaryErr
}

func (p *ParityProvider) compareLists(ctx context.Context, orgID, roleID string, prim, shad []contextengine.SkillRecord, primErr, shadErr error) {
	effectiveOrgID := orgID
	if effectiveOrgID == "" {
		effectiveOrgID = p.OrganizationID
	}

	if (primErr == nil && shadErr != nil) || (primErr != nil && shadErr == nil) {
		pErrStr := "nil"
		if primErr != nil {
			pErrStr = primErr.Error()
		}
		sErrStr := "nil"
		if shadErr != nil {
			sErrStr = shadErr.Error()
		}
		rec := DivergenceRecord{
			OrganizationID: effectiveOrgID,
			RoleID:         roleID,
			Operation:      "ListActiveForRole",
			Field:          "ListActiveForRole.Error",
			PrimaryValue:   pErrStr,
			ShadowValue:    sErrStr,
			Reason:         fmt.Sprintf("error divergence: primary=%s shadow=%s", pErrStr, sErrStr),
			ObservedAt:     time.Now().UTC(),
		}
		rec.DivergenceKey = ComputeDivergenceKey(rec)
		_ = p.Sink.RecordDivergence(ctx, rec)
	}

	if primErr != nil || shadErr != nil {
		return
	}

	pMap := make(map[string]contextengine.SkillRecord, len(prim))
	for _, r := range prim {
		pMap[r.ID] = r
	}
	sMap := make(map[string]contextengine.SkillRecord, len(shad))
	for _, r := range shad {
		sMap[r.ID] = r
	}

	allIDs := make(map[string]struct{})
	for id := range pMap {
		allIDs[id] = struct{}{}
	}
	for id := range sMap {
		allIDs[id] = struct{}{}
	}

	sortedIDs := make([]string, 0, len(allIDs))
	for id := range allIDs {
		sortedIDs = append(sortedIDs, id)
	}
	sort.Strings(sortedIDs)

	for _, id := range sortedIDs {
		pRec, pHas := pMap[id]
		sRec, sHas := sMap[id]

		if pHas && !sHas {
			rec := DivergenceRecord{
				OrganizationID:    effectiveOrgID,
				RoleID:            roleID,
				SkillID:           id,
				Operation:         "ListActiveForRole",
				Field:             "Presence",
				PrimaryValue:      "present",
				ShadowValue:       "absent",
				PrimaryVersion:    pRec.Version,
				ShadowVersion:     "",
				PrimarySourceHash: pRec.SourceHash,
				ShadowSourceHash:  "",
				Reason:            "skill present in primary but absent in shadow",
				ObservedAt:        time.Now().UTC(),
			}
			rec.DivergenceKey = ComputeDivergenceKey(rec)
			_ = p.Sink.RecordDivergence(ctx, rec)
			continue
		}
		if !pHas && sHas {
			rec := DivergenceRecord{
				OrganizationID:    effectiveOrgID,
				RoleID:            roleID,
				SkillID:           id,
				Operation:         "ListActiveForRole",
				Field:             "Presence",
				PrimaryValue:      "absent",
				ShadowValue:       "present",
				PrimaryVersion:    "",
				ShadowVersion:     sRec.Version,
				PrimarySourceHash: "",
				ShadowSourceHash:  sRec.SourceHash,
				Reason:            "skill absent in primary but present in shadow",
				ObservedAt:        time.Now().UTC(),
			}
			rec.DivergenceKey = ComputeDivergenceKey(rec)
			_ = p.Sink.RecordDivergence(ctx, rec)
			continue
		}

		p.compareFields(ctx, effectiveOrgID, roleID, id, "ListActiveForRole", pRec, sRec)
	}
}

func (p *ParityProvider) compareSingle(ctx context.Context, orgID, roleID, skillID string, pRec, sRec contextengine.SkillRecord, primErr, shadErr error) {
	effectiveOrgID := orgID
	if effectiveOrgID == "" {
		effectiveOrgID = p.OrganizationID
	}

	if (primErr == nil && shadErr != nil) || (primErr != nil && shadErr == nil) {
		pErrStr := "nil"
		if primErr != nil {
			pErrStr = primErr.Error()
		}
		sErrStr := "nil"
		if shadErr != nil {
			sErrStr = shadErr.Error()
		}
		rec := DivergenceRecord{
			OrganizationID: effectiveOrgID,
			RoleID:         roleID,
			SkillID:        skillID,
			Operation:      "GetActiveForRole",
			Field:          "GetActiveForRole.Error",
			PrimaryValue:   pErrStr,
			ShadowValue:    sErrStr,
			Reason:         fmt.Sprintf("error divergence on GetActiveForRole: primary=%s shadow=%s", pErrStr, sErrStr),
			ObservedAt:     time.Now().UTC(),
		}
		rec.DivergenceKey = ComputeDivergenceKey(rec)
		_ = p.Sink.RecordDivergence(ctx, rec)
		return
	}
	if primErr != nil || shadErr != nil {
		return
	}
	p.compareFields(ctx, effectiveOrgID, roleID, skillID, "GetActiveForRole", pRec, sRec)
}

func (p *ParityProvider) compareFields(ctx context.Context, orgID, roleID, skillID, operation string, pRec, sRec contextengine.SkillRecord) {
	checks := []struct {
		field string
		pval  string
		sval  string
	}{
		{"RoleID", pRec.RoleID, sRec.RoleID},
		{"Lifecycle", string(pRec.Lifecycle), string(sRec.Lifecycle)},
		{"Assigned", fmt.Sprintf("%v", pRec.Assigned), fmt.Sprintf("%v", sRec.Assigned)},
		{"Version", pRec.Version, sRec.Version},
		{"SourceHash", pRec.SourceHash, sRec.SourceHash},
		{"Path", pRec.Path, sRec.Path},
	}

	for _, check := range checks {
		if check.pval != check.sval {
			rec := DivergenceRecord{
				OrganizationID:    orgID,
				RoleID:            roleID,
				SkillID:           skillID,
				Operation:         operation,
				Field:             check.field,
				PrimaryValue:      check.pval,
				ShadowValue:       check.sval,
				PrimaryVersion:    pRec.Version,
				ShadowVersion:     sRec.Version,
				PrimarySourceHash: pRec.SourceHash,
				ShadowSourceHash:  sRec.SourceHash,
				Reason:            fmt.Sprintf("%s mismatch between primary and shadow: %s vs %s", check.field, check.pval, check.sval),
				ObservedAt:        time.Now().UTC(),
			}
			rec.DivergenceKey = ComputeDivergenceKey(rec)
			_ = p.Sink.RecordDivergence(ctx, rec)
		}
	}
}
