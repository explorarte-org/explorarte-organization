package main

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
)

type missionReviewSpy struct {
	calls                              []string
	ids                                []int64
	actor, verdict, reasonCode, reason string
	err                                error
}

func (s *missionReviewSpy) RequestPromotion(_ context.Context, task, workspace int64, actor string) (staging.Promotion, error) {
	s.calls = append(s.calls, "request")
	s.ids = []int64{task, workspace}
	s.actor = actor
	return staging.Promotion{}, s.err
}
func (s *missionReviewSpy) ReviewMission(_ context.Context, promotion, requirement int64, actor string, verdict engineeringmission.Verdict, code, reason string) (staging.Promotion, error) {
	s.calls = append(s.calls, "review")
	s.ids = []int64{promotion, requirement}
	s.actor = actor
	s.verdict = string(verdict)
	s.reasonCode = code
	s.reason = reason
	return staging.Promotion{}, s.err
}

func TestMissionRequestReviewNeverImplicitlyApproves(t *testing.T) {
	r, err := parseMissionReview([]string{"request-review", "--task", "10", "--workspace", "20", "--actor-role", "ingenieria_ia/code-runner", "--json"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	s := &missionReviewSpy{err: errors.New("required gate missing")}
	_, err = executeMissionReview(context.Background(), s, r)
	if !errors.Is(err, s.err) || !reflect.DeepEqual(s.calls, []string{"request"}) || !reflect.DeepEqual(s.ids, []int64{10, 20}) || s.actor != "ingenieria_ia/code-runner" {
		t.Fatalf("handoff lost service refusal or identity: %+v %v", s, err)
	}
}

func TestMissionReviewPreservesExplicitDecision(t *testing.T) {
	for _, verdict := range []string{"APPROVE", "REMEDIATE", "BLOCK"} {
		r, err := parseMissionReview([]string{"review", "--promotion", "21", "--requirement", "22", "--actor-role", "empresa/human", "--verdict", verdict, "--reason-code", "reviewed", "--reason", "inspected candidate and gate evidence"}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		s := &missionReviewSpy{}
		if _, err := executeMissionReview(context.Background(), s, r); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(s.calls, []string{"review"}) || !reflect.DeepEqual(s.ids, []int64{21, 22}) || s.actor != "empresa/human" || s.verdict != verdict || s.reasonCode != "reviewed" || s.reason != r.reason {
			t.Fatalf("review changed: %+v", s)
		}
	}
}

func TestMissionReviewInvalidInputsDoNotOpenRuntime(t *testing.T) {
	for _, args := range [][]string{
		nil, {"apply"}, {"request-review"}, {"request-review", "--task", "-1", "--workspace", "2", "--actor-role", "empresa/human"},
		{"request-review", "--task", "1", "--workspace", "2", "--actor-role", "empresa/human", "--verdict", "APPROVE"},
		{"review", "--promotion", "1", "--requirement", "2", "--actor-role", "empresa/human", "--reason-code", "ok", "--reason", "reviewed"},
		{"review", "--promotion", "1", "--requirement", "2", "--actor-role", "empresa/human", "--verdict", "approve", "--reason-code", "ok", "--reason", "reviewed"},
	} {
		if code := runCodeRunnerMission(args, io.Discard, io.Discard); code != exitUsage {
			t.Fatalf("%v opened runtime: %d", args, code)
		}
	}
}
