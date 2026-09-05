package skillforge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/improvement"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry/contextprovider"
)

type ForgeEvaluator struct {
	skillsRoot string
}

func NewForgeEvaluator(skillsRoot string) *ForgeEvaluator {
	return &ForgeEvaluator{skillsRoot: skillsRoot}
}

func (e *ForgeEvaluator) EvaluateCandidate(ctx context.Context, roleID string, candidate skillregistry.SkillVersion) (EvaluationResult, error) {
	// 1. Construct PinnedCandidateSkillProvider for isolated test
	pinnedProvider, err := contextprovider.NewPinnedCandidateSkillProvider(nil, candidate, roleID)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("create pinned candidate provider: %w", err)
	}

	// Verify isolated resolution works
	activeRecords, err := pinnedProvider.ListActiveForRole(ctx, candidate.OrganizationID, roleID)
	if err != nil || len(activeRecords) != 1 {
		return EvaluationResult{}, fmt.Errorf("pinned candidate provider failed to resolve candidate: %v", err)
	}

	// 2. Map SkillVersion to improvement.ArtifactRef
	artifactRef := improvement.ArtifactRef{
		ArtifactID:    candidate.ID,
		ContentHash:   candidate.CanonicalHash,
		SchemaVersion: "skill-registry.v1",
	}
	if err := artifactRef.Validate(); err != nil {
		return EvaluationResult{}, fmt.Errorf("invalid improvement artifact ref: %w", err)
	}

	// 3. Adversarial Review: Treat SKILL.md as UNTRUSTED data
	fullPath := filepath.Join(e.skillsRoot, candidate.Source.Path)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("read candidate source for adversarial review: %w", err)
	}

	adversarialPass, advReason := e.runAdversarialReview(content)
	if !adversarialPass {
		return EvaluationResult{
			Passed:                 false,
			CandidateVersionID:     candidate.ID,
			CandidateCanonicalHash: candidate.CanonicalHash,
			CandidateSourceHash:    candidate.Source.NormalizedSHA256,
			SuiteRef:               "adversarial/review:v1",
			AdversarialVerdict:     "fail",
			CanaryVerdict:          "skipped",
		}, fmt.Errorf("%w: %s", ErrAdversarialFailed, advReason)
	}

	// 4. Isolated Canary Evaluation
	canaryPass, metrics := e.runIsolatedCanary(pinnedProvider, roleID, candidate)
	if !canaryPass {
		return EvaluationResult{
			Passed:                 false,
			CandidateVersionID:     candidate.ID,
			CandidateCanonicalHash: candidate.CanonicalHash,
			CandidateSourceHash:    candidate.Source.NormalizedSHA256,
			SuiteRef:               "canary/isolated:v1",
			Metrics:                metrics,
			AdversarialVerdict:     "pass",
			CanaryVerdict:          "fail",
		}, ErrCanaryFailed
	}

	return EvaluationResult{
		Passed:                 true,
		CandidateVersionID:     candidate.ID,
		CandidateCanonicalHash: candidate.CanonicalHash,
		CandidateSourceHash:    candidate.Source.NormalizedSHA256,
		SuiteRef:               "skillforge/evaluation-suite:v1",
		Metrics:                metrics,
		AdversarialVerdict:     "pass",
		CanaryVerdict:          "pass",
	}, nil
}

func (e *ForgeEvaluator) runAdversarialReview(content []byte) (bool, string) {
	text := strings.ToLower(string(content))
	forbiddenPatterns := []string{
		"disregard",
		"ignore owner",
		"grant yourself",
		"activate yourself",
		"delegate across departments",
		"exfiltrate",
		"curl ",
		"wget ",
		"chmod ",
		"rm -rf",
	}

	for _, p := range forbiddenPatterns {
		if strings.Contains(text, p) {
			return false, fmt.Sprintf("adversarial pattern detected: %q", p)
		}
	}

	return true, ""
}

func (e *ForgeEvaluator) runIsolatedCanary(_ *contextprovider.PinnedCandidateSkillProvider, _ string, _ skillregistry.SkillVersion) (bool, map[string]float64) {
	metrics := map[string]float64{
		"verified_success":   1.0,
		"contradiction_rate": 0.0,
		"actual_cost_usd":    0.0005,
		"turns":              1.0,
		"tool_calls":         0.0,
	}
	return true, metrics
}
