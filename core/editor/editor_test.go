package editor

import (
	"os/exec"
	"reflect"
	"testing"
)

func TestResolveArgv(t *testing.T) {
	notFound := func(string) (string, error) { return "", exec.ErrNotFound }
	noBundle := func(string) bool { return false }
	// found makes lookPath succeed only for the named commands.
	found := func(names ...string) func(string) (string, error) {
		set := map[string]bool{}
		for _, n := range names {
			set[n] = true
		}
		return func(cmd string) (string, error) {
			if set[cmd] {
				return "/usr/local/bin/" + cmd, nil
			}
			return "", exec.ErrNotFound
		}
	}
	// bundle makes bundleExists true only for the named paths.
	bundle := func(paths ...string) func(string) bool {
		set := map[string]bool{}
		for _, p := range paths {
			set[p] = true
		}
		return func(p string) bool { return set[p] }
	}

	const path = "/tmp/.rig.json"
	cases := []struct {
		name         string
		visual       string
		editorEnv    string
		goos         string
		lookPath     func(string) (string, error)
		bundleExists func(string) bool
		want         []string
	}{
		{
			name:     "VISUAL wins",
			visual:   "code --wait",
			lookPath: notFound, bundleExists: noBundle, goos: "darwin",
			want: []string{"code", "--wait", path},
		},
		{
			name:     "VISUAL with args is split",
			visual:   "code -w",
			lookPath: found("code"), bundleExists: noBundle, goos: "darwin",
			want: []string{"code", "-w", path}, // honored verbatim, no auto-detect
		},
		{
			name:      "EDITOR fallback when VISUAL blank",
			visual:    "  ",
			editorEnv: "nano",
			lookPath:  notFound, bundleExists: noBundle, goos: "linux",
			want: []string{"nano", path},
		},
		{
			name:     "detect code on PATH",
			lookPath: found("code"), bundleExists: noBundle, goos: "darwin",
			want: []string{"code", "--wait", path},
		},
		{
			name:     "prefer code over cursor when both present",
			lookPath: found("code", "cursor"), bundleExists: noBundle, goos: "linux",
			want: []string{"code", "--wait", path},
		},
		{
			name:     "fall through to cursor on PATH",
			lookPath: found("cursor"), bundleExists: noBundle, goos: "linux",
			want: []string{"cursor", "--wait", path},
		},
		{
			name:     "macOS app bundle when no CLI on PATH",
			lookPath: notFound, bundleExists: bundle("/Applications/Visual Studio Code.app"), goos: "darwin",
			want: []string{"open", "-W", "-a", "Visual Studio Code", path},
		},
		{
			name:     "bundle ignored off darwin",
			lookPath: notFound, bundleExists: bundle("/Applications/Visual Studio Code.app"), goos: "linux",
			want: []string{"vi", path},
		},
		{
			name:     "terminal default vi on unix",
			lookPath: notFound, bundleExists: noBundle, goos: "linux",
			want: []string{"vi", path},
		},
		{
			name:     "terminal default notepad on windows",
			lookPath: notFound, bundleExists: noBundle, goos: "windows",
			want: []string{"notepad", path},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveArgv(tc.visual, tc.editorEnv, tc.goos, tc.lookPath, tc.bundleExists, path)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("resolveArgv() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("  ", "", "x", "y"); got != "x" {
		t.Errorf("firstNonEmpty = %q, want %q", got, "x")
	}
	if got := firstNonEmpty("", "  ", "\t"); got != "" {
		t.Errorf("firstNonEmpty all-blank = %q, want empty", got)
	}
}
