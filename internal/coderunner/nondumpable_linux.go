//go:build linux

package coderunner

import "syscall"

// prSetDumpable is PR_SET_DUMPABLE from <linux/prctl.h>.
const prSetDumpable = 4

// MakeProcessNonDumpable marks the code-runner's own process non-dumpable.
//
// External audit A1 (2026-09-27): a Go test a mission proposes runs as a subprocess with the runner's
// own uid, and could read /proc/1/environ -- the controller's environment, database password included.
// subprocessEnv keeps credentials out of the subprocess's OWN environment; it did nothing about the
// subprocess reading its parent's. A non-dumpable process's /proc/<pid>/environ, mem and fd are owned by
// root and refused to processes without CAP_SYS_PTRACE, same uid included. It needs no capability, and
// it is not inherited across the exec of the commands the runner starts.
func MakeProcessNonDumpable() error {
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0); errno != 0 {
		return errno
	}
	return nil
}
