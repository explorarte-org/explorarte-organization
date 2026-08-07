package modelegress

import (
	"fmt"
	"strings"
)

type Evaluator struct{}

func NewEvaluator() *Evaluator { return &Evaluator{} }

func (e *Evaluator) Evaluate(request EvaluationRequest) (Decision, error) {
	classes, classHash := NormalizeClassifications(request.ContextClassifications)
	decision := Decision{Effect: EffectDeny, Classifications: classes, ClassificationsHash: classHash}
	if request.Policy.Version.ID <= 0 || request.Policy.Version.PolicyVersion <= 0 ||
		request.Policy.DefaultAction != EffectDeny || !lowerHex64Pattern.MatchString(request.Policy.CanonicalHash) ||
		request.Policy.Version.CanonicalHash != request.Policy.CanonicalHash ||
		request.Policy.OrganizationRevisionID != request.OrganizationRevisionID || request.Policy.Version.Status != "materialized" {
		decision.ReasonCodes = []string{"egress_policy_unresolved"}
		return decision, nil
	}
	if !validProviderTransport(request.ProviderTransport) {
		decision.Effect = EffectError
		decision.ReasonCodes = []string{"provider_transport_invalid"}
		return decision, fmt.Errorf("%w: invalid provider transport", ErrEvaluationConflict)
	}
	provider := strings.TrimSpace(request.ProviderID)
	if provider == "" {
		decision.ReasonCodes = []string{"provider_undeclared"}
		return decision, nil
	}
	if len(classes) == 0 {
		decision.ReasonCodes = []string{"empty_classification_set"}
		return decision, nil
	}

	rules := make(map[string]Rule, len(request.Policy.Rules))
	hard := make(map[string]Rule)
	knownProvider := false
	for _, rule := range request.Policy.Rules {
		if rule.ProviderID == "*" && rule.HardDeny {
			hard[string(rule.DataClassification)] = rule
			continue
		}
		if rule.ProviderID == provider {
			knownProvider = true
			rules[string(rule.DataClassification)] = rule
		}
	}

	hardReasons := make([]string, 0, len(classes))
	for _, classification := range classes {
		if rule, exists := hard[classification]; exists {
			hardReasons = append(hardReasons, rule.ReasonCode)
		}
	}
	if len(hardReasons) > 0 {
		decision.ReasonCodes = NormalizeReasonCodes(hardReasons)
		return decision, nil
	}
	if !knownProvider {
		decision.ReasonCodes = []string{"provider_undeclared"}
		return decision, nil
	}

	allowed := true
	reasons := make([]string, 0, len(classes))
	for _, classification := range classes {
		if !validRuntimeClassification(classification) {
			allowed = false
			reasons = append(reasons, "unknown_classification")
			continue
		}
		rule, exists := rules[classification]
		if !exists {
			allowed = false
			reasons = append(reasons, "egress_combination_undeclared")
			continue
		}
		reasons = append(reasons, rule.ReasonCode)
		if rule.Effect != EffectAllow {
			allowed = false
		}
	}
	decision.ReasonCodes = NormalizeReasonCodes(reasons)
	if allowed {
		decision.Effect = EffectAllow
	}
	return decision, nil
}

func validRuntimeClassification(value string) bool {
	switch DataClassification(value) {
	case ClassificationPublic, ClassificationSanitized, ClassificationOrganizational, ClassificationSecret, ClassificationClinical:
		return true
	default:
		return false
	}
}
