package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ResponseHeaderTimeout measures the wait for the FIRST response byte, which a
// non-streaming completion does not send until it has finished generating. A
// constant there does not bound a header, it bounds generation -- and being a
// constant, it silently overrides whatever RequestTimeout the deployment
// configured.
//
// Every adapter had 90s compiled in while the deployment had set 180s and 600s,
// so calls were cut at a fraction of the bound an operator had chosen and the
// configured value never applied. AUTONOMY-SMOKE-001 lost a run to it twice,
// on two different providers.
//
// This is asserted at the source because the failure mode is a new adapter
// copying the old shape, and no behavioural test fails for code not yet
// written.
func TestNoAdapterHardcodesItsResponseHeaderTimeout(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(entry.Name(), "config.go")
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		source := string(body)
		if !strings.Contains(source, "ResponseHeaderTimeout:") {
			continue
		}
		checked++
		if !strings.Contains(source, "ResponseHeaderTimeout: requestTimeout,") {
			t.Errorf("%s sets ResponseHeaderTimeout to something other than the configured request timeout. "+
				"A constant there overrides the deployment's own bound and cuts calls mid-generation; "+
				"the configured timeout must be the single authority on how long a call may take.", path)
		}
	}
	if checked < 5 {
		t.Fatalf("only %d adapters were examined; the guard is looking in the wrong place", checked)
	}
	t.Logf("%d adapters defer to the configured request timeout", checked)
}
