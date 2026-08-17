//go:build !unix

package coderunner

import "os/exec"

// setProcessGroup is a direct-process fallback on non-unix platforms: no
// process-group semantics are available, so only the immediate child is
// managed. Production CodeRunner runs on Linux; this exists so the package
// still builds and behaves safely (best-effort direct kill) elsewhere.
func setProcessGroup(c *exec.Cmd) {}

func killProcessGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	return c.Process.Kill()
}
