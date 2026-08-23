package contextengine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// P5 had no witness.
//
// Every existing test asserted a refusal or an ungrounded state: that a
// grounded request with no provider fails, that dropped excerpts fail, that a
// foreign commit fails. None asserted the thing the whole capability exists
// for -- that when it works, the excerpt reaches the model.
//
// If resolve appended the evidence and the renderer or the compiler quietly
// dropped it, every one of those tests would still pass. That is the exact
// shape of the failure this sensor was built to remove: components reporting
// success while the chain reasons about a false premise.
func TestASuccessfulGroundedBuildPutsTheCodeInFrontOfTheModel(t *testing.T) {
	fixture := newServiceFixture(t)
	const body = "func (o *Orchestrator) driveDepartments() { /* the real thing */ }"
	service := groundedService(t, fixture, stubRepositoryProvider{
		records: []SourceRecord{excerpt(t, body)},
	}, 8192)

	request := fixture.request("grounded-and-included")
	request.RepositoryBaseSHA = groundedSHA
	request.RepositoryQuery = "internal/executive"

	result, err := service.Build(context.Background(), request)
	if err != nil {
		t.Fatalf("a grounded build with usable evidence must succeed: %v", err)
	}

	// The durable snapshot must carry the excerpt as an INCLUDED segment of
	// the right kind and version. Included is the whole claim: a segment
	// recorded and then omitted is not something the model read.
	snapshot, err := service.Get(context.Background(), result.Snapshot.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	var included *Segment
	for i := range snapshot.Segments {
		if snapshot.Segments[i].SourceKind == SourceRepositoryEvidence && snapshot.Segments[i].Included {
			included = &snapshot.Segments[i]
		}
	}
	if included == nil {
		t.Fatal("the snapshot carries no included repository segment: the model saw no code")
	}
	if included.SourceVersion != groundedSHA {
		t.Fatalf("segment version=%q, want the commit the request was about", included.SourceVersion)
	}
	if !strings.Contains(string(included.Content), body) {
		t.Fatal("the segment does not contain the excerpt it claims to carry")
	}

	// And the render the model is actually handed must contain it too. A
	// segment present in the snapshot but absent from the render would be
	// evidence nobody read, recorded as evidence somebody did.
	rendered, err := service.Render(context.Background(), result.Snapshot.ID)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// The render is a JSON envelope and segment content is []byte, so it
	// arrives base64-encoded. Searching the raw bytes for the excerpt finds
	// nothing and looks exactly like the excerpt having been dropped -- the
	// same trap that made an earlier prompt audit report zero occurrences of
	// fields that were in fact present.
	var decoded struct {
		Segments []struct {
			SourceKind SourceKind `json:"source_kind"`
			Included   bool       `json:"included"`
			Content    []byte     `json:"content"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(rendered, &decoded); err != nil {
		t.Fatalf("the render must be readable as the contract it declares: %v", err)
	}
	reached := false
	for _, segment := range decoded.Segments {
		if segment.SourceKind == SourceRepositoryEvidence && segment.Included && strings.Contains(string(segment.Content), body) {
			reached = true
		}
	}
	if !reached {
		t.Fatal("the excerpt reached the snapshot and not the model input")
	}
}

// The negative half, so the positive one cannot pass vacuously: with no
// provider the same request produces no included repository segment at all.
func TestWithoutAProviderNothingReachesTheModel(t *testing.T) {
	fixture := newServiceFixture(t)
	service := groundedService(t, fixture, nil, 8192)
	request := fixture.request("ungrounded-control")
	// No RepositoryBaseSHA: an ordinary execution, which must still build.
	result, err := service.Build(context.Background(), request)
	if err != nil {
		t.Fatalf("an ordinary execution must be unaffected: %v", err)
	}
	snapshot, err := service.Get(context.Background(), result.Snapshot.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, segment := range snapshot.Segments {
		if segment.SourceKind == SourceRepositoryEvidence {
			t.Fatal("an execution that observes no code must carry no repository segment")
		}
	}
	_ = time.Now
}
