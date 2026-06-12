package project

import (
	"testing"

	"github.com/rigsmith/core/pathmap"
)

// Flatten cases anchored on real slugs observed in ~/.claude/projects.
func TestFlatten_RealCases(t *testing.T) {
	cases := map[string]string{
		"/Users/john/Git/rigsmith":                             "-Users-john-Git-rigsmith",
		"/Users/john/Git/Maryland.Avalonia":                    "-Users-john-Git-Maryland-Avalonia",
		"/Users/john/Git/gitninja_worktrees/foggy-spark":       "-Users-john-Git-gitninja-worktrees-foggy-spark",
		"/Users/john/Git/nuxt-roost/.dmux/worktrees/tidy-repo": "-Users-john-Git-nuxt-roost--dmux-worktrees-tidy-repo",
		"/":                          "-",
		`C:\Users\John\Git\rigsmith`: "C--Users-John-Git-rigsmith",
	}
	for in, want := range cases {
		if got := Flatten(in); got != want {
			t.Errorf("Flatten(%q) = %q, want %q", in, got, want)
		}
	}
}

func resolver(home, os string) *pathmap.Resolver {
	return pathmap.NewResolver(pathmap.MapFolders{"HOME": home}, os, nil)
}

func TestRewrite_MacToWindows(t *testing.T) {
	src := map[string]string{"HOME": "/Users/john"}
	tgt := resolver(`C:\Users\John`, pathmap.OSWindows)
	slug, cwd, st := Rewrite("/Users/john/Git/rigsmith", src, pathmap.OSMacOS, tgt)
	if st != pathmap.StatusResolved {
		t.Fatalf("status %v", st)
	}
	if cwd != `C:\Users\John\Git\rigsmith` {
		t.Errorf("cwd = %q", cwd)
	}
	if slug != "C--Users-John-Git-rigsmith" {
		t.Errorf("slug = %q", slug)
	}
}

func TestRewrite_MacToLinux(t *testing.T) {
	src := map[string]string{"HOME": "/Users/john"}
	tgt := resolver("/home/john", pathmap.OSLinux)
	slug, cwd, st := Rewrite("/Users/john/Git/rigsmith", src, pathmap.OSMacOS, tgt)
	if st != pathmap.StatusResolved || cwd != "/home/john/Git/rigsmith" || slug != "-home-john-Git-rigsmith" {
		t.Fatalf("got slug=%q cwd=%q st=%v", slug, cwd, st)
	}
}

func TestRewrite_Identity(t *testing.T) {
	src := map[string]string{"HOME": "/Users/john"}
	tgt := resolver("/Users/john", pathmap.OSMacOS)
	slug, cwd, st := Rewrite("/Users/john/Git/x", src, pathmap.OSMacOS, tgt)
	if st != pathmap.StatusResolved || cwd != "/Users/john/Git/x" || slug != "-Users-john-Git-x" {
		t.Fatalf("got slug=%q cwd=%q st=%v", slug, cwd, st)
	}
}

// A cwd outside any known folder can't be translated → fall back to the original
// slug unchanged, status Unconfigured (still lands on disk: "restore anyway").
func TestRewrite_NotUnderHomeFallsBack(t *testing.T) {
	src := map[string]string{"HOME": "/Users/john"}
	tgt := resolver(`C:\Users\John`, pathmap.OSWindows)
	slug, cwd, st := Rewrite("/opt/shared/proj", src, pathmap.OSMacOS, tgt)
	if st != pathmap.StatusUnconfigured {
		t.Fatalf("status %v", st)
	}
	if cwd != "/opt/shared/proj" || slug != "-opt-shared-proj" {
		t.Errorf("expected unchanged, got slug=%q cwd=%q", slug, cwd)
	}
}
