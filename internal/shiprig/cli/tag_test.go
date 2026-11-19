package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// runTagJSON runs `shiprig tag --json` plus args in the current directory's
// workspace and decodes stdout, which must hold the JSON and nothing else.
func runTagJSON(t *testing.T, args ...string) (tagOutput, string) {
	t.Helper()
	cmd := newTagCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"--json"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tag: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	var res tagOutput
	dec := json.NewDecoder(&stdout)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&res); err != nil {
		t.Fatalf("stdout is not the tag JSON: %v\n%s", err, stdout.String())
	}
	// The decoder reads ahead, so the buffer's remainder says nothing: a
	// second Decode must find the end, not trailing text or another value.
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("stdout has more than the JSON: %v %s", err, extra)
	}
	return res, stderr.String()
}

// tagsByName flattens the report to tag → "action pkg@version ...", packages
// sorted, so a test states the whole expected report in one map.
func tagsByName(res tagOutput) map[string]string {
	got := map[string]string{}
	for _, tg := range res.Tags {
		var pkgs []string
		for _, p := range tg.Packages {
			pkgs = append(pkgs, p.Name+"@"+p.Version)
		}
		sort.Strings(pkgs)
		got[tg.Tag] = tg.Action + " " + strings.Join(pkgs, " ")
	}
	return got
}

// A lockstep repo whose packages share "v${version}": one tag per version,
// naming every package on it, which a reader can't recover from "v1.0.0".
// A dry run creates nothing, and the text report moves to stderr.
func TestTagJSONSharedTagNamesEveryPackage(t *testing.T) {
	root := planRepo(t, `{ "tagTemplate": "v${version}" }`)
	res, stderr := runTagJSON(t, "--dry-run")
	want := map[string]string{
		"v1.0.0": "would-create done@1.0.0 lib@1.0.0",
		"v2.0.0": "would-create app@2.0.0",
	}
	if got := tagsByName(res); !reflect.DeepEqual(got, want) || !res.DryRun {
		t.Errorf("report = %+v (dryRun %v), want %v", got, res.DryRun, want)
	}
	if len(res.Tags) != 2 {
		t.Errorf("%d tag entries, want one per tag: %+v", len(res.Tags), res.Tags)
	}
	if res.Tags[0].Packages[0].Ecosystem != "node" {
		t.Errorf("ecosystem = %q, want node", res.Tags[0].Packages[0].Ecosystem)
	}
	if !strings.Contains(stderr, "would tag v1.0.0") || !strings.Contains(stderr, "2 tag(s)") {
		t.Errorf("text report not on stderr:\n%s", stderr)
	}
	if hasTag(t, root, "v1.0.0") {
		t.Error("a dry run created v1.0.0")
	}
}

// Per-package `name@version` tags (the monorepo default): one package each,
// and the private package isn't tagged.
func TestTagJSONPerPackageTags(t *testing.T) {
	planRepo(t, `{}`)
	res, _ := runTagJSON(t, "--dry-run")
	want := map[string]string{
		"lib@1.0.0":  "would-create lib@1.0.0",
		"app@2.0.0":  "would-create app@2.0.0",
		"done@1.0.0": "would-create done@1.0.0",
	}
	if got := tagsByName(res); !reflect.DeepEqual(got, want) {
		t.Errorf("report = %v, want %v", got, want)
	}
}

// A custom tagTemplate: the tag's name says nothing a reader could parse, and
// the report still names its package. A real run reports "created".
func TestTagJSONCustomTemplate(t *testing.T) {
	root := planRepo(t, `{ "tagTemplate": "release/${name}-${version}" }`)
	res, _ := runTagJSON(t)
	want := map[string]string{
		"release/lib-1.0.0":  "created lib@1.0.0",
		"release/app-2.0.0":  "created app@2.0.0",
		"release/done-1.0.0": "created done@1.0.0",
	}
	if got := tagsByName(res); !reflect.DeepEqual(got, want) || res.DryRun {
		t.Errorf("report = %v (dryRun %v), want %v", got, res.DryRun, want)
	}
	if !hasTag(t, root, "release/app-2.0.0") {
		t.Error("release/app-2.0.0 was not created")
	}
}

// A tag already present is reported as such, with its packages, beside the
// ones the run makes.
func TestTagJSONReportsPresentTags(t *testing.T) {
	root := planRepo(t, `{ "tagTemplate": "v${version}" }`)
	tagRepo(t, root, "v1.0.0")
	res, stderr := runTagJSON(t)
	want := map[string]string{
		"v1.0.0": "exists done@1.0.0 lib@1.0.0",
		"v2.0.0": "created app@2.0.0",
	}
	if got := tagsByName(res); !reflect.DeepEqual(got, want) {
		t.Errorf("report = %v, want %v", got, want)
	}
	if !strings.Contains(stderr, "1 tag(s)") || !strings.Contains(stderr, "1 already present") {
		t.Errorf("summary wrong:\n%s", stderr)
	}
}

