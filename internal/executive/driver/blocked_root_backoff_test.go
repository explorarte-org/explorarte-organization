package driver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// The collaborators below are stubs over the driver's existing seams -- the
// observer option, an executive.RootSource and a RootCoordinator -- and the
// test drives them through RunOnce. No new production collaborator is added.
//
// RootSource exposes no probe for the per-root backoff map, so both halves of
// the required behaviour are asserted through RunOnce itself: a root classified
// as blocked must not be re-driven while its backoff window is open, and must
// become eligible again once Config.ErrorBackoff has elapsed instead of being
// parked for a fixed hour.

const (
	blockedBackoffRootID int64 = 4242

	// blockedBackoffWindow is the configured Config.ErrorBackoff: comfortably
	// above the constructor's 100ms floor and above any realistic gap between
	// two consecutive RunOnce calls in this test.
	blockedBackoffWindow = time.Second

	// blockedBackoffSlack absorbs the time RunOnce itself takes between writing
	// the deadline and reading it back.
	blockedBackoffSlack = 400 * time.Millisecond

	// blockedBackoffRecheckWait exceeds blockedBackoffWindow so that the third
	// sweep runs after the window has elapsed.
	blockedBackoffRecheckWait = blockedBackoffWindow + 300*time.Millisecond
)

// blockedBackoffRootSource is a stub executive.RootSource reporting the same
// single root as executable on every sweep. The embedded interface keeps the
// stub valid if the port grows further methods; RunOnce only calls
// ListExecutableRoots.
type blockedBackoffRootSource struct {
	executive.RootSource
	rootID int64
}

func (s blockedBackoffRootSource) ListExecutableRoots(ctx context.Context, limit int) ([]int64, error) {
	return []int64{s.rootID}, nil
}

// blockedBackoffResumer is a stub ExecutiveResumer: it counts every resume
// attempt and reports each one as the configured blocked outcome.
// ClassifyResult maps that typed error to a blocked classification, so no typed
// Run state is needed to reach the blocked branch of RunOnce.
type blockedBackoffResumer struct {
	// err is the typed error every resume reports.
	err error

	mu      sync.Mutex
	resumes int
}

func (r *blockedBackoffResumer) ResumeDurable(ctx context.Context, rootTaskID int64) (executive.Run, error) {
	r.mu.Lock()
	r.resumes++
	r.mu.Unlock()
	var run executive.Run
	return run, r.err
}

func (r *blockedBackoffResumer) resumeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resumes
}

// blockedBackoffClaim is the RootClaim handed to the driver. It promotes the
// remaining RootClaim methods from the embedded interface and overrides Release,
// the only method RunOnce calls on a claim. Releasing is a no-op: the single
// stubbed root is never contended for.
type blockedBackoffClaim struct {
	RootClaim
}

func (blockedBackoffClaim) Release(context.Context) error { return nil }

var _ RootClaim = blockedBackoffClaim{}

// blockedBackoffCoordinator is a stub RootCoordinator granting the claim for
// every requested root. As with the root source, the embedded interface keeps
// the stub valid if the port grows further methods; RunOnce only calls
// TryClaimRoot.
type blockedBackoffCoordinator struct {
	RootCoordinator
}

func (blockedBackoffCoordinator) TryClaimRoot(ctx context.Context, organizationID string, rootID int64) (RootClaim, bool, error) {
	return blockedBackoffClaim{}, true, nil
}

// blockedBackoffObserver records the classification the driver reports for each
// root.
type blockedBackoffObserver struct {
	mu              sync.Mutex
	classifications []ResultClassification
}

func (o *blockedBackoffObserver) observe(rootID int64, classification ResultClassification, err error) {
	o.mu.Lock()
	o.classifications = append(o.classifications, classification)
	o.mu.Unlock()
}

func (o *blockedBackoffObserver) last() ResultClassification {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.classifications) == 0 {
		return ""
	}
	return o.classifications[len(o.classifications)-1]
}

// blockedBackoffRemaining reports how long the driver currently suppresses the
// stubbed root, read under the driver mutex.
func blockedBackoffRemaining(d *CampaignDriver, rootID int64) (time.Duration, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	deadline, ok := d.backoffs[rootID]
	if !ok {
		return 0, false
	}
	return time.Until(deadline), true
}

