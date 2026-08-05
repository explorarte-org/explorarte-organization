package modelruntime

import "time"

func mustTime(value string) time.Time {
	v, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return v
}
