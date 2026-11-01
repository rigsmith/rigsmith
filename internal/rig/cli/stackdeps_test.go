package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStackDepsMergeTypes(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "acme/app")
	for _, kind := range []string{"merge", "squash", "rebase"} {
		t.Run(kind, func(t *testing.T) {
			// The API reports the upstream merge commit (rebase: last rebased commit),
			// deliberately different from the proposal's original commit.
			merge, old, history := stackDepsHistory(t, kind)
			head := strings.Repeat("c", 40)
			merged := false
			cursor := old
			var posted []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fake" {
					t.Error("missing token")
				}
				if r.Method == "POST" {
					if r.URL.Path != "/repos/acme/app/statuses/"+head {
						t.Error(r.URL.Path)
					}
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					posted = append(posted, body["state"])
					w.WriteHeader(201)
					return
				}
				if strings.Contains(r.URL.Path, "/compare/") {
					if !strings.Contains(r.URL.Path, merge+"...") {
						t.Error("compared original proposal instead of API merge commit")
					}
					status := "behind"
					parts := strings.Split(strings.Split(r.URL.Path, "/compare/")[1], "...")
					command := exec.Command("git", "merge-base", "--is-ancestor", parts[0], parts[1])
					command.Dir = history
					if command.Run() == nil {
						status = "identical"
					}
					fmt.Fprintf(w, `{"status":%q}`, status)
					return
				}
				pr := stackDepsPR{Number: 12, State: "open", MergeSHA: merge}
				pr.Head.SHA = head
				pr.Head.Ref = "stack/topic"
				pr.Head.Repo.FullName = "you/lib"
				if strings.Contains(r.URL.Path, "/app/") {
					pr.Head.Repo.FullName = "you/app"
				}
				if merged {
					stamp := "2026-11-01"
					pr.MergedAt = &stamp
					pr.State = "closed"
				}
				if strings.HasSuffix(r.URL.Path, "/pulls") {
					json.NewEncoder(w).Encode([]stackDepsPR{pr})
				} else {
					json.NewEncoder(w).Encode(pr)
				}
			}))
			defer server.Close()
			a := stackDepsAPI{server.URL, "fake", server.Client()}
			p := stackProposal{Branch: "stack/topic", Commit: old, DependsOn: []string{"lib"}}
			m := &stackManifest{Repos: map[string]*stackRepo{"app": {Upstream: "github.com/acme/app", Fork: "github.com/you/app"}, "lib": {Upstream: "github.com/acme/lib", Fork: "github.com/you/lib"}}, LastSync: map[string]string{"app": old, "lib": old}, Proposals: map[string]map[string]stackProposal{"app": {"topic": p}, "lib": {"topic": p}}}
			for i, want := range []string{"pending", "pending", "success"} {
				if i > 0 {
					merged = true
				}
				if i == 2 {
					cursor = merge
				}
				m.LastSync["lib"] = cursor
				m.LastSync["app"] = cursor
				var out bytes.Buffer
				stackDepsReport(t.Context(), m, a, &out, true)
				if posted[len(posted)-1] != want {
					t.Fatalf("phase %d: %v %s", i, posted, out.String())
				}
			}
		})
	}
}
func TestStackDepsUnavailableAndReadOnly(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer s.Close()
	a := stackDepsAPI{s.URL, "", s.Client()}
	r := &stackRepo{Upstream: "github.com/acme/lib", Fork: "github.com/you/lib"}
	text, ready := a.dependency(t.Context(), r, stackProposal{Branch: "stack/topic"}, strings.Repeat("a", 40))
	if ready || !strings.Contains(text, "could not check") {
		t.Fatal(text, ready)
	}
	m := &stackManifest{Repos: map[string]*stackRepo{"a": r, "b": r}, Proposals: map[string]map[string]stackProposal{"a": {"topic": {Branch: "stack/topic", DependsOn: []string{"a", "b"}}}, "b": {"topic": {Branch: "stack/topic", DependsOn: []string{"a", "b"}}}}}
	var out bytes.Buffer
	stackDepsReport(t.Context(), m, a, &out, true)
	if !strings.Contains(out.String(), "::warning::") {
		t.Fatal(out.String())
	}
}

