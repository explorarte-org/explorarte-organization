package contextengine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type stubRepositoryProvider struct {
	records []SourceRecord
	err     error
}

func (s stubRepositoryProvider) ListRepositoryEvidence(context.Context, BuildRequest) ([]SourceRecord, error) {
	return s.records, s.err
}
func (s stubRepositoryProvider) ValidateVersion(context.Context, string, SourceRecord) error {
	return nil
}

const groundedSHA = "c30328eda491241fccb81b8c83feb8a5b1e6cc35"

func excerpt(t *testing.T, body string) SourceRecord {
	t.Helper()
	priority, _ := AuthorityPriority(TierRAGEvidence)
	payload := []byte(body)
	return SourceRecord{
		Kind: SourceRepositoryEvidence, Reference: "repository://org@" + groundedSHA + "/a.go#L1-L2",
		Version: groundedSHA, AuthorityTier: TierRAGEvidence, AuthorityPriority: priority,
		InstructionClass: InstructionData, TrustClass: TrustUntrusted, DataClass: DataOrganizational,
		Content: payload, ContentHash: DigestMarkdown(payload), Included: true,
	}
}

func groundedService(t *testing.T, fixture *serviceFixture, provider RepositoryEvidenceProvider, maxSegmentBytes int) Service {
	t.Helper()
	options := []ServiceOption{}
	if provider != nil {
		options = append(options, WithRepositoryEvidence(provider))
	}
	service, err := NewService(
		ServiceConfig{OrganizationAgentPath: "AGENT.md", MaxTotalBytes: 65536, MaxSegmentBytes: maxSegmentBytes,
			MaxSegments: 64, MaxSkills: 16, MaxMemorySegments: 32, MaxRAGSegments: 20},
		fixture.registry, fixture.docs, fixture.canonical,
		NoopOwnerConstraintProvider{}, UnavailableMemoryProvider{}, emptySkillProvider{},
		UnavailableProjectProvider{}, UnavailableTaskProvider{}, UnavailableRAGProvider{},
		NewAssembler(), NewRenderer(), fixture.store,
		fixedClock{now: time.Date(2026, 8, 22, 6, 0, 0, 0, time.UTC)},
		options...,
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// The scenario that makes a healthy sensor produce blind designs.
//
// Repository evidence is untrusted data, and untrusted data is droppable under
// context pressure -- correctly, in general. But an execution given a commit
// precisely so it could look at code must not proceed having looked at none.
// Losing excerpts nine through sixteen to the budget is fine: it saw eight.
// Losing all of them and calling the model anyway is the original blindness
// with every component reporting success.
func TestAnExecutionThatObservedNoCodeIsRefused(t *testing.T) {
	fixture := newServiceFixture(t)
	// An excerpt larger than one segment may hold. Being untrusted data it
	// is OMITTED rather than rejecting the assembly -- and an omitted segment
	// stays in the assembly with Included=false, which is what made this
	// case pass silently until the count started looking at that field.
	service := groundedService(t, fixture, stubRepositoryProvider{
		records: []SourceRecord{excerpt(t, strings.Repeat("x", 9000))},
	}, 4096)

	request := fixture.request("dropped-evidence")
	request.RepositoryBaseSHA = groundedSHA
	request.RepositoryQuery = "internal/executive"

	if _, err := service.Build(context.Background(), request); !errors.Is(err, ErrRepositoryEvidenceUnavailable) {
		t.Fatalf("an execution whose every excerpt was dropped must be refused, got %v", err)
	}
}

// A deployment with no repository configured must not be able to look like one
// whose repository happened to be empty.
func TestAGroundedRequestWithNoProviderIsRefused(t *testing.T) {
	fixture := newServiceFixture(t)
	service := groundedService(t, fixture, nil, 8192)

	request := fixture.request("no-provider")
	request.RepositoryBaseSHA = groundedSHA
	request.RepositoryQuery = "internal/executive"

	if _, err := service.Build(context.Background(), request); !errors.Is(err, ErrRepositoryEvidenceUnavailable) {
		t.Fatalf("a grounded request with no repository must be refused, got %v", err)
	}
}

// A provider answering about a different commit is answering a different
// question than the one the design is about.
func TestEvidenceAboutAnotherCommitIsRefused(t *testing.T) {
	fixture := newServiceFixture(t)
	foreign := excerpt(t, "package a")
	foreign.Version = "eedc79f4560701d59c80375bf7f5e19b2a6a8438"
	service := groundedService(t, fixture, stubRepositoryProvider{records: []SourceRecord{foreign}}, 64)

	request := fixture.request("foreign-commit")
	request.RepositoryBaseSHA = groundedSHA
	request.RepositoryQuery = "internal/executive"

	if _, err := service.Build(context.Background(), request); !errors.Is(err, ErrRepositoryEvidenceUnavailable) {
		t.Fatalf("evidence about another commit must be refused, got %v", err)
	}
}

// And the ordinary case still works: an execution with no commit is simply an
// execution that does not observe code, and nothing about it changes.
func TestAnUngroundedRequestIsUnaffected(t *testing.T) {
	fixture := newServiceFixture(t)
	service := groundedService(t, fixture, stubRepositoryProvider{err: errors.New("would fail if consulted")}, 64)

	if _, err := service.Build(context.Background(), fixture.request("ungrounded")); err != nil {
		t.Fatalf("an execution that observes no code must not consult the repository at all: %v", err)
	}
}
