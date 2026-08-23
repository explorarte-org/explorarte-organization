package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/platform/buildinfo"
)

// FleetBuildReader reports what the RUNNING fleet is, which is never what the
// process asking is.
//
// This interface exists because the first read-only run against production
// got that exactly backwards. The observer filled runtime.binary.observed_sha
// and runtime.schema.compatibility from its own buildinfo, so a reconciler
// whose entire purpose is to be a different process from the runtime it
// reconciles reported its own identity as the fleet's -- and the admission
// refusal that followed was computed from the observer's migration tip
// against the fleet's database. The shape of the answer was right and the
// subject of it was wrong, which is the more dangerous of the two mistakes
// because the output still looks like a diagnosis.
//
// A reader that cannot see the fleet must report nothing rather than fall
// back to the local binary. An unobserved build denies admission and says so;
// a self-reported one denies or grants admission for a reason that is not
// about the thing being judged.
type FleetBuildReader interface {
	FleetBuild(ctx context.Context) (buildinfo.Info, error)
}

// HTTPBuild reads a service's own /version document.
//
// Asking the process is the only way to learn what it is. A pinned path in a
// launcher says what was intended to start; the answer from the process says
// what is actually serving, and those come apart precisely when a deployment
// half-succeeded, which is the case worth detecting.
type HTTPBuild struct {
	URL     string
	Client  *http.Client
	Timeout time.Duration
}

func NewHTTPBuild(url string) *HTTPBuild {
	return &HTTPBuild{URL: url, Timeout: 5 * time.Second}
}

func (h *HTTPBuild) FleetBuild(ctx context.Context) (buildinfo.Info, error) {
	if h.URL == "" {
		return buildinfo.Info{}, fmt.Errorf("no fleet version URL configured")
	}
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.URL, nil)
	if err != nil {
		return buildinfo.Info{}, err
	}
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return buildinfo.Info{}, fmt.Errorf("asking %s what it is: %w", h.URL, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return buildinfo.Info{}, fmt.Errorf("%s answered HTTP %d", h.URL, response.StatusCode)
	}
	var info buildinfo.Info
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		return buildinfo.Info{}, fmt.Errorf("decoding the version document from %s: %w", h.URL, err)
	}
	return info, nil
}
