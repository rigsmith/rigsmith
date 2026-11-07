package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type edgeAdoptFixture struct {
	ws, source, upstream, fork, headRepo string
	base, cursor, head, imported, extra  string
	git                                  *gitServer
	api                                  *httptest.Server
	pr                                   stackDepsPR
	requests                             int
}

func newEdgeAdoptFixture(t *testing.T, conflict bool) *edgeAdoptFixture {
	t.Helper()
	f := &edgeAdoptFixture{ws: t.TempDir(), source: t.TempDir()}
	f.git = newGitServer(t, t.TempDir())
	f.upstream, f.fork, f.headRepo = f.git.bare(t, "acme/Lib"), f.git.bare(t, "you/Lib"), f.git.bare(t, "contributor/Lib")
	mustGitStack(t, f.source, "init", "-q", "-b", "main")
	mustGitStack(t, f.source, "config", "user.name", "test")
	mustGitStack(t, f.source, "config", "user.email", "test@localhost")
	f.write(t, f.source, "fix.txt", "original\n")
	f.commit(t, f.source, "base")
	f.base = strings.TrimSpace(mustGitStack(t, f.source, "rev-parse", "HEAD"))
	f.write(t, f.source, "cursor.txt", "cursor addition\n")
	if conflict {
		f.write(t, f.source, "fix.txt", "cursor edit\n")
	}
	f.commit(t, f.source, "cursor")
	f.cursor = strings.TrimSpace(mustGitStack(t, f.source, "rev-parse", "HEAD"))
	mustGitStack(t, f.source, "push", "-q", f.upstream, "HEAD:refs/heads/main")
	mustGitStack(t, f.source, "push", "-q", f.fork, "HEAD:refs/heads/stack/integration")
	mustGitStack(t, f.source, "checkout", "-q", "-b", "foreign-fix", f.base)
	f.write(t, f.source, "fix.txt", "foreign fix\n")
	f.commit(t, f.source, "foreign PR")
	f.head = strings.TrimSpace(mustGitStack(t, f.source, "rev-parse", "HEAD"))
	mustGitStack(t, f.source, "push", "-q", f.headRepo, "HEAD:refs/heads/foreign-fix")
	f.write(t, f.source, "unrelated.txt", "must not fetch this branch or tag\n")
	f.commit(t, f.source, "unrelated branch")
	f.extra = strings.TrimSpace(mustGitStack(t, f.source, "rev-parse", "HEAD"))
	mustGitStack(t, f.source, "push", "-q", f.headRepo, "HEAD:refs/heads/unrelated", "HEAD:refs/tags/unrelated")

	mustGitStack(t, f.ws, "init", "-q", "-b", "main")
	mustGitStack(t, f.ws, "config", "user.name", "test")
	mustGitStack(t, f.ws, "config", "user.email", "test@localhost")
	// A real cursor is unfiltered upstream history, separate from the fused
	// import commit. Adoption must merge against this, not the integration tree.
	mustGitStack(t, f.ws, "fetch", "-q", "--no-write-fetch-head", f.upstream, f.cursor)
	f.write(t, f.ws, "lib/fix.txt", "original\n")
	if conflict {
		f.write(t, f.ws, "lib/fix.txt", "cursor edit\n")
	}
	f.write(t, f.ws, "lib/cursor.txt", "cursor addition\n")
	f.write(t, f.ws, "app/keep.txt", "sibling at import\n")
	f.write(t, f.ws, "README.md", "root at import\n")
	writeStackManifest(t, f.ws, fmt.Sprintf(`{
		"repos":{"lib":{"upstream":%q,"fork":%q,"trackBranch":"stack/integration","publishPrefix":"You."},
		"app":{"upstream":%q,"fork":%q}},
		"edge":{"lib":{"feed":"https://feed.invalid/index.json"}},
		"lastSync":{"lib":%q}
	}`, f.git.spec("acme/Lib"), f.git.spec("you/Lib"), f.git.spec("acme/App"), f.git.spec("you/App"), f.cursor))
	f.commit(t, f.ws, "stack: import lib @ "+short(f.cursor))
	f.imported = strings.TrimSpace(mustGitStack(t, f.ws, "rev-parse", "HEAD"))
	f.write(t, f.ws, "lib/integration-only.txt", "another carried topic\n")
	f.write(t, f.ws, "app/keep.txt", "sibling on integration line\n")
	f.write(t, f.ws, "README.md", "root on integration line\n")
	f.commit(t, f.ws, "other integration content")
	f.pr.Number, f.pr.State, f.pr.Head.SHA, f.pr.Head.Repo.FullName = 7, "open", f.head, "contributor/Lib"
	f.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests++
		if r.Method != "GET" || r.URL.Path != "/repos/acme/Lib/pulls/7" {
			t.Errorf("unexpected API request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(f.pr)
	}))
	t.Cleanup(f.api.Close)
	t.Setenv("GITHUB_API_URL", f.api.URL)
	t.Setenv("GITHUB_TOKEN", "read-only-test-token")
	chdir(t, f.ws)
	return f
}

