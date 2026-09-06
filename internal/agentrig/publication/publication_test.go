package publication_test

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/agentrig/publication"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for k, v := range map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(root, "no-global-config"),
		"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "commit.gpgsign", "GIT_CONFIG_VALUE_0": "false",
		"GIT_AUTHOR_NAME": "Fixture", "GIT_AUTHOR_EMAIL": "fixture@example.test",
		"GIT_COMMITTER_NAME": "Fixture", "GIT_COMMITTER_EMAIL": "fixture@example.test",
		"GIT_ALLOW_PROTOCOL": "file",
	} {
		t.Setenv(k, v)
	}
	return root
}
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func put(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
func policy(t *testing.T) publication.Policy {
	return publication.Policy{
		Init: func(ctx context.Context, dir string) (*gitrepo.Repo, error) {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, err
			}
			if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
				git(t, dir, "init", "-b", "archive")
			}
			return gitrepo.Open(ctx, dir)
		},
		Prepare:       func(context.Context, string) error { return nil },
		Validate:      func(context.Context, string) error { return nil },
		Audit:         func(string) error { return nil },
		Resolve:       func(context.Context, *gitrepo.Repo) ([]string, error) { return nil, errors.New("unexpected conflict") },
		HumanRequired: func([]string) error { return errors.New("manual resolution required") },
	}
}
func plan() publication.Plan {
	return publication.Plan{
		RemoteName: "upstream", Branch: "archive", SnapshotMessage: "capture fixture", PushRetries: 2,
		History:   &publication.HistoryPlan{Branch: "settings-log", Paths: []string{"prefs"}, CommitMessage: "settings snapshot", SquashMessage: "compact settings", MaxCommits: 10},
		Retention: publication.Retention{FloorBytes: 1 << 30, KeepDays: 7, FoldMessage: func(time.Time) string { return "old snapshots" }},
	}
}
func TestPublishRetriesLocalCommitAndUsesSelectedHistory(t *testing.T) {
	root := fixture(t)
	staging, remote := filepath.Join(root, "store"), filepath.Join(root, "remote.git")
	put(t, staging, "prefs/settings.toml", "theme = 'dark'\n")
	put(t, staging, "sessions/native.log", "opaque native bytes\r\n")
	var calls []string
	var events []publication.Event
	p := policy(t)
	init := p.Init
	p.Init = func(ctx context.Context, dir string) (*gitrepo.Repo, error) {
		calls = append(calls, "init")
		return init(ctx, dir)
	}
	p.Prepare = func(context.Context, string) error { calls = append(calls, "prepare"); return nil }
	p.Validate = func(context.Context, string) error { calls = append(calls, "validate"); return nil }
	p.Audit = func(string) error { calls = append(calls, "audit"); return nil }
	w := publication.Workflow{Policy: p, Observe: func(e publication.Event) { events = append(events, e) }}
	req := publication.PublishRequest{StagingDir: staging, Remote: remote, Plan: plan()}
	first, err := w.Publish(t.Context(), req)
	if err == nil || !first.Committed || first.Pushed || len(events) != 0 {
		t.Fatalf("offline: %+v, %v, events %v", first, err, events)
	}
	if !reflect.DeepEqual(calls, []string{"init", "prepare", "audit", "validate", "audit"}) {
		t.Fatalf("audit order: %v", calls)
	}
	head := git(t, staging, "rev-parse", "HEAD")
	git(t, root, "init", "--bare", "-b", "archive", remote)
	second, err := w.Publish(t.Context(), req)
	if err != nil || second.Committed || !second.Pushed {
		t.Fatalf("retry: %+v, %v", second, err)
	}
	if git(t, remote, "rev-parse", "archive") != head {
		t.Fatal("pending commit was replaced")
	}
	if got := git(t, remote, "ls-tree", "-r", "--name-only", "settings-log"); got != "prefs/settings.toml" {
		t.Fatalf("history tree: %s", got)
	}
	if got := git(t, remote, "log", "-1", "--format=%s", "settings-log"); got != "settings snapshot" {
		t.Fatalf("history message: %s", got)
	}
	if len(events) != 1 {
		t.Fatalf("publication events: %v", events)
	}
	if e, ok := events[0].(publication.Published); !ok || e.LocalOnly || e.Result != second {
		t.Fatalf("publication event: %+v", events[0])
	}
}
func TestReconciledTreeMustPassAuditBeforeCommit(t *testing.T) {
	root := fixture(t)
	staging, remote, other := filepath.Join(root, "store"), filepath.Join(root, "remote.git"), filepath.Join(root, "other")
	git(t, root, "init", "--bare", "-b", "archive", remote)
	put(t, staging, "prefs/settings.toml", "initial\n")
	w := publication.Workflow{Policy: policy(t)}
	req := publication.PublishRequest{StagingDir: staging, Remote: remote, Plan: plan()}
	if _, err := w.Publish(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	git(t, root, "clone", remote, other)
	put(t, other, "prefs/settings.toml", "blocked by policy\n")
	git(t, other, "add", ".")
	git(t, other, "commit", "-m", "remote change")
	git(t, other, "push")
	remoteHead := git(t, remote, "rev-parse", "archive")
	put(t, staging, "prefs/settings.toml", "local change\n")
	refused := errors.New("audit refused merged content")
	w.Policy.Resolve = func(ctx context.Context, repo *gitrepo.Repo) ([]string, error) {
		git(t, repo.Dir, "checkout", "--theirs", "--", "prefs/settings.toml")
		git(t, repo.Dir, "add", "prefs/settings.toml")
		return nil, nil
	}
	w.Policy.Audit = func(dir string) error {
		b, err := os.ReadFile(filepath.Join(dir, "prefs/settings.toml"))
		if err != nil {
			return err
		}
		if strings.Contains(string(b), "blocked") {
			return refused
		}
		return nil
	}
	result, err := w.Publish(t.Context(), req)
	if !errors.Is(err, refused) || !result.Committed || result.Pushed {
		t.Fatalf("reconciled audit: %+v, %v", result, err)
	}
	repo, err := gitrepo.Open(t.Context(), staging)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.InMerge(t.Context()) {
		t.Fatal("refused merge must remain pending")
	}
	if git(t, remote, "rev-parse", "archive") != remoteHead {
		t.Fatal("refused content was published")
	}
}
func TestMissingPolicyDoesNotInitializeStore(t *testing.T) {
	root := fixture(t)
	staging := filepath.Join(root, "store")
	_, err := (publication.Workflow{}).Publish(t.Context(), publication.PublishRequest{StagingDir: staging, Plan: plan()})
	if err == nil {
		t.Fatal("missing policy accepted")
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("store initialized: %v", err)
	}
}

func TestInvalidPlansDoNotInitializeStore(t *testing.T) {
	cases := map[string]func(*publication.Plan){
		"missing remote":                    func(p *publication.Plan) { p.RemoteName = " " },
		"missing branch":                    func(p *publication.Plan) { p.Branch = "" },
		"missing snapshot message":          func(p *publication.Plan) { p.SnapshotMessage = " " },
		"negative retries":                  func(p *publication.Plan) { p.PushRetries = -1 },
		"missing fold message":              func(p *publication.Plan) { p.Retention.FoldMessage = nil },
		"zero keep days":                    func(p *publication.Plan) { p.Retention.KeepDays = 0 },
		"negative keep days":                func(p *publication.Plan) { p.Retention.KeepDays = -1 },
		"negative floor":                    func(p *publication.Plan) { p.Retention.FloorBytes = -1 },
		"negative factor":                   func(p *publication.Plan) { p.Retention.SquashFactor = -1 },
		"nan factor":                        func(p *publication.Plan) { p.Retention.SquashFactor = math.NaN() },
		"infinite factor":                   func(p *publication.Plan) { p.Retention.SquashFactor = math.Inf(1) },
		"missing history branch":            func(p *publication.Plan) { p.History.Branch = " " },
		"history overwrites primary branch": func(p *publication.Plan) { p.History.Branch = p.Branch },
		"missing history paths":             func(p *publication.Plan) { p.History.Paths = nil },
		"empty history path":                func(p *publication.Plan) { p.History.Paths = []string{"prefs", ""} },
		"missing history commit message":    func(p *publication.Plan) { p.History.CommitMessage = "" },
		"missing history squash message":    func(p *publication.Plan) { p.History.SquashMessage = " " },
		"zero history limit":                func(p *publication.Plan) { p.History.MaxCommits = 0 },
		"negative history limit":            func(p *publication.Plan) { p.History.MaxCommits = -1 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			staging := filepath.Join(t.TempDir(), "store")
			p := plan()
			change(&p)
			pol := policy(t)
			pol.Init = func(context.Context, string) (*gitrepo.Repo, error) {
				t.Fatal("invalid plan reached repository initialization")
				return nil, nil
			}
			result, err := (publication.Workflow{Policy: pol}).Publish(t.Context(), publication.PublishRequest{StagingDir: staging, Plan: p})
			if err == nil || result.Committed || result.Pushed {
				t.Fatalf("invalid plan: %+v, %v", result, err)
			}
			if _, err := os.Stat(staging); !os.IsNotExist(err) {
				t.Fatalf("invalid plan touched store: %v", err)
			}
		})
	}
}

func TestOptionalHistoryAndZeroThresholdsRemainValid(t *testing.T) {
	root := fixture(t)
	p := plan()
	p.History = nil
	p.PushRetries = 0
	p.Retention.FloorBytes = 0
	p.Retention.SquashFactor = 0
	staging := filepath.Join(root, "store")
	put(t, staging, "native.log", "fixture")
	result, err := (publication.Workflow{Policy: policy(t)}).Publish(t.Context(), publication.PublishRequest{StagingDir: staging, Plan: p})
	if err != nil || !result.Committed || result.Pushed {
		t.Fatalf("valid zero thresholds: %+v, %v", result, err)
	}
}
