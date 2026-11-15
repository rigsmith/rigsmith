package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
	"github.com/rigsmith/rigsmith/internal/shiprig/forge"
	"github.com/spf13/cobra"
)

// fakeGH answers forge-release's gh calls as an authenticated gh against a
// repo whose releases it keeps, and records each create.
type fakeGH struct {
	mu       sync.Mutex
	releases map[string]bool
	creates  [][]string
}

func stubGH(t *testing.T) *fakeGH {
	t.Helper()
	gh := &fakeGH{releases: map[string]bool{}}
	was := newForgeRunner
	newForgeRunner = func(*cobra.Command, []string) forge.Runner {
		return func(dir, name string, args ...string) (string, error) {
			gh.mu.Lock()
			defer gh.mu.Unlock()
			if name != "gh" || len(args) < 2 {
				return "", errors.New("unexpected " + name)
			}
			switch {
			case args[0] == "auth":
				return "", nil
			case args[1] == "view":
				if !gh.releases[args[2]] {
					return "", errors.New("release not found")
				}
				if slices.Contains(args, "--json") {
					return "https://github.com/acme/repo/releases/tag/" + args[2] + "\n", nil
				}
				return "", nil
			case args[1] == "create":
				gh.releases[args[2]] = true
				gh.creates = append(gh.creates, args)
				return "", nil
			}
			return "", nil
		}
	}
	t.Cleanup(func() { newForgeRunner = was })
	return gh
}

func runForgeRelease(t *testing.T, args ...string) (forgeReleaseOutput, string) {
	t.Helper()
	cmd := newForgeReleaseCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"--forge", "github", "--json"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("forge-release: %v\n%s%s", err, out.String(), errOut.String())
	}
	var res forgeReleaseOutput
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("stdout is not the JSON: %v\n%s", err, out.String())
	}
	return res, errOut.String()
}

func hasTag(t *testing.T, root, tag string) bool {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "tag", "--list", tag).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out)) == tag
}

const combinedCfg = `{"ignore": ["app"], "forgeRelease": {"mode": "combined", "name": "Acme ${version}"}}`

// The CI entry point: after publish has tagged lib and done at HEAD, it makes
// ONE release for them, tagged v1.0.0 (created here, there being no remote to
// push to), and reports it as JSON with its URL. A second run finds it and
// creates nothing.
func TestForgeReleaseCombinedFromTagsAtHead(t *testing.T) {
	root := planRepo(t, combinedCfg)
	writeFile(t, root, "packages/lib/CHANGELOG.md", "# lib\n\n## 1.0.0\n\n- lib entry\n")
	writeFile(t, root, "packages/done/CHANGELOG.md", "# done\n\n## 1.0.0\n\n- done entry\n")
	tagRepo(t, root, "lib@1.0.0", "done@1.0.0")
	gh := stubGH(t)

	res, _ := runForgeRelease(t)
	if res.Mode != "combined" || len(res.Releases) != 1 {
		t.Fatalf("output = %+v", res)
	}
	r := res.Releases[0]
	if r.Tag != "v1.0.0" || r.Name != "Acme 1.0.0" || r.Action != forge.ActionCreated || !r.Latest ||
		r.URL != "https://github.com/acme/repo/releases/tag/v1.0.0" || strings.Join(r.Packages, ",") != "done,lib" {
		t.Errorf("release = %+v", r)
	}
	if len(gh.creates) != 1 {
		t.Fatalf("creates = %v", gh.creates)
	}
	notes := gh.creates[0][slices.Index(gh.creates[0], "--notes")+1]
	if notes != "## done@1.0.0\n\n- done entry\n\n## lib@1.0.0\n\n- lib entry" {
		t.Errorf("notes = %q", notes)
	}
	if !hasTag(t, root, "v1.0.0") {
		t.Error("the combined tag was not created")
	}

	res, _ = runForgeRelease(t)
	if len(res.Releases) != 1 || res.Releases[0].Action != forge.ActionExists || len(gh.creates) != 1 {
		t.Errorf("re-run: %+v, creates %d", res, len(gh.creates))
	}
}

// --dry-run says what it would make and makes nothing, the tag included.
func TestForgeReleaseDryRun(t *testing.T) {
	root := planRepo(t, combinedCfg)
	tagRepo(t, root, "lib@1.0.0", "done@1.0.0")
	gh := stubGH(t)

	res, progress := runForgeRelease(t, "--dry-run")
	if !res.DryRun || len(res.Releases) != 1 || res.Releases[0].Action != forge.ActionWouldCreate {
		t.Fatalf("output = %+v", res)
	}
	if len(gh.creates) != 0 || hasTag(t, root, "v1.0.0") {
		t.Errorf("dry run created something: creates %v", gh.creates)
	}
	if !strings.Contains(progress, "would tag v1.0.0") {
		t.Errorf("progress (stderr) = %q", progress)
	}
}

