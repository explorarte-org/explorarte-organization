package modelegress

import (
	"fmt"
	"regexp"
	"strings"
)

var lowerHex64Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func ValidateRegistryPlan(plan RegistryPlan) error {
	if strings.TrimSpace(plan.OrganizationID) == "" || plan.OrganizationRevisionID <= 0 {
		return fmt.Errorf("%w: registry plan organization and revision are required", ErrInvalidPolicy)
	}
	if !lowerHex64Pattern.MatchString(plan.CanonicalHash) || plan.Policy.CanonicalHash != plan.CanonicalHash {
		return fmt.Errorf("%w: registry plan canonical hash mismatch", ErrInvalidPolicy)
	}
	if strings.TrimSpace(plan.Policy.PolicyID) == "" || len(plan.Policy.PolicyID) > 160 || !canonicalIdentifierPattern.MatchString(plan.Policy.PolicyID) {
		return fmt.Errorf("%w: invalid registry policy ID", ErrInvalidPolicy)
	}
	if plan.Policy.PolicyVersion <= 0 || plan.Policy.DefaultAction != EffectDeny {
		return fmt.Errorf("%w: invalid registry policy version or default action", ErrInvalidPolicy)
	}

	hardSeen := make(map[DataClassification]struct{}, len(plan.Policy.HardDenies))
	for _, hardDeny := range plan.Policy.HardDenies {
		if hardDeny.DataClassification != ClassificationSecret && hardDeny.DataClassification != ClassificationClinical {
			return fmt.Errorf("%w: invalid hard deny classification %q", ErrInvalidPolicy, hardDeny.DataClassification)
		}
		if !validReasonCode(hardDeny.ReasonCode) {
			return fmt.Errorf("%w: invalid hard deny reason code", ErrInvalidPolicy)
		}
		if _, duplicate := hardSeen[hardDeny.DataClassification]; duplicate {
			return fmt.Errorf("%w: duplicate hard deny", ErrInvalidPolicy)
		}
		hardSeen[hardDeny.DataClassification] = struct{}{}
	}
	for _, required := range []DataClassification{ClassificationSecret, ClassificationClinical} {
		if _, exists := hardSeen[required]; !exists {
			return fmt.Errorf("%w: %s must be a hard deny", ErrInvalidPolicy, required)
		}
	}

	ruleSeen := make(map[string]struct{}, len(plan.Policy.Rules))
	for _, rule := range plan.Policy.Rules {
		if strings.TrimSpace(rule.ProviderID) == "" || len(rule.ProviderID) > 160 || !canonicalIdentifierPattern.MatchString(rule.ProviderID) {
			return fmt.Errorf("%w: invalid rule provider", ErrInvalidPolicy)
		}
		switch rule.DataClassification {
		case ClassificationPublic, ClassificationSanitized, ClassificationOrganizational:
		default:
			return fmt.Errorf("%w: invalid rule classification %q", ErrInvalidPolicy, rule.DataClassification)
		}
		if rule.Effect != EffectAllow && rule.Effect != EffectDeny {
			return fmt.Errorf("%w: invalid rule effect", ErrInvalidPolicy)
		}
		if rule.HardDeny {
			return fmt.Errorf("%w: explicit provider rules cannot be hard denies", ErrInvalidPolicy)
		}
		if !validReasonCode(rule.ReasonCode) {
			return fmt.Errorf("%w: invalid rule reason code", ErrInvalidPolicy)
		}
		key := rule.ProviderID + "\x00" + string(rule.DataClassification)
		if _, duplicate := ruleSeen[key]; duplicate {
			return fmt.Errorf("%w: duplicate provider/classification rule", ErrInvalidPolicy)
		}
		ruleSeen[key] = struct{}{}
	}
	return nil
}

