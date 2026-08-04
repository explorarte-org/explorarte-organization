package main

import (
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
)

func TestStagingFlagsMayFollowPositionalID(t *testing.T) {
	set := flag.NewFlagSet("staging workspace seal", flag.ContinueOnError)
	holder := set.String("holder", "", "")
	actor := set.String("actor-role", "", "")
	stdin := set.Bool("lease-token-stdin", false, "")
	jsonOutput := set.Bool("json", false, "")
	if err := parseInterspersed(set, []string{"17", "--holder", "runner-1", "--actor-role=ingenieria_ia/code-runner", "--lease-token-stdin", "--json"}); err != nil {
		t.Fatal(err)
	}
	if set.NArg() != 1 || set.Arg(0) != "17" || *holder != "runner-1" || *actor != "ingenieria_ia/code-runner" || !*stdin || !*jsonOutput {
		t.Fatalf("unexpected parse result args=%v holder=%q actor=%q stdin=%v json=%v", set.Args(), *holder, *actor, *stdin, *jsonOutput)
	}
}

func TestStagingErrorDoesNotEchoTokenMarkers(t *testing.T) {
	err := errors.New("lease_token=top-secret token_hash=also-secret")
	got := redactStagingError(err)
	if strings.Contains(got, "lease_token") || strings.Contains(got, "token_hash") {
		t.Fatalf("sensitive marker leaked: %q", got)
	}
}

func TestStagingExitCodesAreStable(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{staging.ErrInvalidInput, exitUsage},
		{staging.ErrDatabaseUnavailable, exitDatabase},
		{staging.ErrWorkspaceNotFound, exitInvalid},
		{staging.ErrCapabilityDenied, exitDrift},
		{errors.New("unexpected"), exitInternal},
	}
	for _, test := range cases {
		var output strings.Builder
		if got := stagingError(&output, test.err); got != test.want {
			t.Fatalf("error=%v exit=%d want=%d", test.err, got, test.want)
		}
	}
}
