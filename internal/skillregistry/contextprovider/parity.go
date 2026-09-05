package contextprovider

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

type Divergence struct {
	RoleID       string    `json:"role_id"`
	SkillID      string    `json:"skill_id"`
	Field        string    `json:"field"`
	PrimaryValue string    `json:"primary_value"`
	ShadowValue  string    `json:"shadow_value"`
	Reason       string    `json:"reason"`
	RecordedAt   time.Time `json:"recorded_at"`
}

type DivergenceRecorder interface {
	RecordDivergence(ctx context.Context, divergence Divergence) error
	ListDivergences(ctx context.Context) ([]Divergence, error)
}

type MemoryDivergenceRecorder struct {
	mu          sync.Mutex
	divergences []Divergence
}

func NewMemoryDivergenceRecorder() *MemoryDivergenceRecorder {
	return &MemoryDivergenceRecorder{divergences: []Divergence{}}
}

func (m *MemoryDivergenceRecorder) RecordDivergence(_ context.Context, d Divergence) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d.RecordedAt.IsZero() {
		d.RecordedAt = time.Now().UTC()
	}
	m.divergences = append(m.divergences, d)
	return nil
}

func (m *MemoryDivergenceRecorder) ListDivergences(_ context.Context) ([]Divergence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Divergence, len(m.divergences))
	copy(out, m.divergences)
	return out, nil
}

// ParityProvider runs Primary and Shadow providers.
// Primary strictly governs all outputs, context, and authority.
// Shadow comparisons record divergences asynchronously/non-blocking or synchronously to Sink.
type ParityProvider struct {
	Primary contextengine.SkillProvider
	Shadow  contextengine.SkillProvider
	Sink    DivergenceRecorder
}

func NewParityProvider(primary, shadow contextengine.SkillProvider, sink DivergenceRecorder) (*ParityProvider, error) {
	if primary == nil {
		return nil, fmt.Errorf("primary skill provider is required")
	}
	return &ParityProvider{
		Primary: primary,
		Shadow:  shadow,
		Sink:    sink,
	}, nil
}

func (p *ParityProvider) ListActiveForRole(ctx context.Context, organizationID, roleID string) ([]contextengine.SkillRecord, error) {
	primaryRecords, primaryErr := p.Primary.ListActiveForRole(ctx, organizationID, roleID)

	// Shadow comparison if shadow is present
	if p.Shadow != nil && p.Sink != nil {
		shadowRecords, shadowErr := p.Shadow.ListActiveForRole(ctx, organizationID, roleID)
		p.compareLists(ctx, roleID, primaryRecords, shadowRecords, primaryErr, shadowErr)
	}

	return primaryRecords, primaryErr
}

func (p *ParityProvider) GetActiveForRole(ctx context.Context, organizationID, roleID, skillID string) (contextengine.SkillRecord, error) {
	primaryRecord, primaryErr := p.Primary.GetActiveForRole(ctx, organizationID, roleID, skillID)

	if p.Shadow != nil && p.Sink != nil {
		shadowRecord, shadowErr := p.Shadow.GetActiveForRole(ctx, organizationID, roleID, skillID)
		p.compareSingle(ctx, roleID, skillID, primaryRecord, shadowRecord, primaryErr, shadowErr)
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
			_ = p.Sink.RecordDivergence(ctx, Divergence{
				RoleID:       expected.RoleID,
				SkillID:      expected.ID,
				Field:        "ValidateVersion",
				PrimaryValue: pErrStr,
				ShadowValue:  sErrStr,
				Reason:       "error mismatch during validation",
				RecordedAt:   time.Now().UTC(),
			})
		}
	}

	return primaryErr
}

func (p *ParityProvider) compareLists(ctx context.Context, roleID string, prim, shad []contextengine.SkillRecord, primErr, shadErr error) {
	if (primErr == nil && shadErr != nil) || (primErr != nil && shadErr == nil) {
		pErrStr := "nil"
		if primErr != nil {
			pErrStr = primErr.Error()
		}
		sErrStr := "nil"
		if shadErr != nil {
			sErrStr = shadErr.Error()
		}
		_ = p.Sink.RecordDivergence(ctx, Divergence{
			RoleID:       roleID,
			Field:        "ListActiveForRole.Error",
			PrimaryValue: pErrStr,
			ShadowValue:  sErrStr,
			Reason:       "error divergence between primary and shadow",
			RecordedAt:   time.Now().UTC(),
		})
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
			_ = p.Sink.RecordDivergence(ctx, Divergence{
				RoleID:       roleID,
				SkillID:      id,
				Field:        "Presence",
				PrimaryValue: "present",
				ShadowValue:  "absent",
				Reason:       "skill present in primary but absent in shadow",
				RecordedAt:   time.Now().UTC(),
			})
			continue
		}
		if !pHas && sHas {
			_ = p.Sink.RecordDivergence(ctx, Divergence{
				RoleID:       roleID,
				SkillID:      id,
				Field:        "Presence",
				PrimaryValue: "absent",
				ShadowValue:  "present",
				Reason:       "skill absent in primary but present in shadow",
				RecordedAt:   time.Now().UTC(),
			})
			continue
		}

		// Both have it, compare fields
		p.compareFields(ctx, roleID, id, pRec, sRec)
	}
}

func (p *ParityProvider) compareSingle(ctx context.Context, roleID, skillID string, pRec, sRec contextengine.SkillRecord, primErr, shadErr error) {
	if (primErr == nil && shadErr != nil) || (primErr != nil && shadErr == nil) {
		pErrStr := "nil"
		if primErr != nil {
			pErrStr = primErr.Error()
		}
		sErrStr := "nil"
		if shadErr != nil {
			sErrStr = shadErr.Error()
		}
		_ = p.Sink.RecordDivergence(ctx, Divergence{
			RoleID:       roleID,
			SkillID:      skillID,
			Field:        "GetActiveForRole.Error",
			PrimaryValue: pErrStr,
			ShadowValue:  sErrStr,
			Reason:       "error divergence on GetActiveForRole",
			RecordedAt:   time.Now().UTC(),
		})
		return
	}
	if primErr != nil || shadErr != nil {
		return
	}
	p.compareFields(ctx, roleID, skillID, pRec, sRec)
}

func (p *ParityProvider) compareFields(ctx context.Context, roleID, skillID string, pRec, sRec contextengine.SkillRecord) {
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
			_ = p.Sink.RecordDivergence(ctx, Divergence{
				RoleID:       roleID,
				SkillID:      skillID,
				Field:        check.field,
				PrimaryValue: check.pval,
				ShadowValue:  check.sval,
				Reason:       fmt.Sprintf("%s mismatch between primary and shadow", check.field),
				RecordedAt:   time.Now().UTC(),
			})
		}
	}
}