// Per-package mode releases exactly the packages tagged at HEAD.
func TestForgeReleasePerPackageReleasesOnlyWhatIsTaggedAtHead(t *testing.T) {
	root := planRepo(t, `{}`)
	tagRepo(t, root, "lib@1.0.0")
	gh := stubGH(t)

	res, _ := runForgeRelease(t)
	if res.Mode != "per-package" || len(res.Releases) != 1 || res.Releases[0].Tag != "lib@1.0.0" {
		t.Fatalf("output = %+v", res)
	}
	if len(gh.creates) != 1 || gh.creates[0][2] != "lib@1.0.0" || slices.Contains(gh.creates[0], "--latest") {
		t.Errorf("creates = %v", gh.creates)
	}
}

// Nothing tagged at HEAD is a clean no-op with an empty list.
func TestForgeReleaseNothingAtHead(t *testing.T) {
	planRepo(t, combinedCfg)
	gh := stubGH(t)
	res, _ := runForgeRelease(t)
	if res.Releases == nil || len(res.Releases) != 0 || res.Message == "" || len(gh.creates) != 0 {
		t.Errorf("output = %+v", res)
	}
}

// `shiprig tag` makes the combined tag beside the package tags, so the push
// step carries it with them.
func TestTagCreatesTheCombinedTag(t *testing.T) {
	root := planRepo(t, combinedCfg)
	cmd := newTagCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tag: %v\n%s", err, out.String())
	}
	for _, tag := range []string{"lib@1.0.0", "done@1.0.0", "v1.0.0"} {
		if !hasTag(t, root, tag) {
			t.Errorf("tag %s missing; output:\n%s", tag, out.String())
		}
	}
}

// publishTagging runs `shiprig publish --yes` (tagging on) with the npm adapter
// recording.
func publishTagging(t *testing.T) ([]plugin.PublishRequest, string, error) {
	t.Helper()
	var mu sync.Mutex
	var reqs []plugin.PublishRequest
	was := openPublishWorkspace
	openPublishWorkspace = func() (*commands.Workspace, error) {
		ws, err := commands.Open()
		if err != nil {
			return nil, err
		}
		node, _ := ws.EcosystemFor("node")
		ws.Registry.Register(recordingNode{Ecosystem: node, mu: &mu, reqs: &reqs})
		return ws, nil
	}
	t.Cleanup(func() { openPublishWorkspace = was })
	cmd := newPublishCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--yes"})
	err := cmd.Execute()
	return reqs, out.String(), err
}

// A combined release needing one version, over packages publish would tag on
// different ones, fails before any registry is asked to publish.
func TestPublishRefusesMismatchedCombinedVersionsBeforePublishing(t *testing.T) {
	root := planRepo(t, `{"forgeRelease": {"mode": "combined"}}`)
	reqs, out, err := publishTagging(t)
	if err == nil {
		t.Fatalf("publish succeeded; output:\n%s", out)
	}
	for _, want := range []string{"app 2.0.0", "lib 1.0.0", "done 1.0.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if len(reqs) != 0 || hasTag(t, root, "lib@1.0.0") {
		t.Errorf("publish went ahead: %d request(s)", len(reqs))
	}
}

// Without ${version} in the templates, publish goes ahead and makes the
// combined tag with the package tags.
func TestPublishMakesTheCombinedTag(t *testing.T) {
	root := planRepo(t, `{"forgeRelease": {"mode": "combined", "tag": "release", "name": "Release"}}`)
	reqs, out, err := publishTagging(t)
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	if len(reqs) == 0 {
		t.Fatal("nothing published")
	}
	for _, tag := range []string{"lib@1.0.0", "app@2.0.0", "release"} {
		if !hasTag(t, root, tag) {
			t.Errorf("tag %s missing; output:\n%s", tag, out)
		}
	}
}

// `shiprig release` checks the plan's versions before the pipeline runs, so a
// mismatch stops it before publish.
func TestReleaseRefusesMismatchedCombinedVersionsUpFront(t *testing.T) {
	root := planRepo(t, `{"ignore": ["app"], "forgeRelease": {"mode": "combined"}}`)
	writeFile(t, root, ".changeset/two-bumps.md", "---\n\"lib\": patch\n\"done\": minor\n---\n\nTwo bumps.\n")
	cmd := newReleaseCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--dry-run", "--no-ui"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("release ran; output:\n%s", out.String())
	}
	for _, want := range []string{"lib 1.0.1", "done 1.1.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if strings.Contains(out.String(), "publish") {
		t.Errorf("the pipeline started:\n%s", out.String())
	}
}
