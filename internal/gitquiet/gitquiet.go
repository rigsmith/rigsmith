// Package gitquiet stops git from starting background work in the throwaway
// repositories tests create.
//
// Every `git commit` spawns `git maintenance run --auto --quiet --detach`. The
// detach is the problem: the process outlives the commit that started it and
// keeps the repository open, writing into .git/objects. A test that commits and
// then returns hands its directory to `t.TempDir`'s cleanup, which walks the
// tree and removes it — and if the maintenance process creates anything under
// .git/objects/pack between the read and the unlink, RemoveAll fails with
// "directory not empty" and the test fails after it had already passed.
//
// It reads as a mysterious failure in whichever test drew the short straw:
//
//	--- FAIL: TestStackForgetRepoKeepsOtherProposals
//	    testing.go:1464: TempDir RemoveAll cleanup: unlinkat
//	    …/001/.git/objects/pack: directory not empty
//
// The line number is the tell — testing.go, not the test — but only if you look.
// It cost a release build a red tick before anyone did.
//
// Turning maintenance off stops the process being spawned at all, rather than
// racing it. The settings go in the environment rather than in each repository
// because git passes these to every child process, so one call covers every
// repository a test makes, including ones created deep inside the code under
// test. gc.auto is set too for git old enough to run `gc --auto` directly.
//
// Import it for its effect from any test package that makes git repositories:
//
//	import _ "github.com/rigsmith/rigsmith/internal/gitquiet"
package gitquiet

import (
	"os"
	"strconv"
)

func init() { Apply() }

// Apply adds the settings to this process's environment, keeping any
// GIT_CONFIG_* entries already there — a caller may have set some for its own
// reasons, and the indices have to stay contiguous for git to read them.
func Apply() {
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	for _, kv := range [][2]string{
		{"maintenance.auto", "false"},
		{"gc.auto", "0"},
	} {
		os.Setenv("GIT_CONFIG_KEY_"+strconv.Itoa(n), kv[0])
		os.Setenv("GIT_CONFIG_VALUE_"+strconv.Itoa(n), kv[1])
		n++
	}
	os.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(n))
}