func (f *edgeAdoptFixture) write(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *edgeAdoptFixture) commit(t *testing.T, root, message string) {
	t.Helper()
	mustGitStack(t, root, "add", "-A")
	mustGitStack(t, root, "commit", "-qm", message)
}

func (f *edgeAdoptFixture) url() string { return "https://" + f.git.spec("acme/Lib") + "/pull/7" }

func (f *edgeAdoptFixture) command(t *testing.T, args ...string) (string, error) {
	t.Helper()
	c := newStackEdgeAdoptCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	err := c.ExecuteContext(t.Context())
	return out.String(), err
}

func (f *edgeAdoptFixture) snapshot(t *testing.T) map[string]string {
	t.Helper()
	s := map[string]string{}
	for _, path := range []string{f.upstream, f.fork, f.headRepo} {
		s[path] = mustGitStack(t, path, "for-each-ref")
	}
	for _, args := range [][]string{{"rev-parse", "HEAD"}, {"write-tree"}, {"status", "--porcelain", "-z"}} {
		s[strings.Join(args, " ")] = mustGitStack(t, f.ws, args...)
	}
	b, err := os.ReadFile(filepath.Join(f.ws, "rig.stack.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	s["manifest"] = string(b)
	return s
}

func TestStackEdgeAdoptTopic(t *testing.T) {
	f := newEdgeAdoptFixture(t, false)
	// Even dirty/staged changes must survive unchanged, without leaking in.
	f.write(t, f.ws, "lib/dirty.txt", "not adopted\n")
	mustGitStack(t, f.ws, "add", "lib/dirty.txt")
	before := f.snapshot(t)
	refs := mustGitStack(t, f.ws, "for-each-ref")
	out, err := f.command(t, "lib", f.url())
	if err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}
	topic := "refs/heads/stack-pr-adopt-7"
	if got := strings.TrimSpace(mustGitStack(t, f.ws, "rev-parse", topic+"^@")); got != f.imported {
		t.Fatalf("topic parents %q, want only import %s", got, f.imported)
	}
	if got := strings.TrimSpace(mustGitStack(t, f.ws, "diff-tree", "--no-commit-id", "--name-only", "-r", f.imported, topic)); got != "lib/fix.txt" {
		t.Fatalf("topic changed more than PR content: %s", got)
	}
	if got := mustGitStack(t, f.ws, "show", topic+":lib/fix.txt"); got != "foreign fix\n" {
		t.Fatalf("topic lost PR change: %q", got)
	}
	if got := mustGitStack(t, f.ws, "show", topic+":lib/cursor.txt"); got != "cursor addition\n" {
		t.Fatalf("topic lost cursor change: %q", got)
	}
	if got := f.snapshot(t); !reflect.DeepEqual(got, before) {
		t.Fatalf("adoption changed checkout, index, manifest or remotes: before=%v after=%v", before, got)
	}
	for _, want := range []string{`{ "pr": "` + f.url() + `", "commit": "` + f.head + `" }`, "merge stack-pr-adopt-7 into the integration line", "edge.lib.adopted", "rig stack propose lib --track-only"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing next step %q: %s", want, out)
		}
	}
	if got := strings.ReplaceAll(mustGitStack(t, f.ws, "for-each-ref"), mustGitStack(t, f.ws, "for-each-ref", topic), ""); got != refs {
		t.Fatalf("adopt wrote another ref: %s", got)
	}
	if _, err := stackCIGit(t.Context(), f.ws, "cat-file", "-e", f.extra); err == nil {
		t.Fatal("adopt fetched an unrelated branch/tag")
	}
	if _, err := os.Stat(filepath.Join(f.ws, ".git", "FETCH_HEAD")); !os.IsNotExist(err) {
		t.Fatalf("adopt wrote FETCH_HEAD: %v", err)
	}
}

func TestStackEdgeAdoptConflict(t *testing.T) {
	f := newEdgeAdoptFixture(t, true)
	before, refs := f.snapshot(t), mustGitStack(t, f.ws, "for-each-ref")
	out, err := f.command(t, "lib", f.url())
	if err == nil || !strings.Contains(err.Error(), f.url()) || !strings.Contains(err.Error(), "needs rebasing onto the cursor") || !strings.Contains(err.Error(), "by hand as a topic") {
		t.Fatalf("expected PR-specific conflict refusal, got %v\n%s", err, out)
	}
	if !reflect.DeepEqual(f.snapshot(t), before) || mustGitStack(t, f.ws, "for-each-ref") != refs {
		t.Fatal("conflicting adoption created a topic or changed checkout/manifest/remotes")
	}
}

