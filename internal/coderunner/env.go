package coderunner

import "strings"

// Every command CodeRunner runs -- git, go, make, rg -- is a subprocess of the
// code-runner's own process, and that process legitimately holds real credentials
// (ORG_DATABASE_*, and whatever else it needs for its own task-queue and staging
// work: env.Environ inherits them automatically, as it should). Go's exec.Command
// hands a subprocess the WHOLE of that environment unless c.Env says otherwise, so
// without this, every mission's build/test/fitness gate ran with the same database
// credentials the code-runner itself uses.
//
// That stopped being theoretical on root 1082 (2026-09-21): GO_TEST failed identically
// five times not because of anything the mission changed, but because
// TestSkillForgeReadOnlyWhenDisabled (cmd/orgctl) assumed an unreachable database and
// took its no-database code path -- which does not hold once the test's own subprocess
// can reach the real one. The fix is not that test's assumption; it is that a mission's
// subprocess should never have been able to reach the real database to begin with.
//
// subprocessEnv is what closes that: every command CodeRunner runs gets an EXPLICIT
// environment built from an allowlist of names, not the inherited whole. The allowlist
// is deliberately not a denylist of secrets: a name added to the process environment
// tomorrow for some new purpose is excluded by default, not included until someone
// remembers to deny it. Every name on it is toolchain plumbing -- where binaries and
// caches live, module resolution, locale -- and none of it can carry a credential; this
// package's own callers (git, go, make, rg) are exactly what was verified to need it and
// nothing more (empirically, against real git/go/make on the deployed commit).
var subprocessEnvAllowlist = []string{
	// Where things live and where a command is found.
	"PATH", "HOME",
	// Go's own toolchain configuration: without these the deployment's module
	// cache and offline/pinned toolchain settings are invisible to the
	// subprocess, and `go build`/`vet`/`test` would try to rebuild caches or
	// resolve modules from scratch.
	"GOPATH", "GOCACHE", "GOMODCACHE", "GOFLAGS", "GOTOOLCHAIN", "GOROOT",
	"GOPROXY", "GOSUMDB", "GONOSUMCHECK", "GOPRIVATE", "GO111MODULE",
	// Where temporary files go, and locale/timezone -- neither is secret, and
	// their absence can otherwise change a command's output in confusing ways.
	"TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "LC_CTYPE", "TZ",
}

// subprocessEnv filters environ (ordinarily os.Environ(), the code-runner
// process's own environment) down to exactly the allowlisted names present in
// it, in their original "NAME=value" form. A name absent from environ is
// simply absent from the result -- this never invents a value. It is a pure
// function of its argument so it can be tested without mutating the real
// process environment.
func subprocessEnv(environ []string) []string {
	allowed := make(map[string]struct{}, len(subprocessEnvAllowlist))
	for _, name := range subprocessEnvAllowlist {
		allowed[name] = struct{}{}
	}
	kept := make([]string, 0, len(subprocessEnvAllowlist))
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if _, want := allowed[name]; want {
			kept = append(kept, kv)
		}
	}
	return kept
}
