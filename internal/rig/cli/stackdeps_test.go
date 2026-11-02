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
	"sync"
	"testing"
	"time"
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
		if err := stackRememberProposal(src, m, name, "topic", "stack/topic", strings.Repeat("a", 40), io.Discard); err != nil {
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
	m := &stackManifest{Repos: map[string]*stackRepo{"app": {Upstream: "github.com/acme/app", Fork: "github.com/you/app"}, "lib": {Upstream: "github.com/acme/lib", Fork: "github.com/you/lib"}}, LastSync: map[string]string{"lib": sha}, Proposals: map[string]map[string]stackProposal{"app": {"topic": {Branch: "stack/topic", DependsOn: []string{"lib"}}}, "lib": {"topic": {Branch: "stack/topic"}, "other": {Branch: "stack/other", DependsOn: []string{"app"}}}}}
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

// The handler reads output while status is still running.
type stackDepsLiveOutput struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (o *stackDepsLiveOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Write(p)
}
func (o *stackDepsLiveOutput) String() string { o.mu.Lock(); defer o.mu.Unlock(); return o.b.String() }

func TestStackDepsStatusLocalFirstAndBounded(t *testing.T) {
	var out stackDepsLiveOutput
	observed := make(chan time.Time, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(out.String(), "app") {
			t.Error("network report preceded local status")
		}
		select {
		case observed <- time.Now():
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	t.Setenv("GITHUB_API_URL", server.URL)
	t.Setenv("GITHUB_TOKEN", "fake")
	root := t.TempDir()
	mustGitStack(t, root, "init", "-q")
	mustGitStack(t, root, "config", "user.name", "you")
	mustGitStack(t, root, "config", "user.email", "you@example.com")
	sha := strings.Repeat("a", 40)
	m := &stackManifest{Repos: map[string]*stackRepo{"app": {Upstream: "github.com/acme/app", Fork: "github.com/you/app", UpstreamCommit: sha}, "lib": {Upstream: "github.com/acme/lib", Fork: "github.com/you/lib", UpstreamCommit: sha}}, Proposals: map[string]map[string]stackProposal{"app": {"topic": {Branch: "stack/topic", DependsOn: []string{"lib"}}}, "lib": {"topic": {Branch: "stack/topic"}}}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeStackManifest(t, root, string(data))
	mustGitStack(t, root, "add", ".")
	mustGitStack(t, root, "commit", "-qm", "manifest")
	t.Chdir(root)
	c := newStackStatusCmd()
	c.SetOut(&out)
	c.SetErr(&out)
	if err = c.Execute(); err != nil {
		t.Fatal(err)
	}
	select {
	case started := <-observed:
		if elapsed := time.Since(started); elapsed > 4*time.Second {
			t.Fatalf("dependency report stalled for %v", elapsed)
		}
	default:
		t.Fatal("hanging server was not reached")
	}

	if !strings.Contains(out.String(), "could not check") {
		t.Fatal(out.String())
	}
}

func TestStackDepsInvalidMemberRejectedBeforePublication(t *testing.T) {
	for _, dep := range []string{"", " ", "missing", "app"} {
		t.Run(fmt.Sprintf("dependency=%q", dep), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				t.Error("invalid manifest reached API", r.Method)
				w.WriteHeader(500)
			}))
			defer server.Close()
			t.Setenv("GITHUB_API_URL", server.URL)
			t.Setenv("GITHUB_TOKEN", "fake")
			t.Setenv("GITHUB_REPOSITORY", "acme/app")
			root := t.TempDir()
			mustGitStack(t, root, "init", "-q")
			mustGitStack(t, root, "config", "user.name", "you")
			mustGitStack(t, root, "config", "user.email", "you@example.com")
			m := &stackManifest{Repos: map[string]*stackRepo{"app": {Upstream: "github.com/acme/app", Fork: "github.com/you/app"}}, Proposals: map[string]map[string]stackProposal{"app": {"topic": {Branch: "stack/topic", DependsOn: []string{dep}}}}}
			if err := m.validate(); err == nil || !strings.Contains(err.Error(), "dependsOn entry") {
				t.Fatalf("validation: %v", err)
			}
			data, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			writeStackManifest(t, root, string(data))
			mustGitStack(t, root, "add", ".")
			mustGitStack(t, root, "commit", "-qm", "manifest")
			t.Chdir(root)
			var out bytes.Buffer
			c := newStackDepsCmd()
			c.SetOut(&out)
			c.SetErr(&out)
			c.SetArgs([]string{"--publish"})
			err = c.Execute()
			if err == nil || !strings.Contains(err.Error(), "dependsOn entry") || calls != 0 {
				t.Fatalf("error %v; API calls %d", err, calls)
			}
		})
	}
}

