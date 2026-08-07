package main

import "os"

// executiveCommandEntry preserves the existing monolithic orgctl dispatcher
// unchanged while R22 is developed in parallel with R21. It is intentionally
// limited to the explicit `orgctl executive ...` namespace. The final post-R21
// stabilization may fold this branch into run() once adjacent CLI contracts are
// stable.
func init() {
	if len(os.Args) < 2 || os.Args[1] != "executive" {
		return
	}
	os.Exit(runExecutive(os.Args[2:], os.Stdout, os.Stderr))
}
