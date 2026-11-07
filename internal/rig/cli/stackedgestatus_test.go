package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// This package's existing hermeticgit_test.go isolates every git subprocess.
// The bare remotes and fake API/feed follow the released and track-only tests.
type edgeStatusFixture struct {
	ws, source, upstream, fork, base, fix, track, release string
	git                                                   *gitServer
	api                                                   *httptest.Server
	merged, inCursor, published, private                  bool
	adopted, missingMetadata, compareError, extraPR       bool
	adoptedHead                                           string
}

func newEdgeStatusFixture(t *testing.T) *edgeStatusFixture {
	t.Helper()
	f := &edgeStatusFixture{ws: t.TempDir(), source: t.TempDir()}
	f.git = newGitServer(t, t.TempDir())
	f.upstream = f.git.bare(t, "acme/lib")
	f.fork = f.git.bare(t, "you/lib")
	mustGitStack(t, f.source, "init", "-q", "-b", "main")
	mustGitStack(t, f.source, "config", "user.email", "t@t")
	mustGitStack(t, f.source, "config", "user.name", "t")
	f.write(t, f.source, "Lib.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><PackageId>Lib</PackageId><Version>1.0.0</Version></PropertyGroup></Project>`)
	f.write(t, f.source, "fix.txt", "original\n")
	mustGitStack(t, f.source, "add", "-A")
	mustGitStack(t, f.source, "commit", "-qm", "base")
	f.base = strings.TrimSpace(mustGitStack(t, f.source, "rev-parse", "HEAD"))
	mustGitStack(t, f.source, "push", "-q", f.upstream, "main")
	f.write(t, f.source, "fix.txt", "carried fix\n")
	mustGitStack(t, f.source, "add", "-A")
	mustGitStack(t, f.source, "commit", "-qm", "fix")
	f.fix = strings.TrimSpace(mustGitStack(t, f.source, "rev-parse", "HEAD"))
	mustGitStack(t, f.source, "push", "-q", f.upstream, "HEAD:refs/pull/7/head")
	mustGitStack(t, f.source, "push", "-q", f.upstream, "HEAD:refs/pull/8/head")
	mustGitStack(t, f.source, "push", "-q", f.fork, "HEAD:refs/heads/stack/fix")
	f.track = strings.TrimSpace(mustGitStack(t, f.source, "commit-tree", "HEAD^{tree}", "-p", f.base, "-m", "integration"))
	mustGitStack(t, f.source, "push", "-q", f.fork, f.track+":refs/heads/stack/integration")
	// Release has unrelated upstream content too: equality with track's tree
	// would reject this valid retirement; a three-way merge preserves it.
	f.write(t, f.source, "upstream.txt", "upstream addition\n")
	mustGitStack(t, f.source, "add", "-A")
	mustGitStack(t, f.source, "commit", "-qm", "release")
	f.release = strings.TrimSpace(mustGitStack(t, f.source, "rev-parse", "HEAD"))
	mustGitStack(t, f.source, "push", "-q", f.upstream, "main")
	mustGitStack(t, f.ws, "init", "-q", "-b", "main")
	mustGitStack(t, f.ws, "config", "user.email", "t@t")
	mustGitStack(t, f.ws, "config", "user.name", "t")
	f.write(t, f.ws, "lib/Lib.csproj", string(f.read(t, f.source, "Lib.csproj")))
	f.write(t, f.ws, "lib/fix.txt", "carried fix\n")
	mustGitStack(t, f.ws, "fetch", "-q", f.fork, f.track)
	mustGitStack(t, f.ws, "update-ref", "refs/rigsmith/integration/lib", f.track)
	f.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("status attempted a write: %s %s", r.Method, r.URL)
			w.WriteHeader(405)
			return
		}
		switch {
		case r.URL.Path == "/repos/acme/lib/pulls":
			if f.adopted {
				t.Error("adopted PR looked up by branch instead of number")
			}
			if strings.HasSuffix(r.URL.Query().Get("head"), ":stack/open") {
				fmt.Fprint(w, `[{"number":8,"head":{"ref":"stack/open","repo":{"full_name":"you/lib"}}}]`)
			} else {
				fmt.Fprint(w, `[{"number":7,"head":{"ref":"stack/fix","repo":{"full_name":"you/lib"}}}]`)
			}
		case r.URL.Path == "/repos/acme/lib/pulls/7":
			merged := "null"
			if f.merged {
				merged = `"today"`
			}
			head := f.fix
			if f.adoptedHead != "" {
				head = f.adoptedHead
			}
			fmt.Fprintf(w, `{"number":7,"merged_at":%s,"merge_commit_sha":%q,"head":{"sha":%q}}`, merged, f.fix, head)
		case r.URL.Path == "/repos/acme/lib/pulls/8":
			fmt.Fprintf(w, `{"number":8,"merged_at":null,"head":{"sha":%q}}`, f.fix)
		case strings.Contains(r.URL.Path, "/compare/"):
			if f.compareError {
				w.WriteHeader(503)
				return
			}
			parts := strings.Split(strings.Split(r.URL.Path, "/compare/")[1], "...")
			status := "diverged"
			if len(parts) == 2 {
				p := exec.Command("git", "merge-base", "--is-ancestor", parts[0], parts[1])
				p.Dir = f.source
				if p.Run() == nil {
					status = "ahead"
					if parts[0] == parts[1] {
						status = "identical"
					}
				}
			}
			fmt.Fprintf(w, `{"status":%q}`, status)
		case r.URL.Path == "/public/lib/index.json":
			f.assertAnonymous(t, r)
			if f.published {
				fmt.Fprint(w, `{"versions":["2.0.0"]}`)
			} else {
				fmt.Fprint(w, `{"versions":[]}`)
			}
		case r.URL.Path == "/public/lib/2.0.0/lib.nuspec":
			f.assertAnonymous(t, r)
			commit := f.release
			if f.missingMetadata {
				commit = ""
			}
			fmt.Fprintf(w, `<package><metadata><repository url="https://%s" commit="%s" /></metadata></package>`, f.git.spec("acme/lib"), commit)
		case r.URL.Path == "/service/index.json":
			f.assertAnonymous(t, r)
			if f.private {
				w.WriteHeader(401)
				return
			}
			fmt.Fprintf(w, `{"resources":[{"@id":%q,"@type":"PackageBaseAddress/3.0.0"}]}`, f.api.URL+"/edge/")
		case r.URL.Path == "/edge/you.lib/index.json":
			f.assertAnonymous(t, r)
			fmt.Fprint(w, `{"versions":["2.0.1-edge.9","2.0.1-edge.10","2.0.1-edge.2","9.0.0-preview.1"]}`)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.api.Close)
	t.Setenv("GITHUB_API_URL", f.api.URL)
	t.Setenv("GITHUB_TOKEN", "github-read-token")
	t.Setenv("EDGE_FEED_KEY", "push-key-must-not-reach-status")
	chdir(t, f.ws)
	return f
}