func TestStackDepsProposalCycles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edges map[string][]string
		cycle string
	}{
		{"two members", map[string][]string{"app": {"lib"}, "lib": {"app"}}, "app -> lib -> app"},
		{"three members", map[string][]string{"app": {"lib"}, "lib": {"core"}, "core": {"app"}}, "app -> lib -> core -> app"},
		{"shared prerequisite", map[string][]string{"app": {"lib", "core"}, "lib": {"core"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &stackManifest{Repos: map[string]*stackRepo{}, Proposals: map[string]map[string]stackProposal{}}
			for _, member := range []string{"app", "lib", "core"} {
				m.Repos[member] = &stackRepo{Upstream: "github.com/acme/" + member, Fork: "github.com/you/" + member}
				m.Proposals[member] = map[string]stackProposal{"topic": {Branch: "stack/topic", DependsOn: tc.edges[member]}}
			}
			err := m.validate()
			if tc.cycle != "" {
				if err == nil || !strings.Contains(err.Error(), "dependsOn cycle") || !strings.Contains(err.Error(), tc.cycle) {
					t.Fatalf("expected cycle %q: %v", tc.cycle, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			// A reverse edge in another topic must not turn an acyclic topic into a cycle.
			m.Proposals["app"]["topic"] = stackProposal{Branch: "stack/topic", DependsOn: []string{"lib"}}
			m.Proposals["lib"]["topic"] = stackProposal{Branch: "stack/topic"}
			m.Proposals["core"]["topic"] = stackProposal{Branch: "stack/topic"}
			m.Proposals["lib"]["other"] = stackProposal{Branch: "stack/other", DependsOn: []string{"app"}}
			if err := m.validate(); err != nil {
				t.Fatalf("independent topics: %v", err)
			}
		})
	}
}

func TestStackDepsRememberProposalSkipsCyclicEdges(t *testing.T) {
	for _, transitive := range []bool{false, true} {
		t.Run(fmt.Sprintf("transitive=%v", transitive), func(t *testing.T) {
			root := t.TempDir()
			m := &stackManifest{Repos: map[string]*stackRepo{}, Proposals: map[string]map[string]stackProposal{}}
			for _, member := range []string{"app", "core", "lib", "util"} {
				m.Repos[member] = &stackRepo{Upstream: "github.com/acme/" + member, Fork: "github.com/you/" + member}
			}
			m.Proposals["app"] = map[string]stackProposal{"topic": {Branch: "stack/topic", DependsOn: []string{"lib"}}}
			m.Proposals["util"] = map[string]stackProposal{"topic": {Branch: "stack/topic"}}
			if transitive {
				m.Proposals["app"]["topic"] = stackProposal{Branch: "stack/topic", DependsOn: []string{"core"}}
				m.Proposals["core"] = map[string]stackProposal{"topic": {Branch: "stack/topic", DependsOn: []string{"lib"}}}
			}
			data, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			writeStackManifest(t, root, string(data))
			m, src, err := loadStackManifest(root)
			if err != nil {
				t.Fatal(err)
			}
			var notice bytes.Buffer
			if err = stackRememberProposal(src, m, "lib", "topic", "stack/topic", strings.Repeat("a", 40), &notice); err != nil {
				t.Fatalf("recording an already-pushed proposal should succeed: %v", err)
			}
			lines := strings.Split(strings.TrimSpace(notice.String()), "\n")
			wantLines := 1
			if transitive {
				wantLines = 2
			}
			if len(lines) != wantLines {
				t.Fatalf("expected one line per skipped edge: %q", notice.String())
			}
			for _, line := range lines {
				if !strings.Contains(line, "skipped inferred dependsOn") || !strings.Contains(line, "would create a dependency cycle") {
					t.Fatal(line)
				}
			}
			m, _, err = loadStackManifest(root)
			if err != nil {
				t.Fatalf("proposal left an unusable manifest: %v", err)
			}
			proposal := m.Proposals["lib"]["topic"]
			if proposal.Branch != "stack/topic" || len(proposal.DependsOn) != 1 || proposal.DependsOn[0] != "util" {
				t.Fatalf("must retain proposal and safe inferred edge: %+v", proposal)
			}
		})
	}
}

func TestStackDepsExistingCycleErrorNamesFileChainAndRepair(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, root, "init", "-q")
	t.Chdir(root)
	m := &stackManifest{Repos: map[string]*stackRepo{"app": {Upstream: "github.com/acme/app", Fork: "github.com/you/app"}, "lib": {Upstream: "github.com/acme/lib", Fork: "github.com/you/lib"}}, Proposals: map[string]map[string]stackProposal{"app": {"topic": {Branch: "stack/topic", DependsOn: []string{"lib"}}}, "lib": {"topic": {Branch: "stack/topic", DependsOn: []string{"app"}}}}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeStackManifest(t, root, string(data))
	for _, verb := range []string{"status", "pull", "propose"} {
		t.Run(verb, func(t *testing.T) {
			c := newStackStatusCmd()
			switch verb {
			case "pull":
				c = newStackPullCmd()
			case "propose":
				c = newStackSendCmd()
				c.SetArgs([]string{"app", "topic"})
			}
			var out bytes.Buffer
			c.SetOut(&out)
			c.SetErr(&out)
			err := c.Execute()
			if err == nil {
				t.Fatal("accepted an already-cyclic manifest")
			}
			for _, want := range []string{filepath.Join(root, "rig.stack.jsonc"), `topic "topic"`, "app -> lib -> app", "edit proposals[member][topic].dependsOn", "remove or reverse an edge", "retry"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("missing %q in %v", want, err)
				}
			}
		})
	}
}

func TestStackDepsPublishDryRun(t *testing.T) {
	stackDryRun(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Error("dry run posted a GitHub status")
		}
		t.Error("refused dry run contacted API")
	}))
	defer server.Close()
	t.Setenv("GITHUB_API_URL", server.URL)
	summary := filepath.Join(t.TempDir(), "summary")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	cmd := newStackDepsCmd()
	if err := cmd.Flags().Set("publish", "true"); err != nil {
		t.Fatal(err)
	}
	_, err := runVerbOut(context.Background(), cmd)
	if err == nil || !strings.Contains(err.Error(), "--dry-run is not supported for stack deps") {
		t.Fatalf("expected refusal: %v", err)
	}
	if _, err := os.Stat(summary); !os.IsNotExist(err) {
		t.Fatal("dry run wrote summary")
	}
}