func stackDepsHistory(t *testing.T, kind string) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "upstream.git")
	mustGitStack(t, root, "init", "--bare", bare)
	work := filepath.Join(root, "work")
	mustGitStack(t, root, "clone", bare, work)
	mustGitStack(t, work, "config", "user.name", "you")
	mustGitStack(t, work, "config", "user.email", "you@example.com")
	write := func(name, value string) {
		if err := os.WriteFile(filepath.Join(work, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		mustGitStack(t, work, "add", name)
		mustGitStack(t, work, "commit", "-m", value)
	}
	write("base", "base")
	old := strings.TrimSpace(mustGitStack(t, work, "rev-parse", "HEAD"))
	mustGitStack(t, work, "branch", "-M", "main")
	mustGitStack(t, work, "checkout", "-b", "topic")
	write("feature", "feature")
	mustGitStack(t, work, "checkout", "main")
	write("other", "other")
	switch kind {
	case "merge":
		mustGitStack(t, work, "merge", "--no-ff", "topic", "-m", "merge")
	case "squash":
		mustGitStack(t, work, "merge", "--squash", "topic")
		mustGitStack(t, work, "commit", "-m", "squash")
	case "rebase":
		mustGitStack(t, work, "checkout", "topic")
		mustGitStack(t, work, "rebase", "main")
		mustGitStack(t, work, "checkout", "main")
		mustGitStack(t, work, "merge", "--ff-only", "topic")
	}
	merge := strings.TrimSpace(mustGitStack(t, work, "rev-parse", "HEAD"))
	mustGitStack(t, work, "push", "origin", "main")
	return merge, old, bare
}

func TestStackDepsProposalDirection(t *testing.T) {
	root := t.TempDir()
	data := `{"repos":{"lib":{"upstream":"github.com/acme/lib","fork":"github.com/you/lib"},"app":{"upstream":"github.com/acme/app","fork":"github.com/you/app"}}}`
	if err := os.WriteFile(filepath.Join(root, "rig.stack.jsonc"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	m, src, err := loadStackManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lib", "app", "lib"} {
		if err := stackRememberProposal(src, m, name, "topic", "stack/topic", strings.Repeat("a", 40)); err != nil {
			t.Fatal(err)
		}
	}
	m, _, err = loadStackManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Proposals["lib"]["topic"].DependsOn) != 0 {
		t.Fatal("re-proposing introduced cycle")
	}
	deps := m.Proposals["app"]["topic"].DependsOn
	if len(deps) != 1 || deps[0] != "lib" {
		t.Fatal(deps)
	}
}

func TestStackDepsDeniedStatusWrite(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "acme/lib")
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			writes++
			w.WriteHeader(403)
			return
		}
		if strings.Contains(r.URL.Path, "/compare/") {
			fmt.Fprint(w, `{"status":"identical"}`)
			return
		}
		pr := stackDepsPR{Number: 12, State: "open"}
		pr.Head.SHA = strings.Repeat("a", 40)
		pr.Head.Ref = "stack/topic"
		pr.Head.Repo.FullName = "you/lib"
		if strings.HasSuffix(r.URL.Path, "/pulls") {
			json.NewEncoder(w).Encode([]stackDepsPR{pr})
		} else {
			json.NewEncoder(w).Encode(pr)
		}
	}))
	defer server.Close()
	r := &stackRepo{Upstream: "github.com/acme/lib", Fork: "github.com/you/lib"}
	m := &stackManifest{Repos: map[string]*stackRepo{"app": r, "lib": r}, Proposals: map[string]map[string]stackProposal{"app": {"topic": {Branch: "stack/topic", DependsOn: []string{"lib"}}}, "lib": {"topic": {Branch: "stack/topic"}}}}
	var out bytes.Buffer
	stackDepsReport(t.Context(), m, stackDepsAPI{server.URL, "fake", server.Client()}, &out, true)
	if writes != 1 || !strings.Contains(out.String(), "::warning::") || !strings.Contains(out.String(), "(open)") {
		t.Fatal(out.String(), writes)
	}
}

func TestStackDepsPublishRequiresRepository(t *testing.T) {
	for _, repo := range []string{"", "acme/unrelated"} {
		t.Run(repo, func(t *testing.T) {
			t.Setenv("GITHUB_REPOSITORY", repo)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("unexpected API call") }))
			defer server.Close()
			m := &stackManifest{Repos: map[string]*stackRepo{"app": {Upstream: "github.com/acme/app"}}}
			var out bytes.Buffer
			stackDepsReport(t.Context(), m, stackDepsAPI{server.URL, "fake", server.Client()}, &out, true)
			if calls != 0 || !strings.Contains(out.String(), "Refusing to publish") {
				t.Fatal(out.String(), calls)
			}
		})
	}
}

