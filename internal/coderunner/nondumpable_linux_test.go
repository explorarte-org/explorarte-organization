//go:build linux

package coderunner

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// External audit A1: a subprocess the runner starts, same uid, must not read the runner's environment.
func TestASubprocessCannotReadTheNonDumpableRunnersEnvironment(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root holds CAP_SYS_PTRACE and reads any process; the property is for the unprivileged runner")
	}
	environ := fmt.Sprintf("/proc/%d/environ", os.Getpid())
	if _, err := os.ReadFile(environ); err != nil {
		t.Skipf("this process cannot read its own environ even before the change: %v", err)
	}
	if err := MakeProcessNonDumpable(); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("cat", environ).CombinedOutput()
	if err == nil {
		t.Fatalf("a same-uid subprocess read the runner's environment (%d bytes)", len(out))
	}
}
