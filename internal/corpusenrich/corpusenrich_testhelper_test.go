package corpusenrich

// batchEndpointForTest swaps the package-level endpoint for the
// duration of a test and returns a restore function -- keeps tests from
// ever touching the real Semantic Scholar API.
func batchEndpointForTest(url string) func() {
	original := batchEndpoint
	batchEndpoint = url
	return func() { batchEndpoint = original }
}
