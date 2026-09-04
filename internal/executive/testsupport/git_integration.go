//go:build integration

// Package testsupport contains integration-only helpers for executive tests.
package testsupport

import (
	"os"
	"os/exec"
)

// LookPath reports whether the integration fixture can use the requested
// executable without putting process execution in the executive package.
func LookPath(name string) error {
	_, err := exec.LookPath(name)
	return err
}

// RunGit executes a fixture-local Git command with prompting disabled.
func RunGit(directory string, args ...string) ([]byte, error) {
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return command.CombinedOutput()
}
