package bootstrap

import (
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/config"
)

// An Executive that only decides designs is a legitimate deployment. Staging
// being disabled must not stop it from starting, and an unconfigured
// deployment must simply carry no mission provisioning rather than failing.
func TestMissionProvisioningIsAbsentWhenUnconfigured(t *testing.T) {
	t.Setenv(missionRepositoryEnv, "")
	t.Setenv(missionTargetRefEnv, "")
	cfg := config.Config{}
	cfg.Staging.Enabled = false

	options, err := missionProvisioningOptions(cfg, nil, nil)
	if err != nil {
		t.Fatalf("an unconfigured deployment failed to start: %v", err)
	}
	if len(options) != 0 {
		t.Fatalf("options=%d, want none", len(options))
	}
}

// Configured to provision but unable to is the one combination that must fail
// loudly. Starting would produce an Executive that spends the design and
// review budget on every governed run and then blocks at the last step.
func TestConfiguredProvisioningWithStagingDisabledRefusesToStart(t *testing.T) {
	t.Setenv(missionRepositoryEnv, "explorarte-organization")
	t.Setenv(missionTargetRefEnv, "refs/heads/v2/program-context-memory-001")
	cfg := config.Config{}
	cfg.Staging.Enabled = false

	_, err := missionProvisioningOptions(cfg, nil, nil)
	if err == nil {
		t.Fatal("mission provisioning was configured against disabled staging and started anyway")
	}
	if !strings.Contains(err.Error(), "staging is disabled") {
		t.Fatalf("err=%v", err)
	}
}

// Both variables are required together: naming a repository without a target
// ref, or the reverse, is a half-configuration that must not silently enable
// or silently disable provisioning.
func TestPartialProvisioningConfigurationIsInert(t *testing.T) {
	cfg := config.Config{}
	cfg.Staging.Enabled = false

	for name, pair := range map[string][2]string{
		"repository only": {"explorarte-organization", ""},
		"target only":     {"", "refs/heads/v2/program-context-memory-001"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(missionRepositoryEnv, pair[0])
			t.Setenv(missionTargetRefEnv, pair[1])
			options, err := missionProvisioningOptions(cfg, nil, nil)
			if err != nil {
				t.Fatalf("half configuration errored: %v", err)
			}
			if len(options) != 0 {
				t.Fatal("half configuration enabled mission provisioning")
			}
		})
	}
}