func TestStackDepsNoDependenciesMakesNoCalls(t *testing.T) {
	old := stackDepsAuthForURL
	t.Cleanup(func() { stackDepsAuthForURL = old })
	stackDepsAuthForURL = func(context.Context, string) *gitrepo.HTTPAuth { t.Error("unexpected credential lookup"); return nil }
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("unexpected API call") }))
	defer server.Close()
	m := &stackManifest{Repos: map[string]*stackRepo{"app": {Upstream: "github.com/acme/app"}}, Proposals: map[string]map[string]stackProposal{"app": {"topic": {Branch: "stack/topic"}}}}
	var out bytes.Buffer
	stackDepsReport(t.Context(), m, stackDepsAPI{server.URL, "", server.Client()}, &out, false)
	if calls != 0 {
		t.Fatal(calls)
	}
}

func TestStackDepsScheduleWithoutPRContext(t *testing.T) {
	t.Setenv("GITHUB_EVENT_NAME", "schedule")
	t.Setenv("GITHUB_EVENT_PATH", "")
	t.Setenv("GITHUB_REPOSITORY", "acme/app")
	t.Setenv("GITHUB_TOKEN", "fake")
	posts := 0
	sha := strings.Repeat("a", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts++
			if r.URL.Path != "/repos/acme/app/statuses/"+sha {
				t.Error("cross-repository publication", r.URL.Path)
			}
			w.WriteHeader(201)
			return
		}
		if strings.Contains(r.URL.Path, "/compare/") {
			fmt.Fprint(w, `{"status":"identical"}`)
			return
		}
		stamp := "2026-11-01"
		pr := stackDepsPR{Number: 12, State: "closed", MergedAt: &stamp, MergeSHA: sha}
		pr.Head.SHA = sha
		pr.Head.Ref = "stack/topic"
		pr.Head.Repo.FullName = "you/lib"
		if strings.Contains(r.URL.Path, "/app/") {
			pr.Head.Repo.FullName = "you/app"
		}
		if strings.HasSuffix(r.URL.Path, "/pulls") {
			json.NewEncoder(w).Encode([]stackDepsPR{pr})
		} else {
			json.NewEncoder(w).Encode(pr)
		}
	}))
	defer server.Close()
	t.Setenv("GITHUB_API_URL", server.URL)
	root := t.TempDir()
	mustGitStack(t, root, "init", "-q")
	mustGitStack(t, root, "config", "user.name", "you")
	mustGitStack(t, root, "config", "user.email", "you@example.com")
	m := &stackManifest{Repos: map[string]*stackRepo{"app": {Upstream: "github.com/acme/app", Fork: "github.com/you/app"}, "lib": {Upstream: "github.com/acme/lib", Fork: "github.com/you/lib"}}, LastSync: map[string]string{"lib": sha}, Proposals: map[string]map[string]stackProposal{"app": {"topic": {Branch: "stack/topic", DependsOn: []string{"lib"}}}, "lib": {"topic": {Branch: "stack/topic", DependsOn: []string{"app"}}}}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "rig.stack.jsonc"), data, 0600); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, root, "add", ".")
	mustGitStack(t, root, "commit", "-qm", "manifest")
	t.Chdir(root)
	var out bytes.Buffer
	c := newStackDepsCmd()
	c.SetOut(&out)
	c.SetArgs([]string{"--publish"})
	if err = c.Execute(); err != nil {
		t.Fatal(err)
	}
	if posts != 1 || !strings.Contains(out.String(), "included in cursor") {
		t.Fatal(posts, out.String())
	}
}

type stackDepsRoundTrip func(*http.Request) (*http.Response, error)

func (f stackDepsRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestStackDepsLocalCredentialFallback(t *testing.T) {
	old := stackDepsAuthForURL
	t.Cleanup(func() { stackDepsAuthForURL = old })
	lookups := 0
	stackDepsAuthForURL = func(_ context.Context, remote string) *gitrepo.HTTPAuth {
		lookups++
		if remote != "https://github.com/" {
			t.Error(remote)
		}
		return &gitrepo.HTTPAuth{Password: "local-token"}
	}
	client := &http.Client{Transport: stackDepsRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer local-token" {
			t.Error("missing local credential")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})}
	a := stackDepsAPI{"https://api.github.com", "", client}
	if err := a.request(t.Context(), "GET", "/repos/acme/lib", nil, nil); err != nil {
		t.Fatal(err)
	}
	if lookups != 1 {
		t.Fatal(lookups)
	}
}
func TestStackDepsDescriptionUnicode(t *testing.T) {
	text := stackDepsDescription(strings.Repeat("界", 141))
	if !utf8.ValidString(text) || len([]rune(text)) != 140 || !strings.HasSuffix(text, "...") {
		t.Fatal(text)
	}
}