func (f *edgeStatusFixture) assertAnonymous(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Header.Get("Authorization") != "" || r.Header.Get("X-NuGet-ApiKey") != "" {
		t.Error("feed received a credential")
	}
}

func (f *edgeStatusFixture) write(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *edgeStatusFixture) read(t *testing.T, root, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *edgeStatusFixture) manifest(t *testing.T) {
	t.Helper()
	cursor := f.base
	if f.inCursor {
		cursor = f.fix
	}
	proposals, adopted := `{"lib":{"fix":{"branch":"stack/fix"}}}`, "[]"
	if f.extraPR {
		proposals = `{"lib":{"fix":{"branch":"stack/fix"},"open":{"branch":"stack/open"}}}`
	}
	if f.adopted {
		proposals = "{}"
		adopted = fmt.Sprintf(`[{"pr":%q,"commit":%q}]`, f.git.spec("acme/lib")+"/pull/7", f.fix)
	}
	writeStackManifest(t, f.ws, fmt.Sprintf(`{
		"repos":{"lib":{"upstream":%q,"fork":%q,"trackBranch":"stack/integration","publishPrefix":"You."}},
		"edge":{"lib":{"feed":%q,"pushKeySecret":"EDGE_FEED_KEY","adopted":%s}},
		"lastSync":{"lib":%q},"proposals":%s
	}`, f.git.spec("acme/lib"), f.git.spec("you/lib"), f.api.URL+"/service/index.json", adopted, cursor, proposals))
	mustGitStack(t, f.ws, "add", "-A")
	mustGitStack(t, f.ws, "commit", "-qm", "stackspace")
}

func (f *edgeStatusFixture) command(t *testing.T, args ...string) (string, error) {
	t.Helper()
	c := newStackEdgeStatusCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(append([]string{"--feed", f.api.URL + "/public"}, args...))
	err := c.ExecuteContext(context.Background())
	return out.String(), err
}