// newBlockedBackoffDriver wires the stubs into a driver whose only root is the
// stubbed one.
func newBlockedBackoffDriver(t *testing.T, resumer *blockedBackoffResumer, observer *blockedBackoffObserver) *CampaignDriver {
	t.Helper()
	d, err := NewCampaignDriver(
		resumer,
		blockedBackoffRootSource{rootID: blockedBackoffRootID},
		blockedBackoffCoordinator{},
		Config{
			OrganizationID: "explorarte",
			PollInterval:   time.Second,
			ErrorBackoff:   blockedBackoffWindow,
			BatchSize:      4,
			MaxConcurrency: 1,
		},
		WithObserver(observer.observe),
	)
	if err != nil {
		t.Fatalf("NewCampaignDriver: %v", err)
	}
	return d
}

// TestBlockedRootBackoffMatchesConfiguredErrorBackoff is the regression test for
// the backoff RunOnce records when a root comes back blocked.
//
// It fails against the uncorrected driver, whose blocked case writes a one-hour
// deadline, and passes once that case writes Config.ErrorBackoff:
//
//   - the recorded window is the configured backoff, not a fixed hour;
//   - suppression: while the window is open the root is not resumed again;
//   - re-check: once the window has elapsed the same root becomes eligible and
//     is resumed, so a human or safety unblock is observed within the configured
//     backoff rather than an hour later.
func TestBlockedRootBackoffMatchesConfiguredErrorBackoff(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ResultClassification
	}{
		{name: "human block", err: executive.ErrRunBlocked, want: ResultBlockedHuman},
		{name: "safety block", err: executive.ErrIndeterminateToolExecution, want: ResultBlockedSafety},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resumer := &blockedBackoffResumer{err: tc.err}
			observer := &blockedBackoffObserver{}
			d := newBlockedBackoffDriver(t, resumer, observer)
			ctx := context.Background()

			// First sweep: the root is claimed, driven once, and classified as
			// blocked.
			if _, err := d.RunOnce(ctx); err != nil {
				t.Fatalf("first RunOnce: %v", err)
			}
			if got := resumer.resumeCount(); got != 1 {
				t.Fatalf("resume attempts after the first sweep = %d, want 1", got)
			}
			if got := observer.last(); got != tc.want {
				t.Fatalf("classification after the first sweep = %s, want %s", got, tc.want)
			}

			// The deadline written by the blocked case is the configured
			// backoff.
			remaining, ok := blockedBackoffRemaining(d, blockedBackoffRootID)
			if !ok {
				t.Fatalf("no backoff deadline recorded for root %d after a blocked outcome", blockedBackoffRootID)
			}
			if remaining <= 0 {
				t.Fatalf("backoff window for a blocked root = %s, want a future deadline", remaining)
			}
			if remaining > blockedBackoffWindow+blockedBackoffSlack {
				t.Fatalf("backoff window for a blocked root = %s, want at most %s (configured ErrorBackoff %s plus slack): a blocked root must not be parked for a fixed hour", remaining, blockedBackoffWindow+blockedBackoffSlack, blockedBackoffWindow)
			}

			// Suppression: a second sweep inside the window must not re-drive
			// the root.
			if _, err := d.RunOnce(ctx); err != nil {
				t.Fatalf("second RunOnce: %v", err)
			}
			if got := resumer.resumeCount(); got != 1 {
				t.Fatalf("resume attempts after a sweep inside the backoff window = %d, want 1", got)
			}

			// Re-check: once the configured backoff has elapsed the root is
			// eligible again.
			time.Sleep(blockedBackoffRecheckWait)
			if _, err := d.RunOnce(ctx); err != nil {
				t.Fatalf("third RunOnce: %v", err)
			}
			if got := resumer.resumeCount(); got != 2 {
				t.Fatalf("resume attempts after the configured backoff elapsed = %d, want 2", got)
			}
			if got := observer.last(); got != tc.want {
				t.Fatalf("classification after the re-check sweep = %s, want %s", got, tc.want)
			}
		})
	}
}