// readTagEvents returns the event file's lines as "tag packageName".
func readTagEvents(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var ev map[string]string
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("event %q: %v", line, err)
		}
		if ev["type"] != "git-tag" || len(ev) != 3 {
			t.Errorf("event %q is not @changesets' git-tag shape", line)
		}
		got = append(got, ev["tag"]+" "+ev["packageName"])
	}
	sort.Strings(got)
	return got
}

// With tag events, a tag several packages share gets one event per package
// (a reader maps events to packages by packageName), and a tag already
// present gets none.
func TestTagEventsOnePerPackageOfASharedTag(t *testing.T) {
	root := planRepo(t, `{ "tagTemplate": "v${version}" }`)
	tagRepo(t, root, "v2.0.0")
	events := filepath.Join(t.TempDir(), "events.ndjson")
	res, _ := runTagJSON(t, "--output", events)
	want := []string{"v1.0.0 done", "v1.0.0 lib"}
	if got := readTagEvents(t, events); !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	if got := tagsByName(res); got["v1.0.0"] != "created done@1.0.0 lib@1.0.0" || got["v2.0.0"] != "exists app@2.0.0" {
		t.Errorf("report = %v", got)
	}
}

// publish's tagging phase reports a shared tag's events the same way.
func TestPublishTagEventsOnePerPackageOfASharedTag(t *testing.T) {
	planRepo(t, `{ "tagTemplate": "v${version}" }`)
	events := filepath.Join(t.TempDir(), "events.ndjson")
	if _, out, err := publishTagging(t, "--output", events); err != nil {
		t.Fatalf("publish: %v\n%s", err, out)
	}
	want := []string{"v1.0.0 done", "v1.0.0 lib", "v2.0.0 app"}
	if got := readTagEvents(t, events); !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

// forgeRelease "combined": its tag is listed beside the package tags, marked
// combined, with the packages the release covers.
func TestTagJSONListsTheCombinedTag(t *testing.T) {
	planRepo(t, combinedCfg)
	res, _ := runTagJSON(t, "--dry-run")
	want := map[string]string{
		"lib@1.0.0":  "would-create lib@1.0.0",
		"done@1.0.0": "would-create done@1.0.0",
		"v1.0.0":     "would-create done@1.0.0 lib@1.0.0",
	}
	if got := tagsByName(res); !reflect.DeepEqual(got, want) {
		t.Errorf("report = %v, want %v", got, want)
	}
	for _, tg := range res.Tags {
		combined := tg.Tag == "v1.0.0"
		if tg.Combined != combined || tg.ReleaseOnly != combined {
			t.Errorf("%s combined = %v, releaseOnly = %v, want %v", tg.Tag, tg.Combined, tg.ReleaseOnly, combined)
		}
	}
}

// A lockstep repo whose combined release uses the packages' own tag
// ("v${version}" for both): the one tag is listed once, marked combined, with
// its packages, and not as a release-only entry a reader would skip. The text
// report still shows it once.
func TestTagJSONCombinedTagThatIsThePackagesTag(t *testing.T) {
	root := planRepo(t, `{"ignore": ["app"], "tagTemplate": "v${version}", "forgeRelease": {"mode": "combined", "tag": "v${version}"}}`)
	for _, args := range [][]string{{"--dry-run"}, nil} {
		res, stderr := runTagJSON(t, args...)
		action := "created"
		if len(args) > 0 {
			action = "would-create"
		}
		if len(res.Tags) != 1 {
			t.Fatalf("%v: %d entries, want 1: %+v", args, len(res.Tags), res.Tags)
		}
		tg := res.Tags[0]
		if got := tagsByName(res)["v1.0.0"]; got != action+" done@1.0.0 lib@1.0.0" || !tg.Combined || tg.ReleaseOnly {
			t.Errorf("%v: entry = %q combined=%v releaseOnly=%v", args, got, tg.Combined, tg.ReleaseOnly)
		}
		if n := strings.Count(stderr, "v1.0.0"); n != 1 || !strings.Contains(stderr, "1 tag(s)") {
			t.Errorf("%v: text report shows v1.0.0 %d times:\n%s", args, n, stderr)
		}
	}
	if !hasTag(t, root, "v1.0.0") {
		t.Error("v1.0.0 was not created")
	}
}

// A failure still prints the envelope on stdout, the failure in its message,
// as forge-release --json does, and exits non-zero.
func TestTagJSONFailurePrintsTheEnvelope(t *testing.T) {
	// A combined release needs one version; app is on 2.0.0, lib on 1.0.0.
	// lib's tag is already there, and the refusal comes before any tag is
	// recorded: the envelope still lists it.
	root := planRepo(t, `{"forgeRelease": {"mode": "combined"}}`)
	tagRepo(t, root, "lib@1.0.0")
	cmd := newTagCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--json", "--dry-run"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("tag succeeded:\n%s", stdout.String())
	}
	var res tagOutput
	if jerr := json.Unmarshal(stdout.Bytes(), &res); jerr != nil {
		t.Fatalf("stdout is not the tag JSON: %v\n%s", jerr, stdout.String())
	}
	if res.Message != err.Error() {
		t.Errorf("envelope = %+v, want message %q", res, err)
	}
	if got := tagsByName(res); !reflect.DeepEqual(got, map[string]string{"lib@1.0.0": "exists lib@1.0.0"}) {
		t.Errorf("tags = %v, want the present lib@1.0.0", got)
	}
}

