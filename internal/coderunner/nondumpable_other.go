//go:build !linux

package coderunner

// MakeProcessNonDumpable is a no-op outside Linux, where the runner is not deployed.
func MakeProcessNonDumpable() error { return nil }
