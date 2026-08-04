package main

import (
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

func TestNormalizeTaskSubcommand(t *testing.T) {
	tests := map[string]struct {
		input []string
		want  string
	}{
		"dependency":  {[]string{"dependency", "add", "1", "2"}, "dependency-add"},
		"requirement": {[]string{"requirement", "add", "1"}, "requirement-add"},
		"evidence":    {[]string{"evidence", "add", "1"}, "evidence-add"},
		"dead list":   {[]string{"dead-letter", "list"}, "dead-list"},
		"dead show":   {[]string{"dead-letter", "show", "1"}, "dead-show"},
		"plain":       {[]string{"get", "1"}, "get"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got := normalizeTaskSubcommand(test.input)
			if len(got) == 0 || got[0] != test.want {
				t.Fatalf("normalize=%v want first=%q", got, test.want)
			}
		})
	}
}

func TestParseInterspersed(t *testing.T) {
	flags := flag.NewFlagSet("task start", flag.ContinueOnError)
	attempt := flags.Int64("attempt", 0, "attempt id")
	worker := flags.String("worker", "", "worker id")
	jsonOutput := flags.Bool("json", false, "emit JSON")

	err := parseInterspersed(flags, []string{"17", "--attempt", "9", "--worker=worker-1", "--json"})
	if err != nil {
		t.Fatalf("parse interspersed: %v", err)
	}
	if flags.NArg() != 1 || flags.Arg(0) != "17" {
		t.Fatalf("positionals=%v", flags.Args())
	}
	if *attempt != 9 || *worker != "worker-1" || !*jsonOutput {
		t.Fatalf("attempt=%d worker=%q json=%t", *attempt, *worker, *jsonOutput)
	}
}

func TestParseInterspersedHonorsDoubleDash(t *testing.T) {
	flags := flag.NewFlagSet("task get", flag.ContinueOnError)
	jsonOutput := flags.Bool("json", false, "emit JSON")

	err := parseInterspersed(flags, []string{"--json", "--", "-17"})
	if err != nil {
		t.Fatalf("parse interspersed: %v", err)
	}
	if !*jsonOutput || flags.NArg() != 1 || flags.Arg(0) != "-17" {
		t.Fatalf("json=%t positionals=%v", *jsonOutput, flags.Args())
	}
}

func TestReadSecretToken(t *testing.T) {
	got, err := readSecretToken(strings.NewReader("opaque-token\n"))
	if err != nil || got != "opaque-token" {
		t.Fatalf("token=%q err=%v", got, err)
	}
	for _, input := range []string{"", "two tokens", "line1\nline2", strings.Repeat("x", 4097)} {
		if _, err := readSecretToken(strings.NewReader(input)); err == nil {
			t.Fatalf("input %q was accepted", input)
		}
	}
}

func TestParseStatuses(t *testing.T) {
	values, err := parseStatuses("ready,running")
	if err != nil || len(values) != 2 || values[0] != tasks.StatusReady || values[1] != tasks.StatusRunning {
		t.Fatalf("statuses=%v err=%v", values, err)
	}
	if _, err := parseStatuses("ready,unknown"); err == nil {
		t.Fatal("invalid status accepted")
	}
}

func TestTaskErrorCodesAreStable(t *testing.T) {
	var stderr strings.Builder
	if got := taskError(&stderr, tasks.ErrDatabaseUnavailable); got != exitDatabase {
		t.Fatalf("database code=%d", got)
	}
	if got := taskError(&stderr, tasks.ErrInvalidInput); got != exitUsage {
		t.Fatalf("usage code=%d", got)
	}
	if got := taskError(&stderr, tasks.ErrLeaseMismatch); got != exitDrift {
		t.Fatalf("conflict code=%d", got)
	}
	if got := taskError(&stderr, tasks.ErrNotFound); got != exitInvalid {
		t.Fatalf("not found code=%d", got)
	}
	if got := taskError(&stderr, errors.New("unexpected")); got != exitInternal {
		t.Fatalf("internal code=%d", got)
	}
}
