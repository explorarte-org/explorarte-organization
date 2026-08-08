//go:build integration

package sleep

// SetPageFetchSizeForTest temporarily overrides the ListEligible keyset page
// size so an integration test can exercise the multi-page path with a
// handful of fixture rows instead of thousands. Call the returned restore
// func to put the real page size back.
func SetPageFetchSizeForTest(n int) (restore func()) {
	previous := pageFetchSize
	pageFetchSize = n
	return func() { pageFetchSize = previous }
}
