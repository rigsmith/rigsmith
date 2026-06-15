package forge

import (
	"errors"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// failView returns a responder that fails any `<cli> release view` (so the
// release is treated as not-yet-existing and gets created) and succeeds
// everything else (CLI ready, etc.).
func failView() func(recordedCall) (string, error) {
	return func(call recordedCall) (string, error) {
		if call.has("view") {
			return "", errors.New("exit status 1")
		}
		return "", nil
	}
}

// findCall returns the first recorded call whose name matches and that contains
// all of the given args, or nil.
func findCall(runner *recordingRunner, name string, args ...string) *recordedCall {
	for i := range runner.calls {
		c := &runner.calls[i]
		if c.name != name {
			continue
		}
		ok := true
		for _, a := range args {
			if !c.has(a) {
				ok = false
				break
			}
		}
		if ok {
			return c
		}
	}
	return nil
}

func TestProviderMatches(t *testing.T) {
	cases := []struct {
		p    Provider
		url  string
		want bool
	}{
		{githubProvider{}, "git@github.com:o/r.git", true},
		{githubProvider{}, "https://gitlab.com/o/r.git", false},
		{gitlabProvider{}, "https://gitlab.com/o/r.git", true},
		{gitlabProvider{}, "https://github.com/o/r.git", false},
		{giteaProvider{}, "https://git.example.com/o/r.git", false}, // self-hosted: explicit-only
		{giteaProvider{}, "https://gitea.com/o/r.git", false},
	}
	for _, c := range cases {
		if got := c.p.Matches(c.url); got != c.want {
			t.Errorf("%s.Matches(%q) = %v, want %v", c.p.Name(), c.url, got, c.want)
		}
	}
}

func TestGitLabExplicit_CreatesReleaseWithName(t *testing.T) {
	runner := &recordingRunner{responder: failView()}

	ok, message := runService(t, []plugin.Package{pkg("pkg-a", "1.2.0")}, Selection{Forge: "gitlab"}, t.TempDir(), runner)
	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}
	create := findCall(runner, "glab", "create", "pkg-a@1.2.0")
	if create == nil {
		t.Fatalf("no glab release create call; calls: %v", runner.calls)
	}
	// GitLab uses --name (not gh's --title).
	if !create.has("--name") {
		t.Errorf("glab create should use --name; got %v", create.args)
	}
}

func TestGiteaExplicit_CreatesReleaseWithTagFlag(t *testing.T) {
	// tea has no `release view`; ReleaseExists lists releases. An empty list ⇒
	// the release does not exist ⇒ create.
	runner := &recordingRunner{} // default responder returns "" / nil for everything

	ok, message := runService(t, []plugin.Package{pkg("pkg-a", "1.2.0")}, Selection{Forge: "gitea"}, t.TempDir(), runner)
	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}
	// Readiness is `tea login list`, existence is `tea release list`.
	if findCall(runner, "tea", "login", "list") == nil {
		t.Errorf("expected `tea login list` readiness probe; calls: %v", runner.calls)
	}
	create := findCall(runner, "tea", "create", "--tag", "pkg-a@1.2.0")
	if create == nil {
		t.Fatalf("no tea release create call with --tag; calls: %v", runner.calls)
	}
	if !create.has("--note") {
		t.Errorf("tea create should use --note; got %v", create.args)
	}
}

func TestGiteaReleaseExists_FromList_SkipsCreate(t *testing.T) {
	runner := &recordingRunner{
		responder: func(call recordedCall) (string, error) {
			if call.name == "tea" && call.has("release") && call.has("list") {
				// tea release list output: the tag appears as a field.
				return "12  pkg-a@1.2.0  Some release\n", nil
			}
			return "", nil
		},
	}

	ok, _ := runService(t, []plugin.Package{pkg("pkg-a", "1.2.0")}, Selection{Forge: "gitea"}, t.TempDir(), runner)
	if !ok {
		t.Fatal("Run ok = false, want true")
	}
	if c := findCall(runner, "tea", "create"); c != nil {
		t.Fatalf("unexpected create when release already listed: %v", c)
	}
}