func TestStackEdgeAdoptExisting(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(fmt.Sprintf("same-%t", same), func(t *testing.T) {
			f := newEdgeAdoptFixture(t, false)
			if same {
				if out, err := f.command(t, "lib", f.url()); err != nil {
					t.Fatalf("first adopt: %v\n%s", err, out)
				}
			} else {
				mustGitStack(t, f.ws, "branch", "stack-pr-adopt-7", f.imported)
			}
			before, refs := f.snapshot(t), mustGitStack(t, f.ws, "for-each-ref")
			out, err := f.command(t, "lib", f.url())
			if same && (err != nil || !strings.Contains(out, "same content; nothing to do")) {
				t.Fatalf("same content should be no-op: %v\n%s", err, out)
			}
			if !same && (err == nil || !strings.Contains(err.Error(), "different content")) {
				t.Fatalf("different topic should refuse: %v\n%s", err, out)
			}
			if !reflect.DeepEqual(f.snapshot(t), before) || mustGitStack(t, f.ws, "for-each-ref") != refs {
				t.Fatal("existing topic was overwritten or state changed")
			}
		})
	}
}

func TestStackEdgeAdoptDryRun(t *testing.T) {
	f := newEdgeAdoptFixture(t, false)
	before, refs := f.snapshot(t), mustGitStack(t, f.ws, "for-each-ref")
	out, err := f.command(t, "lib", f.url(), "--dry-run")
	if err != nil || !strings.Contains(out, "Would create stack-pr-adopt-7") || !strings.Contains(out, f.head) {
		t.Fatalf("dry-run: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(f.snapshot(t), before) || mustGitStack(t, f.ws, "for-each-ref") != refs {
		t.Fatal("dry-run wrote state")
	}
}

func TestStackEdgeAdoptWrongRepo(t *testing.T) {
	f := newEdgeAdoptFixture(t, false)
	before, refs := f.snapshot(t), mustGitStack(t, f.ws, "for-each-ref")
	_, err := f.command(t, "lib", "https://"+f.git.spec("other/Lib")+"/pull/7")
	if err == nil || !strings.Contains(err.Error(), "PR must be on the member's upstream") || f.requests != 0 {
		t.Fatalf("wrong repo not refused before API access: %v; requests=%d", err, f.requests)
	}
	if !reflect.DeepEqual(f.snapshot(t), before) || mustGitStack(t, f.ws, "for-each-ref") != refs {
		t.Fatal("wrong-repo adoption wrote state")
	}
}

func TestStackEdgeAdoptCompletion(t *testing.T) {
	f := newEdgeAdoptFixture(t, false)
	c := newStackEdgeAdoptCmd()
	c.SetContext(t.Context())
	got, directive := c.ValidArgsFunction(c, nil, "")
	if !reflect.DeepEqual(got, []string{"app", "lib"}) || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("member completion: %v, %v", got, directive)
	}
	if got, directive := c.ValidArgsFunction(c, []string{"lib"}, ""); len(got) != 0 || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("PR URL completion: %v, %v", got, directive)
	}
	if f.requests != 0 {
		t.Fatal("completion contacted the API")
	}
}

func TestStackEdgeAdoptedMoved(t *testing.T) {
	pinned, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("head check attempted a write: %s", r.Method)
		}
		switch r.URL.Path {
		case "/repos/acme/Lib/pulls/1":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/repos/acme/Lib/pulls/2":
			fmt.Fprintf(w, `{"head":{"sha":%q}}`, head)
		case "/repos/acme/Lib/pulls/3":
			fmt.Fprintf(w, `{"head":{"sha":%q}}`, strings.ToUpper(pinned))
		default:
			t.Errorf("unexpected request: %s", r.URL)
		}
	}))
	defer api.Close()
	member := &stackRepo{Upstream: "github.com/acme/Lib"}
	adopted := []stackEdgeAdopted{
		{PR: "https://github.com/acme/Lib/pull/1", Commit: pinned},
		{PR: "https://github.com/acme/Lib/pull/2", Commit: pinned},
		{PR: "https://github.com/acme/Lib/pull/3", Commit: pinned},
	}
	moved, err := stackEdgeAdoptedMoved(t.Context(), stackDepsAPI{base: api.URL, client: api.Client()}, member, adopted)
	if err == nil || !strings.Contains(err.Error(), "503") || len(moved) != 1 || moved[0].PR != adopted[1].PR || moved[0].Pinned != pinned || moved[0].Head != head || moved[0].Warning == "" {
		t.Fatalf("unavailable PR hid later movement, or matching head warned: %+v, %v", moved, err)
	}
}
