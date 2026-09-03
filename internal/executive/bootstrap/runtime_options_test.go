package bootstrap

import (
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

func TestOpenOptionsCanPinStrictExecutiveLimitsAndNoRetries(t *testing.T) {
	limits := executive.DefaultLimits()
	limits.MaxModelCalls = 1
	limits.MaxOutputTokens = 2000
	options := defaultOpenOptions()
	WithExecutiveLimits(limits)(&options)
	WithNoRetries()(&options)

	if options.limits.MaxModelCalls != 1 || options.limits.MaxOutputTokens != 2000 {
		t.Fatalf("limits=%+v, want one call and 2000 output tokens", options.limits)
	}
	if !options.noRetries {
		t.Fatal("noRetries=false, want true")
	}
}