func (f *edgeStatusFixture) report(t *testing.T, args ...string) stackEdgeStatusReport {
	t.Helper()
	out, err := f.command(t, append([]string{"--json"}, args...)...)
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	var reports []stackEdgeStatusReport
	if err := json.Unmarshal([]byte(out), &reports); err != nil || len(reports) != 1 {
		t.Fatalf("bad JSON report: %v\n%s", err, out)
	}
	return reports[0]
}

func TestStackEdgeStatusItemStates(t *testing.T) {
	for _, tc := range []struct {
		name, want                string
		merged, cursor, published bool
	}{
		{"open", "open", false, false, false},
		{"merged", "merged", true, false, false},
		{"cursor", "merged and in cursor", true, true, false},
		{"released", "released in V", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEdgeStatusFixture(t)
			f.merged, f.inCursor, f.published = tc.merged, tc.cursor, tc.published
			f.manifest(t)
			r := f.report(t, "lib")
			if len(r.Items) != 1 || r.Items[0].Kind != "proposal" || r.Items[0].Name != "fix" || r.Items[0].Status != tc.want {
				t.Fatalf("item state: %+v", r)
			}
			if r.NewestEdge != "2.0.1-edge.10" || r.Package != "Lib" {
				t.Fatalf("feed/package: %+v", r)
			}
			if tc.published && (r.Items[0].Version != "2.0.0" || r.Items[0].ReleaseCommit != f.release || r.Items[0].MergeCommit != f.fix) {
				t.Fatalf("missing release evidence: %+v", r.Items[0])
			}
			out, err := f.command(t, "lib")
			want := tc.want
			if tc.published {
				want = "released in 2.0.0"
			}
			if err != nil || !strings.Contains(out, want) || strings.Contains(out, "released in V") {
				t.Fatalf("text output: %v\n%s", err, out)
			}
		})
	}
}

func TestStackEdgeStatusAdopted(t *testing.T) {
	f := newEdgeStatusFixture(t)
	f.adopted, f.merged, f.published = true, true, true
	f.manifest(t)
	r := f.report(t)
	if len(r.Items) != 1 || r.Items[0].Kind != "adopted" || r.Items[0].Status != "released in V" || !r.Retirable {
		t.Fatalf("adopted PR not proven by number: %+v", r)
	}
}

func TestStackEdgeStatusAdoptedMoved(t *testing.T) {
	for _, released := range []bool{false, true} {
		t.Run(fmt.Sprintf("released-%t", released), func(t *testing.T) {
			f := newEdgeStatusFixture(t)
			f.adopted, f.merged, f.published = true, released, released
			f.manifest(t)
			before := f.report(t)
			if before.Items[0].Warning != "" {
				t.Fatalf("matching head warned: %+v", before.Items[0])
			}
			f.adoptedHead = f.release
			after := f.report(t)
			warning := after.Items[0].Warning
			for _, want := range []string{"has moved from " + f.fix + " to " + f.release, "re-run `rig stack edge adopt`", "then update the pin"} {
				if !strings.Contains(warning, want) {
					t.Fatalf("missing warning %q: %+v", want, after.Items[0])
				}
			}
			after.Items[0].Warning = ""
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("head movement changed pinned-content/release/retirement verdict: before=%+v after=%+v", before, after)
			}
			out, err := f.command(t)
			if err != nil || !strings.Contains(out, "warning: "+warning) {
				t.Fatalf("text warning: %v\n%s", err, out)
			}
		})
	}
}

