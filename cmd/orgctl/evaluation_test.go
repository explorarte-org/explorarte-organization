package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestEvaluationSeedListsAllR30Fixtures(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runEvaluation([]string{"seed", "--suite", "r30"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "14 fixtures") {
		t.Fatalf("stdout=%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "9 runner-ready") {
		t.Fatalf("stdout=%s", stdout.String())
	}
}

func TestEvaluationSeedRejectsUnknownSuite(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runEvaluation([]string{"seed", "--suite", "does-not-exist"}, &stdout, &stderr)
	if code != exitInvalid {
		t.Fatalf("code=%d, want %d", code, exitInvalid)
	}
}

func TestEvaluationSeedRequiresSuiteFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runEvaluation([]string{"seed"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("code=%d, want %d", code, exitUsage)
	}
}

func TestEvaluationRunRequiresModeFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runEvaluation([]string{"run", "--suite", "r30"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("code=%d, want %d", code, exitUsage)
	}
}

func TestEvaluationUnknownCommandPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runEvaluation([]string{"bogus"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("code=%d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "usage: orgctl evaluation") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func TestEvaluationReportRequiresRunID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runEvaluation([]string{"report"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("code=%d, want %d", code, exitUsage)
	}
}

func TestEvaluationCompareRequiresTwoRunIDs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runEvaluation([]string{"compare", "1"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("code=%d, want %d", code, exitUsage)
	}
}
