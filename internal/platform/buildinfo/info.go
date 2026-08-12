package buildinfo

import "fmt"

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	// MigrationTip is the highest migration version compiled into this
	// binary. Serving it next to the commit turns deployment drift into a
	// two-number comparison against the database tip: a binary reporting
	// 40 against a database at 41 is the whole diagnosis, available in
	// seconds from an endpoint that answers even while readiness fails.
	MigrationTip int64 `json:"migration_tip"`
}

func (i Info) String() string {
	return fmt.Sprintf(
		"explorarte-organization version=%s commit=%s build_time=%s",
		valueOr(i.Version, "dev"),
		valueOr(i.Commit, "unknown"),
		valueOr(i.BuildTime, "unknown"),
	)
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