func TestStackEdgeStatusMixedItems(t *testing.T) {
	f := newEdgeStatusFixture(t)
	f.merged, f.published, f.extraPR = true, true, true
	f.manifest(t)
	r := f.report(t)
	if len(r.Items) != 2 || r.Items[0].Status != "released in V" || r.Items[1].Status != "open" || r.Retirable {
		t.Fatalf("one open item hid another item's evidence or allowed retirement: %+v", r)
	}
	// An adopted open PR blocks retirement alongside a released proposal too.
	m, _, err := loadStackManifest(f.ws)
	if err != nil {
		t.Fatal(err)
	}
	delete(m.Proposals["lib"], "open")
	m.Edge["lib"].Adopted = []stackEdgeAdopted{{PR: f.git.spec("acme/lib") + "/pull/8", Commit: f.fix}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeStackManifest(t, f.ws, string(b))
	r = f.report(t)
	if len(r.Items) != 2 || r.Items[0].Status != "released in V" || r.Items[1].Kind != "adopted" || r.Items[1].Status != "open" || r.Retirable {
		t.Fatalf("adopted item was omitted from retirement: %+v", r)
	}
}

func TestStackEdgeStatusUnrecordedOnly(t *testing.T) {
	f := newEdgeStatusFixture(t)
	f.published = true
	f.manifest(t)
	m, _, err := loadStackManifest(f.ws)
	if err != nil {
		t.Fatal(err)
	}
	m.Proposals = nil
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeStackManifest(t, f.ws, string(b))
	r := f.report(t)
	if len(r.Items) != 1 || r.Items[0].Kind != "unproposed content" || r.Items[0].Status != "carried" || !r.Retirable {
		t.Fatalf("unrecorded content already shipped upstream was not recognized: %+v", r)
	}
}

func TestStackEdgeStatusMissingPackage(t *testing.T) {
	f := newEdgeStatusFixture(t)
	f.merged, f.published = true, true
	f.manifest(t)
	if err := os.Remove(filepath.Join(f.ws, "lib", "Lib.csproj")); err != nil {
		t.Fatal(err)
	}
	r := f.report(t)
	if r.Retirable || r.Items[0].Status != "can't check" || !strings.Contains(r.Retirement, "--package") {
		t.Fatalf("missing discovery guessed a release: %+v", r)
	}
	r = f.report(t, "--package", "Lib")
	if !r.Retirable || r.Items[0].Status != "released in V" {
		t.Fatalf("explicit evidence package did not work: %+v", r)
	}
}

func TestStackEdgeStatusRetirement(t *testing.T) {
	for _, tc := range []string{"contained", "unreleased", "unproposed", "reworked", "missing-metadata", "compare-unavailable"} {
		t.Run(tc, func(t *testing.T) {
			f := newEdgeStatusFixture(t)
			f.merged, f.published = true, true
			switch tc {
			case "unreleased":
				f.published = false
			case "unproposed":
				f.write(t, f.source, "private-fix.txt", "not proposed\n")
				mustGitStack(t, f.source, "add", "private-fix.txt")
				mustGitStack(t, f.source, "commit", "-qm", "unproposed")
				// Preserve the original upstream base and add content to track.
				f.track = strings.TrimSpace(mustGitStack(t, f.source, "commit-tree", "HEAD^{tree}", "-p", f.base, "-m", "integration with extra content"))
				mustGitStack(t, f.source, "push", "-q", "--force", f.fork, f.track+":refs/heads/stack/integration")
			case "reworked":
				f.write(t, f.source, "fix.txt", "upstream reworked the fix\n")
				mustGitStack(t, f.source, "add", "-A")
				mustGitStack(t, f.source, "commit", "-qm", "rework merged fix")
				f.release = strings.TrimSpace(mustGitStack(t, f.source, "rev-parse", "HEAD"))
				mustGitStack(t, f.source, "push", "-q", f.upstream, "main")
			case "missing-metadata":
				f.missingMetadata = true
			case "compare-unavailable":
				f.compareError = true
			}
			f.manifest(t)
			r := f.report(t, "lib")
			if r.Retirable != (tc == "contained") {
				t.Fatalf("unsafe retirement verdict: %+v", r)
			}
			if tc == "contained" && (r.Version != "2.0.0" || !strings.Contains(r.Retirement, "rig stack released")) {
				t.Fatalf("missing migration advice: %+v", r)
			}
			if tc == "unproposed" {
				if len(r.Items) != 2 || r.Items[1].Kind != "unproposed content" || r.Items[1].Status != "carried" {
					t.Fatalf("silent unproposed content: %+v", r)
				}
			}
			if tc == "unproposed" || tc == "reworked" {
				if r.Items[0].Status != "released in V" || !strings.Contains(r.Retirement, "not contained") {
					t.Fatalf("release ancestry substituted for containment: %+v", r)
				}
			}
		})
	}
}

func TestStackEdgeStatusFreshness(t *testing.T) {
	for _, tc := range []string{"fresh", "local-stale", "missing-receipt", "upstream-stale", "upstream-metadata-only", "remote-moved"} {
		t.Run(tc, func(t *testing.T) {
			f := newEdgeStatusFixture(t)
			f.manifest(t)
			// Start with track's parent equal to the remote upstream tip.
			mustGitStack(t, f.source, "push", "-q", "--force", f.upstream, f.base+":refs/heads/main")
			switch tc {
			case "local-stale":
				f.write(t, f.ws, "lib/new.txt", "new local integration content\n")
				mustGitStack(t, f.ws, "add", "-A")
				mustGitStack(t, f.ws, "commit", "-qm", "new carried fix")
			case "missing-receipt":
				mustGitStack(t, f.ws, "update-ref", "-d", "refs/rigsmith/integration/lib")
			case "upstream-stale":
				mustGitStack(t, f.source, "push", "-q", f.upstream, "main")
			case "upstream-metadata-only":
				tip := strings.TrimSpace(mustGitStack(t, f.source, "commit-tree", f.base+"^{tree}", "-p", f.base, "-m", "same tree, new upstream commit"))
				mustGitStack(t, f.source, "push", "-q", f.upstream, tip+":refs/heads/main")
			case "remote-moved":
				track := strings.TrimSpace(mustGitStack(t, f.source, "commit-tree", f.fix+"^{tree}", "-p", f.release, "-m", "remote integration moved"))
				mustGitStack(t, f.source, "push", "-q", "--force", f.fork, track+":refs/heads/stack/integration")
			}
			r := f.report(t)
			localFresh := tc != "local-stale" && tc != "missing-receipt"
			upstreamFresh := tc == "fresh" || tc == "local-stale" || tc == "missing-receipt"
			if (r.Local == "fresh") != localFresh || (r.Upstream == "fresh") != upstreamFresh {
				t.Fatalf("wrong freshness: %+v", r)
			}
			if !localFresh && !strings.Contains(r.Local, "rig stack propose lib --track-only") {
				t.Fatalf("missing local remedy: %+v", r)
			}
			if !upstreamFresh && !strings.Contains(r.Upstream, "stale") {
				t.Fatalf("missing upstream staleness: %+v", r)
			}
		})
	}
}

func TestStackEdgeStatusPrivateFeedReadOnly(t *testing.T) {
	f := newEdgeStatusFixture(t)
	f.merged, f.published, f.private = true, true, true
	f.manifest(t)
	f.write(t, f.ws, "lib/uncommitted.txt", "leave dirty files alone\n")
	snapshot := func() []string {
		return []string{
			mustGitStack(t, f.ws, "for-each-ref"),
			mustGitStack(t, f.ws, "status", "--porcelain=v1", "--untracked-files=all"),
			mustGitStack(t, f.fork, "for-each-ref"),
			mustGitStack(t, f.upstream, "for-each-ref"),
			string(f.read(t, f.ws, "rig.stack.jsonc")),
			string(f.read(t, f.ws, ".git/index")),
			string(f.read(t, f.ws, ".git/FETCH_HEAD")),
		}
	}
	before := snapshot()
	r := f.report(t)
	if !strings.Contains(r.NewestEdge, "can't check") || !r.Retirable {
		t.Fatalf("private feed blocked public retirement evidence: %+v", r)
	}
	if got := snapshot(); !reflect.DeepEqual(before, got) {
		t.Fatalf("status wrote to the stackspace or remotes:\nbefore=%q\nafter=%q", before, got)
	}
}

func TestStackEdgeStatusNewestVersion(t *testing.T) {
	for _, tc := range []struct {
		name, versions, label, want string
	}{
		{"numeric", `"1.0.1-edge.9","1.0.1-edge.10","1.0.1-edge.2"`, "", "1.0.1-edge.10"},
		{"new-base", `"1.0.1-edge.999","2.0.0-edge.1","3.0.0-other.1"`, "edge", "2.0.0-edge.1"},
		{"prerelease-tree", `"2.0.0-rc.1.edge.7","2.0.0-rc.1.edge.9"`, "edge", "2.0.0-rc.1.edge.9"},
		{"repeated-label", `"2.0.0-edge.1.edge.7","2.0.0-edge.1.edge.9"`, "edge", "2.0.0-edge.1.edge.9"},
		{"custom-label", `"2.0.0-preview.fix.2","2.0.0-preview.fix.10","3.0.0-edge.1"`, "preview.fix", "2.0.0-preview.fix.10"},
		{"case", `"2.0.0-EDGE.1","2.0.0-edge.2"`, "EDGE", "2.0.0-edge.2"},
		{"boundary", `"3.0.0-notedge.9","2.0.0-edge.1"`, "edge", "2.0.0-edge.1"},
		{"none", `"2.0.0","2.0.1-preview.1"`, "edge", "none published"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/you.lib/index.json" {
					t.Errorf("wrong package identity: %s", r.URL.Path)
				}
				fmt.Fprintf(w, `{"versions":[%s]}`, tc.versions)
			}))
			defer srv.Close()
			got, err := stackEdgeNewestVersion(context.Background(), srv.Client(), srv.URL, "You.Lib", tc.label)
			if err != nil || got != tc.want {
				t.Fatalf("newest=%q, err=%v; want %q", got, err, tc.want)
			}
		})
	}
}