func ValidatePreSendEvaluation(evaluation PreSendEvaluation) error {
	if evaluation.InvocationID <= 0 || evaluation.DispatchAttemptID <= 0 || evaluation.PolicyVersionID <= 0 || evaluation.OrganizationRevisionID <= 0 || evaluation.ModelProfileVersionID <= 0 {
		return fmt.Errorf("%w: pre-send identifiers must be positive", ErrEvaluationConflict)
	}
	if strings.TrimSpace(evaluation.OrganizationID) == "" || strings.TrimSpace(evaluation.DispatchActorRoleID) == "" || strings.TrimSpace(evaluation.SubjectRoleID) == "" || strings.TrimSpace(evaluation.ProviderID) == "" {
		return fmt.Errorf("%w: pre-send identity fields are required", ErrEvaluationConflict)
	}
	if !validProviderTransport(evaluation.ProviderTransport) {
		return fmt.Errorf("%w: invalid provider transport", ErrEvaluationConflict)
	}
	for name, value := range map[string]string{
		"policy hash":            evaluation.PolicyHash,
		"action digest":          evaluation.ActionDigest,
		"capability matrix hash": evaluation.CapabilityMatrixHash,
		"classification hash":    evaluation.ContextClassificationsHash,
		"decision hash":          evaluation.DecisionHash,
	} {
		if !lowerHex64Pattern.MatchString(value) {
			return fmt.Errorf("%w: invalid %s", ErrEvaluationConflict, name)
		}
	}
	classes, classHash := NormalizeClassifications(evaluation.ContextClassifications)
	if classHash != evaluation.ContextClassificationsHash || !equalStrings(classes, evaluation.ContextClassifications) {
		return fmt.Errorf("%w: classifications are not normalized", ErrEvaluationConflict)
	}
	if evaluation.EgressEffect == EffectAllow {
		if len(classes) == 0 {
			return fmt.Errorf("%w: egress allow requires at least one classification", ErrEvaluationConflict)
		}
		for _, classification := range classes {
			switch DataClassification(classification) {
			case ClassificationPublic, ClassificationSanitized, ClassificationOrganizational:
			default:
				return fmt.Errorf("%w: egress allow contains a forbidden or unknown classification", ErrEvaluationConflict)
			}
		}
	}
	reasons := NormalizeReasonCodes(evaluation.EgressReasonCodes)
	if !equalStrings(reasons, evaluation.EgressReasonCodes) {
		return fmt.Errorf("%w: egress reason codes are not normalized", ErrEvaluationConflict)
	}
	if !validReasonCode(evaluation.AuthorizationReasonCode) {
		return fmt.Errorf("%w: invalid authorization reason code", ErrEvaluationConflict)
	}
	for _, reason := range reasons {
		if !validReasonCode(reason) {
			return fmt.Errorf("%w: invalid egress reason code", ErrEvaluationConflict)
		}
	}
	switch evaluation.AuthorizationEffect {
	case AuthorizationAllow:
		if evaluation.EgressEffect != EffectAllow && evaluation.EgressEffect != EffectDeny && evaluation.EgressEffect != EffectError {
			return fmt.Errorf("%w: authorized evaluation must contain an egress decision", ErrEvaluationConflict)
		}
		if len(reasons) == 0 {
			return fmt.Errorf("%w: egress reason code is required", ErrEvaluationConflict)
		}
	case AuthorizationDeny, AuthorizationError:
		if evaluation.EgressEffect != EffectNotEvaluated || len(reasons) != 0 {
			return fmt.Errorf("%w: authorization deny or error must short-circuit egress", ErrEvaluationConflict)
		}
	default:
		return fmt.Errorf("%w: invalid authorization effect", ErrEvaluationConflict)
	}
	if DecisionHash(evaluation) != evaluation.DecisionHash {
		return fmt.Errorf("%w: decision hash mismatch", ErrEvaluationConflict)
	}
	return nil
}

func ValidatePersistAllowCommand(command PersistAllowCommand) error {
	if err := ValidatePreSendEvaluation(command.Evaluation); err != nil {
		return err
	}
	if command.Evaluation.AuthorizationEffect != AuthorizationAllow || command.Evaluation.EgressEffect != EffectAllow {
		return fmt.Errorf("%w: allow transition requires authorization and egress allow", ErrEvaluationConflict)
	}
	if strings.TrimSpace(command.ClaimToken) == "" || !lowerHex64Pattern.MatchString(command.ProviderIdempotencyKeyHash) || command.Deadline.IsZero() {
		return fmt.Errorf("%w: invalid allow transition metadata", ErrEvaluationConflict)
	}
	return nil
}

func ValidatePersistFailureCommand(command PersistDenyCommand) error {
	if err := ValidatePreSendEvaluation(command.Evaluation); err != nil {
		return err
	}
	// A successful authorization+egress decision may only be persisted by the
	// allow transaction that also marks send_started. Persisting it through the
	// failure path would violate the pre-send atomicity invariant.
	if command.Evaluation.AuthorizationEffect == AuthorizationAllow && command.Evaluation.EgressEffect == EffectAllow {
		return fmt.Errorf("%w: allow decision cannot be persisted by failure transition", ErrEvaluationConflict)
	}
	if strings.TrimSpace(command.ClaimToken) == "" || !validReasonCode(command.ErrorCode) || command.OutboxMaxAttempts < 1 || command.OutboxMaxAttempts > 100 {
		return fmt.Errorf("%w: invalid pre-send failure metadata", ErrEvaluationConflict)
	}
	return nil
}

func validProviderTransport(value string) bool {
	switch strings.TrimSpace(value) {
	case "cli_adapter", "http_adapter", "fake_adapter":
		return true
	default:
		return false
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
