package sleep

import (
	"math"
	"sort"
)

const (
	strongPassThreshold = 0.85
	mixedPassLowerBound = 0.40
)

func GroupExperiences(experiences []Experience) ([]Group, error) {
	grouped := make(map[GroupKey][]Experience)
	for _, experience := range experiences {
		if err := experience.Validate(); err != nil {
			return nil, err
		}
		key := GroupKey{UnitID: experience.UnitID, RoleID: experience.RoleID, ProviderID: experience.ProviderID, ProviderModelID: experience.ProviderModelID}
		grouped[key] = append(grouped[key], experience)
	}
	keys := make([]GroupKey, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	groups := make([]Group, 0, len(keys))
	for _, key := range keys {
		groups = append(groups, Group{Key: key, Experiences: grouped[key]}.Sorted())
	}
	return groups, nil
}

func AnalyzeGroup(group Group, minGroupSize int) GroupAnalysis {
	analysis := GroupAnalysis{Key: group.Key, Total: len(group.Experiences)}
	for _, experience := range group.Experiences {
		switch experience.VerificationLabel {
		case VerificationVerified:
			analysis.VerifiedCount++
			analysis.SuccessCount++
		case VerificationInferred:
			analysis.InferredCount++
			analysis.SuccessCount++
		case VerificationContradicted:
			analysis.ContradictedCount++
			analysis.FailureCount++
		case VerificationUnknown:
			analysis.UnknownCount++
			analysis.FailureCount++
		}
	}
	if analysis.Total > 0 {
		analysis.PassRate = round6(float64(analysis.SuccessCount) / float64(analysis.Total))
	}
	analysis.Contradiction = analysis.PassRate > mixedPassLowerBound && analysis.PassRate < strongPassThreshold
	analysis.Eligible = analysis.Total >= minGroupSize && analysis.PassRate > mixedPassLowerBound
	return analysis
}

func RecurringGroups(groups []Group, minGroupSize int) []Group {
	out := make([]Group, 0, len(groups))
	for _, group := range groups {
		if len(group.Experiences) >= minGroupSize {
			out = append(out, group)
		}
	}
	return out
}

func PortabilityFor(primary Group, recurring []Group, minGroupSize int) (Portability, []Experience) {
	// Grouping keys now include ProviderModelID, so a single provider can
	// contribute more than one group here (one per distinct model). The
	// portability signal is about the provider boundary, not the
	// provider+model combination, so ProvidersSeen must count distinct
	// ProviderID values rather than the number of groups collected.
	providerGroups := make([]Group, 0)
	for _, group := range recurring {
		if len(group.Experiences) < minGroupSize {
			continue
		}
		if group.Key.UnitID == primary.Key.UnitID && group.Key.RoleID == primary.Key.RoleID {
			providerGroups = append(providerGroups, group.Sorted())
		}
	}
	sort.Slice(providerGroups, func(i, j int) bool {
		if providerGroups[i].Key.ProviderID != providerGroups[j].Key.ProviderID {
			return providerGroups[i].Key.ProviderID < providerGroups[j].Key.ProviderID
		}
		return providerGroups[i].Key.ProviderModelID < providerGroups[j].Key.ProviderModelID
	})

	distinctProviders := make(map[string]struct{}, len(providerGroups))
	for _, group := range providerGroups {
		distinctProviders[group.Key.ProviderID] = struct{}{}
	}

	portability := Portability{ProvidersSeen: len(distinctProviders)}
	allEvidence := make([]Experience, 0)
	bands := map[string]struct{}{}
	for _, group := range providerGroups {
		analysis := AnalyzeGroup(group, minGroupSize)
		band := passBand(analysis.PassRate)
		bands[band] = struct{}{}
		portability.ProviderRates = append(portability.ProviderRates, ProviderRate{
			ProviderID: group.Key.ProviderID, ProviderModelID: group.Key.ProviderModelID, PassRate: analysis.PassRate, Count: analysis.Total, Band: band,
		})
		allEvidence = append(allEvidence, group.Experiences...)
	}
	if portability.ProvidersSeen <= 1 {
		portability.Classification = "single_provider_observation"
	} else if len(bands) == 1 {
		portability.Classification = "consistent_eligibility_band_across_providers"
	} else {
		portability.Classification = "provider_dependent"
	}
	sort.Slice(allEvidence, func(i, j int) bool {
		if allEvidence[i].ObservedAt.Equal(allEvidence[j].ObservedAt) {
			return allEvidence[i].RunID < allEvidence[j].RunID
		}
		return allEvidence[i].ObservedAt.Before(allEvidence[j].ObservedAt)
	})
	return portability, dedupeExperiences(allEvidence)
}

func passBand(passRate float64) string {
	switch {
	case passRate >= strongPassThreshold:
		return "strong"
	case passRate > mixedPassLowerBound:
		return "mixed"
	default:
		return "weak"
	}
}

func Confidence(recurrenceCount, recurrenceTarget int, passRate float64, providersSeen int, contradiction bool) float64 {
	if recurrenceCount <= 0 || recurrenceTarget <= 0 || passRate <= 0 {
		return 0
	}
	recurrence := math.Min(1, float64(recurrenceCount)/float64(recurrenceTarget))
	providerBonus := 1.0 + 0.1*float64(minInt(maxInt(providersSeen-1, 0), 3))
	value := recurrence * passRate * providerBonus
	if contradiction {
		value -= 0.15
	}
	if value < 0 {
		value = 0
	}
	if value > 1 {
		value = 1
	}
	return round6(value)
}

func dedupeExperiences(values []Experience) []Experience {
	seen := make(map[int64]struct{}, len(values))
	out := make([]Experience, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value.RunID]; exists {
			continue
		}
		seen[value.RunID] = struct{}{}
		out = append(out, value)
	}
	return out
}

func round6(value float64) float64 { return math.Round(value*1_000_000) / 1_000_000 }
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
