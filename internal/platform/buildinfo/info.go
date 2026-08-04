package buildinfo

import "fmt"

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
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