// An event that can't be written takes its tag back off, and create says the
// tag is gone, so `tag --json` doesn't list it as made. (The other half, a
// tag kept because its events landed and only the close failed, has no clean
// way to fail an *os.File's Close and is not tested.)
func TestTagEventsWriteFailureReportsTheTagGone(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a read-only file")
	}
	root := planRepo(t, `{}`)
	path := filepath.Join(t.TempDir(), "events.ndjson")
	if err := os.WriteFile(path, nil, 0o444); err != nil {
		t.Fatal(err)
	}
	created, err := (&tagEvents{path: path}).create(context.Background(), root, "v1.0.0", []string{"lib", "done"})
	if err == nil || created {
		t.Errorf("create = %v, %v; want an error and the tag reported gone", created, err)
	}
	if hasTag(t, root, "v1.0.0") {
		t.Error("v1.0.0 is still there")
	}
}

// laterCommit tags HEAD with tags, then commits again, so the tags sit on a
// commit that is no longer the release commit.
func laterCommit(t *testing.T, root string, tags ...string) {
	t.Helper()
	tagRepo(t, root, tags...)
	if out, err := exec.Command("git", "-C", root, "-c", "user.email=x@y.z", "-c", "user.name=x",
		"-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "later").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

// A combined tag that is also a package's tag, already there on an earlier
// commit: refused, the envelope listing the tag as present, never reported
// as this release's.
func TestTagJSONRefusesACombinedPackageTagOnAnotherCommit(t *testing.T) {
	root := planRepo(t, `{"ignore": ["app"], "forgeRelease": {"mode": "combined", "tag": "lib@${version}"}}`)
	laterCommit(t, root, "lib@1.0.0")
	cmd := newTagCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--json"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "already exists at") {
		t.Fatalf("tag = %v, want the combined tag refused\n%s", err, stderr.String())
	}
	var res tagOutput
	if jerr := json.Unmarshal(stdout.Bytes(), &res); jerr != nil {
		t.Fatalf("stdout is not the tag JSON: %v\n%s", jerr, stdout.String())
	}
	for _, tg := range res.Tags {
		if tg.Combined {
			t.Errorf("%s reported as the combined release's", tg.Tag)
		}
	}
	if hasTag(t, root, "done@1.0.0") {
		t.Error("done@1.0.0 was created before the refusal")
	}
}

// markCombined checks the tag it marks: one listed as present but on another
// commit (made there after the preflight, say) is an error, and the entry is
// left unmarked. One at HEAD is marked.
func TestMarkCombinedChecksTheTagIsAtHead(t *testing.T) {
	root := planRepo(t, `{}`)
	laterCommit(t, root, "lib@1.0.0")
	tags := []tagJSON{{Tag: "lib@1.0.0", Action: "exists"}}
	found, err := markCombined(context.Background(), root, tags, "lib@1.0.0")
	if !found || err == nil || tags[0].Combined {
		t.Errorf("tag on an earlier commit: found=%v err=%v combined=%v; want found, an error, unmarked", found, err, tags[0].Combined)
	}
	tagRepo(t, root, "done@1.0.0")
	tags = []tagJSON{{Tag: "done@1.0.0", Action: "exists"}}
	if found, err := markCombined(context.Background(), root, tags, "done@1.0.0"); !found || err != nil || !tags[0].Combined {
		t.Errorf("tag at HEAD: found=%v err=%v combined=%v; want it marked", found, err, tags[0].Combined)
	}
}