func TestAutoMode_GitLabOrigin_CreatesRelease(t *testing.T) {
	runner := &recordingRunner{
		responder: func(call recordedCall) (string, error) {
			if isGitRemote(call) {
				return "https://gitlab.com/owner/repo.git", nil
			}
			if call.has("view") {
				return "", errors.New("exit status 1")
			}
			return "", nil
		},
	}

	ok, message := runService(t, []plugin.Package{pkg("pkg-a", "1.2.0")}, selAuto, t.TempDir(), runner)
	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}
	if findCall(runner, "glab", "auth", "status") == nil {
		t.Errorf("auto should probe glab readiness; calls: %v", runner.calls)
	}
	if findCall(runner, "glab", "create", "pkg-a@1.2.0") == nil {
		t.Fatalf("no glab create for gitlab.com origin; calls: %v", runner.calls)
	}
	// And it must NOT have fallen through to gh.
	if findCall(runner, "gh", "create") != nil {
		t.Error("gitlab origin should not create a github release")
	}
}

func TestGitLabExplicit_AttachesAssets(t *testing.T) {
	runner := &recordingRunner{responder: failView()}
	packages := []plugin.Package{{Name: "core", Version: "1.2.0", Dir: "core"}}
	attach := map[string][]string{"core": {"/dist/core_1.2.0_linux_amd64.tar.gz"}}

	ok, _ := Run(packages, map[string]string{"core": "node"}, attach, config.Default(), Selection{Forge: "gitlab"}, t.TempDir(), runner.run, nil)
	if !ok {
		t.Fatal("Run ok = false, want true")
	}
	upload := findCall(runner, "glab", "upload", "core@1.2.0", "/dist/core_1.2.0_linux_amd64.tar.gz")
	if upload == nil {
		t.Fatalf("no glab release upload call; calls: %v", runner.calls)
	}
}

func TestGiteaExplicit_AttachToExistingRelease_SkippedNotFailed(t *testing.T) {
	runner := &recordingRunner{} // empty list ⇒ create; then attach is attempted
	var reported []string
	report := func(lines ...string) { reported = append(reported, lines...) }

	packages := []plugin.Package{pkg("pkg-a", "1.2.0")}
	attach := map[string][]string{"pkg-a": {"/dist/pkg-a.tar.gz"}}

	ok, _ := Run(packages, nil, attach, config.Default(), Selection{Forge: "gitea"}, t.TempDir(), runner.run, report)
	if !ok {
		t.Fatal("Run ok = false, want true — an unsupported asset upload is a skip, not a failure")
	}
	// No upload call should be issued for gitea.
	for _, c := range runner.calls {
		if c.has("upload") {
			t.Fatalf("unexpected upload call for gitea: %v", c)
		}
	}
	// And the skip is reported.
	found := false
	for _, line := range reported {
		if strings.Contains(line, "gitea") && strings.Contains(line, "cannot attach") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a reported asset-skip note; reports: %v", reported)
	}
}

func TestExplicitForge_CLINotReady_SkipsToTagsOnly(t *testing.T) {
	// glab auth status fails ⇒ not ready ⇒ skip (degrade to tags-only), no create.
	runner := &recordingRunner{
		responder: func(call recordedCall) (string, error) {
			if call.name == "glab" && call.has("auth") {
				return "", errors.New("not logged in")
			}
			return "", nil
		},
	}

	ok, message := runService(t, []plugin.Package{pkg("pkg-a", "1.2.0")}, Selection{Forge: "gitlab"}, t.TempDir(), runner)
	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}
	if findCall(runner, "glab", "create") != nil {
		t.Fatalf("should not create when CLI not ready; calls: %v", runner.calls)
	}
	want := "gitlab CLI unavailable or not authenticated; skipped releases (tags only)."
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
}

func TestUnknownForge_Skips(t *testing.T) {
	runner := &recordingRunner{}
	ok, message := runService(t, []plugin.Package{pkg("pkg-a", "1.2.0")}, Selection{Forge: "bogus"}, t.TempDir(), runner)
	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("unknown forge should run nothing; calls: %v", runner.calls)
	}
	want := `Unknown forge "bogus"; skipped releases (tags only).`
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
}
