package secrets

import (
	"os"
	"runtime"
	"syscall"
	"testing"
)

// A permission test is only a permission test if its fixture has the
// permissions it asked for.
//
// os.WriteFile's mode is a request: the process umask clears bits from it.
// Under a service running with umask 0077 a file asked for as 0644 lands as
// 0600, so the tests that check a permissive credential is REJECTED were
// writing a safe file and then failing because nothing rejected it. They
// passed everywhere a human ran them and failed only under systemd, which is
// the worst place to find out.
//
// This asserts the property directly, under the umask that exposed it, so the
// fixtures cannot quietly stop testing what they claim to test.
func TestPermissiveFixturesAreActuallyPermissive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission test")
	}
	previous := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(previous) })

	path := writePrivateKey(t, 0o644)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("the fixture asked for 0644 and got %#o; a restrictive file cannot test that permissive files are refused", got)
	}
	if _, err := LoadEd25519PrivateKey(path); err == nil {
		t.Fatal("a world-readable private key must be refused")
	}
}

// And the safe fixture must stay safe, so the accepting case is not passing
// for the wrong reason either.
func TestRestrictiveFixturesAreActuallyRestrictive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission test")
	}
	previous := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(previous) })

	path := writePrivateKey(t, 0o600)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("the fixture asked for 0600 and got %#o", got)
	}
	if _, err := LoadEd25519PrivateKey(path); err != nil {
		t.Fatalf("a correctly permissioned key must load: %v", err)
	}
}
