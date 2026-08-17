//go:build unix

package coderunner

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts c as the leader of its own process group so that
// every descendant it forks (compiled test binaries, go tool subprocesses,
// etc.) inherits that group and can be reached by a single group-wide kill.
func setProcessGroup(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}

// killProcessGroup sends SIGKILL to the entire process group led by c, not
// just the direct child, so descendants cannot outlive lease/context
// cancellation and keep mutating the staging workspace. ESRCH (already gone)
// is not an error: the group may have exited on its own between the
// cancellation signal and this call.
func killProcessGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	if err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		// Fall back to killing the direct process: on some platforms/edge
		// cases the group kill can fail (e.g. Setpgid didn't take before
		// the process exec'd) while the direct process kill still works.
		return c.Process.Kill()
	}
	return nil
}
