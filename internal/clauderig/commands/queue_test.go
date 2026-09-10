package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/process"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

type queueCommandFixture struct {
	req                  service.SyncRequest
	dir                  string
	deps                 queueCommandDeps
	identity             service.Identity
	reads, privateChecks int
}

func newQueueFixture(t *testing.T) *queueCommandFixture {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Roots = cfg.Roots[:1]
	cfg.Remote = "https://github.com/acme/private-backup.git"
	f := &queueCommandFixture{req: service.SyncRequest{Config: cfg, Machine: config.Machine{Name: "fixture", OS: config.OSToken(), Home: filepath.Join(root, "home")}, StagingDir: filepath.Join(root, "stage")}, dir: filepath.Join(root, "runtime"), identity: service.Identity{AccountUUID: "11111111-1111-4111-8111-111111111111", Email: "producer@example.com"}}
	f.deps = queueCommandDeps{resolve: func() (service.SyncRequest, error) { return f.req, nil }, identity: func() (service.Identity, error) { f.reads++; return f.identity, nil }, private: func(context.Context, string) error { f.privateChecks++; return nil }, supervise: func(ctx context.Context) (context.Context, error) { return ctx, nil }}
	return f
}
func (f *queueCommandFixture) execute(ctx context.Context, args ...string) (string, error) {
	cmd := newQueueCmd(f.deps)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--dir", f.dir}, args...))
	err := cmd.ExecuteContext(ctx)
	return out.String(), err
}
func (f *queueCommandFixture) must(t *testing.T, args ...string) string {
	t.Helper()
	out, err := f.execute(t.Context(), args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return out
}
func (f *queueCommandFixture) open(t *testing.T) *service.QueueRuntime {
	t.Helper()
	r, err := service.OpenQueueRuntime(t.Context(), f.dir, f.req, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestQueueCommandSavedRequestRetry(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	path := filepath.Join(t.TempDir(), "request.json")
	f.must(t, "prepare", "--session", "s", "--output", path, "--flush")
	saved, err := readQueueRequest(path)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)
	f.identity = service.Identity{AccountUUID: "22222222-2222-4222-8222-222222222222"}
	if _, err := f.execute(t.Context(), "prepare", "--session", "s", "--output", path); !os.IsExist(err) {
		t.Fatalf("overwrote saved input: %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(original, after) {
		t.Fatal("changed existing producer file")
	}
	reads := f.reads
	first := f.must(t, "enqueue", path)
	second := f.must(t, "enqueue", path)
	if first != second || f.reads != reads {
		t.Fatal("retry created another event or reread login", first, second, f.reads)
	}
	jobs, err := f.open(t).Snapshot(t.Context())
	if err != nil || len(jobs) != 1 || len(jobs[0].Events) != 1 {
		t.Fatal(jobs, err)
	}
	event := jobs[0].Events[0]
	if event.Request.ProvenanceID != saved.Request.ProvenanceID || !event.EnqueuedAt.Equal(saved.At) || event.Request.Flush.Mode != queue.All {
		t.Fatal(event, saved)
	}
	out := f.must(t, "status")
	if !strings.Contains(out, "EnqueueHeadroom") || strings.Contains(out, "producer@example.com") || strings.Contains(out, "ProvenanceID") {
		t.Fatal(out)
	}
	if f.privateChecks != 1 {
		t.Fatal("offline producer/status performed network checks", f.privateChecks)
	}
}
func TestQueueCommandRefusals(t *testing.T) {
	f := newQueueFixture(t)
	for _, args := range [][]string{{"status"}, {"enqueue", filepath.Join(t.TempDir(), "missing")}, {"prepare", "--session", "s", "--output", filepath.Join(t.TempDir(), "request")}} {
		if _, err := f.execute(t.Context(), args...); err == nil {
			t.Fatal("accepted missing runtime", args)
		}
	}
	if _, err := os.Stat(f.dir); !os.IsNotExist(err) {
		t.Fatal("created missing runtime", err)
	}
	f.deps.private = func(context.Context, string) error { return errors.New("privacy refused") }
	if _, err := f.execute(t.Context(), "init"); err == nil {
		t.Fatal("created private-unverified runtime")
	}
	if _, err := os.Stat(f.dir); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	f.deps.private = func(context.Context, string) error { return nil }
	f.must(t, "init")
	path := filepath.Join(t.TempDir(), "request")
	f.identity = service.Identity{}
	if _, err := f.execute(t.Context(), "prepare", "--session", "s", "--output", path); err == nil {
		t.Fatal("silently inferred unknown identity")
	}
	f.must(t, "prepare", "--session", "s", "--output", path, "--unknown-identity")
	saved, _ := readQueueRequest(path)
	if saved.Identity != (service.Identity{}) {
		t.Fatal(saved)
	}
	foreign := filepath.Join(filepath.Dir(f.dir), "other-runtime")
	old := f.dir
	f.dir = foreign
	f.must(t, "init")
	if _, err := f.execute(t.Context(), "enqueue", path); !errors.Is(err, queue.ErrBinding) {
		t.Fatal("accepted foreign request", err)
	}
	f.dir = old
	f.req.Config.RedactTranscripts = !f.req.Config.RedactTranscripts
	if _, err := f.execute(t.Context(), "enqueue", path); !errors.Is(err, queue.ErrBinding) {
		t.Fatal("redirected changed configuration", err)
	}
}
func TestQueueRequestBoundsAndShape(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	path := filepath.Join(t.TempDir(), "request")
	f.must(t, "prepare", "--session", "s", "--output", path)
	valid, _ := os.ReadFile(path)
	for name, data := range map[string][]byte{"truncated": valid[:len(valid)/2], "trailing": append(bytes.Clone(valid), []byte("{}")...), "oversized": bytes.Repeat([]byte(" "), queueRequestLimit+1), "unknown": bytes.Replace(valid, []byte(`"Version": 1`), []byte(`"Version": 1, "unexpected": true`), 1), "version": bytes.Replace(valid, []byte(`"Version": 1`), []byte(`"Version": 2`), 1)} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "request")
			if err := os.WriteFile(p, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := f.execute(t.Context(), "enqueue", p); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
	if _, err := readQueueRequest(t.TempDir()); err == nil {
		t.Fatal("accepted directory")
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(path, link); err != nil {
			t.Fatal(err)
		}
		if _, err := readQueueRequest(link); err == nil {
			t.Fatal("accepted linked request")
		}
	}
	jobs, err := f.open(t).Snapshot(t.Context())
	if err != nil || len(jobs) != 0 {
		t.Fatal(jobs, err)
	}
}
func TestQueueCommandStartupRefusalPreservesWork(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	path := filepath.Join(t.TempDir(), "request")
	f.must(t, "prepare", "--session", "s", "--output", path)
	f.must(t, "enqueue", path)
	f.deps.private = func(context.Context, string) error { return errors.New("privacy changed") }
	if _, err := f.execute(t.Context(), "drain"); err == nil || !strings.Contains(err.Error(), "privacy changed") {
		t.Fatal(err)
	}
	jobs, _ := f.open(t).Snapshot(t.Context())
	if len(jobs) != 1 || jobs[0].Attempts != 0 || jobs[0].Status != queue.Pending {
		t.Fatal(jobs)
	}
	for _, args := range [][]string{{"run", "--max-archive-bytes", "-1"}, {"retry", "0"}, {"retry", "invalid"}} {
		if _, err := f.execute(t.Context(), args...); err == nil {
			t.Fatal(args)
		}
	}
}

func TestQueueCommandSupervisedDrain(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") != "1" {
		t.Skip("set CLAUDERIG_E2E=1 for synthetic Git integration")
	}
	// Exercise the real executable entrypoint, not a test-only supervisor dispatch.
	binary := filepath.Join(t.TempDir(), "clauderig")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "../../../cmd/clauderig")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	f := newQueueFixture(t)
	root := filepath.Dir(f.dir)
	t.Setenv("HOME", f.req.Machine.Home)
	t.Setenv("USERPROFILE", f.req.Machine.Home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	if err := os.WriteFile(filepath.Join(root, "gitconfig"), []byte("[user]\n name = Fixture\n email = fixture@example.com\n[init]\n defaultBranch = main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(root, "remote.git")
	runGit := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	runGit("init", "--bare", remote)
	f.req.Config.Remote = remote
	settings := filepath.Join(f.req.Machine.Home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := (service.Service{ReadIdentity: func() (service.Identity, error) { return f.identity, nil }}).Sync(t.Context(), f.req); err != nil {
		t.Fatal(err)
	}
	f.deps.supervise = func(ctx context.Context) (context.Context, error) {
		return process.WithSupervisor(ctx, binary, "__queue-supervisor"), nil
	}
	f.must(t, "init")
	session := filepath.Join(f.req.Machine.Home, ".claude", "projects", "-workspace-acme", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(session), 0700); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","sessionId":"s","uuid":"message-1","cwd":"/workspace/acme","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"queued command fixture"}}` + "\n"
	if err := os.WriteFile(session, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "request")
	f.must(t, "prepare", "--session", "s", "--output", path, "--flush")
	f.must(t, "enqueue", path)
	f.deps.identity = func() (service.Identity, error) { t.Fatal("worker read current login"); return service.Identity{}, nil }
	// A size refusal blocks durably; explicit retry retains its saved intent.
	if _, err := f.execute(t.Context(), "drain", "--max-archive-bytes", "1"); !errors.Is(err, queue.ErrUndrained) {
		t.Fatalf("expected blocked drain: %v", err)
	}
	jobs, _ := f.open(t).Snapshot(t.Context())
	if len(jobs) != 1 || jobs[0].Status != queue.Blocked {
		t.Fatal(jobs)
	}
	attempts := jobs[0].Attempts
	f.must(t, "retry", fmt.Sprint(jobs[0].ID))
	jobs, _ = f.open(t).Snapshot(t.Context())
	if jobs[0].Attempts != attempts || jobs[0].Status != queue.Pending {
		t.Fatal(jobs)
	}
	out := f.must(t, "drain")
	if !strings.Contains(out, "1 batches completed") {
		t.Fatal(out)
	}
	jobs, _ = f.open(t).Snapshot(t.Context())
	if len(jobs) != 0 {
		t.Fatal(jobs)
	}
	clone := filepath.Join(root, "clone")
	runGit("clone", "--branch", "main", remote, clone)
	entries := ledger.LoadAll(clone)
	found := false
	for _, e := range entries {
		if e.ID == "s" {
			found = true
			if e.Account != f.identity.AccountUUID {
				t.Fatal(e)
			}
		}
	}
	if !found {
		t.Fatal("missing queued session", entries)
	}
	// Admission retry after completion returns its original receipt, without new work.
	f.must(t, "enqueue", path)
	jobs, _ = f.open(t).Snapshot(t.Context())
	if len(jobs) != 0 {
		t.Fatal(jobs)
	}
	f.must(t, "drain")
	if f.privateChecks < 4 {
		t.Fatal("missing startup/per-batch privacy checks", f.privateChecks)
	}
}

func TestQueueCommandHistoryRefusal(t *testing.T) {
	f := newQueueFixture(t)
	f.req.Config.Remote = filepath.Join(t.TempDir(), "remote.git")
	f.must(t, "init")
	// No staging HEAD: the startup gate refuses before contacting the fixture remote.
	_, err := f.execute(t.Context(), "drain")
	if err == nil {
		t.Fatal("uninitialized history accepted")
	}
}

func TestQueueCommandGracefulThenCancelled(t *testing.T) {
	signals := make(chan os.Signal, 2)
	ctx, stop, finish := queueStopContext(t.Context(), signals)
	defer finish()
	signals <- os.Interrupt
	select {
	case <-stop:
	case <-time.After(time.Second):
		t.Fatal("first signal did not request stop")
	}
	if ctx.Err() != nil {
		t.Fatal("first signal cancelled active cleanup")
	}
	signals <- os.Interrupt
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("second signal did not cancel")
	}
}
