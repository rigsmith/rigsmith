package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStackDepsMergeTypes(t *testing.T) {
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
					if !strings.HasSuffix(r.URL.Path, "/statuses/"+head) {
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
