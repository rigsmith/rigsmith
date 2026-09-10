package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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
	privateRemotes       []string
}

func newQueueFixture(t *testing.T) *queueCommandFixture {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Roots = cfg.Roots[:1]
	cfg.Remote = "https://github.com/acme/private-backup.git"
	f := &queueCommandFixture{req: service.SyncRequest{Config: cfg, Machine: config.Machine{Name: "fixture", OS: config.OSToken(), Home: filepath.Join(root, "home")}, StagingDir: filepath.Join(root, "stage")}, dir: filepath.Join(root, "runtime"), identity: service.Identity{AccountUUID: "11111111-1111-4111-8111-111111111111", Email: "producer@example.com"}}
	f.deps = queueCommandDeps{resolve: func() (service.SyncRequest, error) { return f.req, nil }, identity: func() (service.Identity, error) { f.reads++; return f.identity, nil }, private: func(_ context.Context, remote string) error {
		f.privateChecks++
		f.privateRemotes = append(f.privateRemotes, remote)
		return nil
	}, supervise: func(ctx context.Context) (context.Context, error) { return ctx, nil }}
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
	if !strings.Contains(out, "enqueueHeadroom") || strings.Contains(out, "producer@example.com") || strings.Contains(out, "ProvenanceID") {
		t.Fatal(out)
	}
	if !slices.Equal(f.privateRemotes, []string{f.req.Config.Remote}) {
		t.Fatal("wrong init privacy remote", f.privateRemotes)
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
	for _, checked := range f.privateRemotes {
		if checked != remote {
			t.Fatal("wrong init/startup/batch privacy remote", checked, remote)
		}
	}
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

func TestQueueCommandRequestTreeExclusion(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	source := filepath.Join(f.req.Machine.Home, ".claude")
	roots := []string{source, f.req.StagingDir, f.dir}
	for _, root := range roots {
		if err := os.MkdirAll(filepath.Join(root, "nested"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		for _, root := range append([]string(nil), roots...) {
			link := filepath.Join(t.TempDir(), "alias")
			if err := os.Symlink(root, link); err != nil {
				t.Fatal(err)
			}
			roots = append(roots, link)
		}
	}
	valid := filepath.Join(t.TempDir(), "request")
	f.must(t, "prepare", "--session", "s", "--output", valid)
	data, err := os.ReadFile(valid)
	if err != nil {
		t.Fatal(err)
	}
	for i, root := range roots {
		path := filepath.Join(root, "nested", fmt.Sprintf("request-%d.json", i))
		if _, err := f.execute(t.Context(), "prepare", "--session", "s", "--output", path); !errors.Is(err, queue.ErrBinding) {
			t.Fatalf("prepare %s: %v", path, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("wrote forbidden path: %v", err)
		}
		// A previously copied request is also refused without rewriting its bytes.
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := f.execute(t.Context(), "enqueue", path); !errors.Is(err, queue.ErrBinding) {
			t.Fatalf("enqueue %s: %v", path, err)
		}
		got, _ := os.ReadFile(path)
		if !bytes.Equal(got, data) {
			t.Fatal("rewrote excluded request")
		}
	}
	jobs, err := f.open(t).Snapshot(t.Context())
	if err != nil || len(jobs) != 0 {
		t.Fatal(jobs, err)
	}
}

func TestQueueCommandRemoteValidatedBeforePrivacy(t *testing.T) {
	for _, remote := range []string{"https://user:secret@github.com/acme/repo", "https://github.com/acme/repo?token=secret", "https://github.com/acme/repo#secret", "https://github.com/acme/repo?"} {
		f := newQueueFixture(t)
		f.req.Config.Remote = remote
		out, err := f.execute(t.Context(), "init")
		if err == nil || f.privateChecks != 0 || strings.Contains(out+err.Error(), "secret") {
			t.Fatal("malformed remote reached privacy check or diagnostic", remote, out, err, f.privateChecks)
		}
		if _, err := os.Stat(f.dir); !os.IsNotExist(err) {
			t.Fatal("created malformed remote runtime", err)
		}
		// Both worker resolver and init use this same validation-before-privacy path.
		if _, err := f.deps.remote(t.Context(), f.req); err == nil || f.privateChecks != 0 {
			t.Fatal("resolver accepted malformed remote", err)
		}
	}
}

func TestQueueRequestReflushRejectsChangedFile(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint(replace), func(t *testing.T) {
			f := newQueueFixture(t)
			f.must(t, "init")
			path := filepath.Join(t.TempDir(), "request")
			f.must(t, "prepare", "--session", "s", "--output", path)
			saved, err := loadQueueRequest(path)
			if err != nil {
				t.Fatal(err)
			}
			changed := saved.submission
			changed.Request.EventID = "changed-event"
			changed.Checksum = queueRequestChecksum(changed)
			data, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if replace {
				// Rename retains an independently opened identity on Windows too.
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := saved.confirm(t.Context(), path); err == nil {
				t.Fatal("confirmed replaced or modified request")
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("clobbered replacement", err)
			}
			jobs, err := f.open(t).Snapshot(t.Context())
			if err != nil || len(jobs) != 0 {
				t.Fatal(jobs, err)
			}
		})
	}
}

func TestQueueCommandJSONKeys(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	path := filepath.Join(t.TempDir(), "request")
	f.must(t, "prepare", "--session", "s", "--output", path)
	receipt := f.must(t, "enqueue", path)
	if receipt != "{\"generation\":1,\"batch\":1}\n" {
		t.Fatal(receipt)
	}
	var status map[string]json.RawMessage
	if err := json.Unmarshal([]byte(f.must(t, "status")), &status); err != nil {
		t.Fatal(err)
	}
	assertKeys := func(got map[string]json.RawMessage, want []string) {
		t.Helper()
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		slices.Sort(want)
		if !slices.Equal(keys, want) {
			t.Fatal(keys, want)
		}
	}
	assertKeys(status, []string{"batches", "capacity"})
	var batches []map[string]json.RawMessage
	if err := json.Unmarshal(status["batches"], &batches); err != nil || len(batches) != 1 {
		t.Fatal(batches, err)
	}
	assertKeys(batches[0], []string{"batch", "phase", "status", "events", "attempts", "notBefore", "failureCode"})
	var capacity map[string]json.RawMessage
	if err := json.Unmarshal(status["capacity"], &capacity); err != nil {
		t.Fatal(err)
	}
	assertKeys(capacity, []string{"payloadBytes", "stateLimit", "enqueueLimit", "enqueueHeadroom", "outstandingBatches", "batchLimit", "pendingBatches", "runningBatches", "blockedBatches", "outstandingEvents", "completedReceipts", "completedBatches", "retiredReceipts", "replayBefore", "remedies"})
}

func TestQueueCommandConcurrentAdmissionOfSavedFile(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	path := filepath.Join(t.TempDir(), "request")
	f.must(t, "prepare", "--session", "s", "--output", path)
	results := make(chan error, 8)
	for range 8 {
		go func() {
			out, err := f.execute(t.Context(), "enqueue", path)
			if err == nil && out != "{\"generation\":1,\"batch\":1}\n" {
				err = fmt.Errorf("receipt %s", out)
			}
			results <- err
		}()
	}
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := f.open(t).Snapshot(t.Context())
	if err != nil || len(jobs) != 1 || len(jobs[0].Events) != 1 {
		t.Fatal(jobs, err)
	}
}

func TestQueueCommandPreservesPartialIdentity(t *testing.T) {
	for _, identity := range []service.Identity{
		{Email: "producer@example.com"},
		{OrganizationUUID: "33333333-3333-4333-8333-333333333333"},
		{Email: "producer@example.com", OrganizationUUID: "33333333-3333-4333-8333-333333333333"},
	} {
		f := newQueueFixture(t)
		f.identity = identity
		f.must(t, "init")
		path := filepath.Join(t.TempDir(), "request")
		f.must(t, "prepare", "--session", "s", "--output", path)
		saved, err := readQueueRequest(path)
		if err != nil || saved.Identity != identity {
			t.Fatal("lost partial identity", saved.Identity, err)
		}
		f.deps.identity = func() (service.Identity, error) { t.Fatal("admission reread identity"); return service.Identity{}, nil }
		f.must(t, "enqueue", path)
		jobs, err := f.open(t).Snapshot(t.Context())
		if err != nil || len(jobs) != 1 {
			t.Fatal(jobs, err)
		}
		provenance, err := service.CaptureProvenance(identity)
		if err != nil || jobs[0].Events[0].Request.ProvenanceID != provenance {
			t.Fatal(jobs, err)
		}
	}
}

func TestQueueRequestRejectsAmbiguousJSON(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	path := filepath.Join(t.TempDir(), "request")
	f.must(t, "prepare", "--session", "s", "--output", path)
	data, _ := os.ReadFile(path)
	// Each shadow value is followed by the original: ordinary decoding retains
	// the original value and checksum, so only member validation rejects these.
	for _, field := range []string{"Version", "Scope", "Identity", "AccountUUID", "Request", "EventID", "Flush", "Mode"} {
		for _, alias := range []bool{false, true} {
			name := field
			if alias {
				name = strings.ToLower(field)
			}
			needle := []byte(`"` + field + `":`)
			edited := bytes.Replace(data, needle, []byte(`"`+name+`": null, `+string(needle)), 1)
			if bytes.Equal(edited, data) {
				t.Fatal("fixture lacks field", field)
			}
			copy := filepath.Join(t.TempDir(), "request")
			if err := os.WriteFile(copy, edited, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := f.execute(t.Context(), "enqueue", copy); err == nil {
				t.Fatal("accepted ambiguous field", field, alias)
			}
		}
	}
	if _, err := f.execute(t.Context(), "enqueue", path); err != nil {
		t.Fatal("rejected original", err)
	}
	jobs, _ := f.open(t).Snapshot(t.Context())
	if len(jobs) != 1 || len(jobs[0].Events) != 1 {
		t.Fatal(jobs)
	}
}

func TestQueueCommandRejectsHardLinkedRequest(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	path := filepath.Join(t.TempDir(), "request")
	f.must(t, "prepare", "--session", "s", "--output", path)
	data, _ := os.ReadFile(path)
	alias := filepath.Join(f.req.Machine.Home, ".claude", "projects", "fixture", "request.jsonl")
	if err := os.MkdirAll(filepath.Dir(alias), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := f.execute(t.Context(), "prepare", "--session", "s", "--output", path); !os.IsExist(err) {
		t.Fatal("prepare replaced linked file", err)
	}
	if _, err := f.execute(t.Context(), "enqueue", path); err == nil || !strings.Contains(err.Error(), "hard link") {
		t.Fatal("admitted linked request", err)
	}
	for _, p := range []string{path, alias} {
		got, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatal("modified link", err)
		}
	}
	jobs, err := f.open(t).Snapshot(t.Context())
	if err != nil || len(jobs) != 0 {
		t.Fatal(jobs, err)
	}
	// After the operator removes the source alias, normal admission resumes.
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	f.must(t, "enqueue", path)
}

func TestQueueCommandExcludesUnselectedDesktopProfiles(t *testing.T) {
	for _, link := range []string{"none", "profile", "data"} {
		if link != "none" && runtime.GOOS == "windows" {
			continue
		}
		f := newQueueFixture(t)
		f.must(t, "init") // No selected profiles.
		store := filepath.Join(f.req.Machine.Home, ".clauderig", "desktop")
		profile := filepath.Join(store, "unselected")
		if link == "profile" {
			target := t.TempDir()
			if err := os.MkdirAll(store, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, profile); err != nil {
				t.Fatal(err)
			}
			profile = target // Request via the symlink target's outside spelling.
		}
		if err := os.MkdirAll(filepath.Join(profile, "data", "nested"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profile, "profile.json"), []byte(`{"name":"unselected"}`), 0600); err != nil {
			t.Fatal(err)
		}
		dataRoot := filepath.Join(profile, "data")
		if link == "data" {
			target := t.TempDir()
			if err := os.RemoveAll(dataRoot); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, dataRoot); err != nil {
				t.Fatal(err)
			}
			dataRoot = target
			if err := os.MkdirAll(filepath.Join(dataRoot, "nested"), 0700); err != nil {
				t.Fatal(err)
			}
		}
		path := filepath.Join(dataRoot, "nested", "request.json")
		if _, err := f.execute(t.Context(), "prepare", "--session", "s", "--output", path); !errors.Is(err, queue.ErrBinding) {
			t.Fatal("accepted unselected profile", link, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("wrote into unselected profile", err)
		}
		valid := filepath.Join(t.TempDir(), "request")
		f.must(t, "prepare", "--session", "s", "--output", valid)
		data, _ := os.ReadFile(valid)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := f.execute(t.Context(), "enqueue", path); !errors.Is(err, queue.ErrBinding) {
			t.Fatal("admitted unselected profile", link, err)
		}
	}
}

func TestQueueCommandCanonicalSessionID(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	path := filepath.Join(t.TempDir(), "request")
	f.must(t, "prepare", "--session", "ABCDEFAB-1234-4123-8123-ABCDEFABCDEF", "--output", path)
	saved, err := readQueueRequest(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Request.SessionID != "abcdefab-1234-4123-8123-abcdefabcdef" {
		t.Fatal(saved.Request.SessionID)
	}
	f.must(t, "enqueue", path)
	jobs, _ := f.open(t).Snapshot(t.Context())
	if len(jobs) != 1 || jobs[0].Events[0].Request.SessionID != saved.Request.SessionID {
		t.Fatal(jobs)
	}
}

func TestQueueRequestRequiresTypedIdentityAndMembers(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	path := filepath.Join(t.TempDir(), "request")
	f.must(t, "prepare", "--session", "s", "--output", path, "--unknown-identity")
	data, _ := os.ReadFile(path)
	var original map[string]any
	if err := json.Unmarshal(data, &original); err != nil {
		t.Fatal(err)
	}
	for _, which := range []string{"null-identity", "omitted-identity", "null-email", "omitted-email", "null-request", "null-flush", "null-version", "omitted-scope"} {
		var edited map[string]any
		json.Unmarshal(data, &edited)
		switch which {
		case "null-identity":
			edited["Identity"] = nil
		case "omitted-identity":
			delete(edited, "Identity")
		case "null-email":
			edited["Identity"].(map[string]any)["Email"] = nil
		case "omitted-email":
			delete(edited["Identity"].(map[string]any), "Email")
		case "null-request":
			edited["Request"] = nil
		case "null-flush":
			edited["Request"].(map[string]any)["Flush"] = nil
		case "null-version":
			edited["Version"] = nil
		case "omitted-scope":
			delete(edited, "Scope")
		}
		raw, _ := json.Marshal(edited)
		copy := filepath.Join(t.TempDir(), "request")
		os.WriteFile(copy, raw, 0600)
		if _, err := f.execute(t.Context(), "enqueue", copy); err == nil {
			t.Fatal("accepted", which)
		}
	}
	// Paths is explicitly optional: omission and null both mean no selected paths.
	delete(original["Request"].(map[string]any)["Flush"].(map[string]any), "Paths")
	raw, _ := json.Marshal(original)
	copy := filepath.Join(t.TempDir(), "request")
	os.WriteFile(copy, raw, 0600)
	f.must(t, "enqueue", copy)
}

func TestQueueCommandRuntimeExcludesAllDesktopProfiles(t *testing.T) {
	for _, link := range []string{"none", "profile", "data"} {
		t.Run(link, func(t *testing.T) {
			if link != "none" && runtime.GOOS == "windows" {
				t.Skip("symlink creation requires privileges")
			}
			f := newQueueFixture(t)
			store := filepath.Join(f.req.Machine.Home, ".clauderig", "desktop")
			profile := filepath.Join(store, "unselected")
			if err := os.MkdirAll(filepath.Join(profile, "data"), 0700); err != nil {
				t.Fatal(err)
			}
			dataRoot := filepath.Join(profile, "data")
			if link != "none" {
				target := t.TempDir()
				alias := profile
				if link == "data" {
					alias = dataRoot
				}
				if err := os.RemoveAll(alias); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, alias); err != nil {
					t.Fatal(err)
				}
				dataRoot = target
				if link == "profile" {
					dataRoot = filepath.Join(target, "data")
				}
				if err := os.MkdirAll(dataRoot, 0700); err != nil {
					t.Fatal(err)
				}
			}
			f.dir = filepath.Join(dataRoot, "claude-code-sessions", "runtime")
			if _, err := f.execute(t.Context(), "init"); !errors.Is(err, queue.ErrBinding) {
				t.Fatal("accepted runtime in unselected profile", err)
			}
			if _, err := os.Stat(f.dir); !os.IsNotExist(err) {
				t.Fatal("created unsafe runtime", err)
			}
			if _, err := f.execute(t.Context(), "status"); !errors.Is(err, queue.ErrBinding) {
				t.Fatal("reopened unsafe location", err)
			}
		})
	}
	// A previously private runtime becomes unsafe if a profile later targets it.
	if runtime.GOOS != "windows" {
		f := newQueueFixture(t)
		f.must(t, "init")
		before, err := os.ReadFile(filepath.Join(f.dir, "runtime.json"))
		if err != nil {
			t.Fatal(err)
		}
		profile := filepath.Join(f.req.Machine.Home, ".clauderig", "desktop", "later")
		if err := os.MkdirAll(profile, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(f.dir, filepath.Join(profile, "data")); err != nil {
			t.Fatal(err)
		}
		for _, verb := range []string{"init", "status"} {
			if _, err := f.execute(t.Context(), verb); !errors.Is(err, queue.ErrBinding) {
				t.Fatal("reopened newly exposed runtime", verb, err)
			}
		}
		after, err := os.ReadFile(filepath.Join(f.dir, "runtime.json"))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("modified refused runtime", err)
		}
	}
}

func TestQueueCommandCanonicalSessionBounds(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	path := filepath.Join(t.TempDir(), "request")
	// U+023A is two bytes but its lowercase U+2C65 is three bytes.
	oversized := strings.Repeat("Ⱥ", 2048)
	if _, err := f.execute(t.Context(), "prepare", "--session", oversized, "--output", path); err == nil {
		t.Fatal("accepted expanded canonical identifier")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("saved oversized request", err)
	}
	valid := strings.Repeat("Ⱥ", 1365) + "a" // Exactly 4096 bytes after lowercasing.
	f.must(t, "prepare", "--session", valid, "--output", path)
	f.must(t, "enqueue", path)
}

func TestQueueCommandCanonicalProducerUUIDs(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	const id = "abcdefab-1234-4123-8123-abcdefabcdef"
	var provenance string
	for _, spelling := range []string{id, strings.ToUpper(id), "  " + strings.ToUpper(id) + "  "} {
		f.identity.AccountUUID = spelling
		f.identity.OrganizationUUID = spelling
		path := filepath.Join(t.TempDir(), "request")
		f.must(t, "prepare", "--session", "s", "--output", path)
		saved, err := readQueueRequest(path)
		if err != nil {
			t.Fatal(err)
		}
		if saved.Identity.AccountUUID != id || saved.Identity.OrganizationUUID != id {
			t.Fatal("noncanonical saved identity", saved.Identity)
		}
		if provenance != "" && saved.Request.ProvenanceID != provenance {
			t.Fatal("different provenance for equivalent UUIDs")
		}
		provenance = saved.Request.ProvenanceID
		f.must(t, "enqueue", path)
	}
	for _, invalid := range []string{"not-a-uuid", "   "} {
		f.identity.OrganizationUUID = invalid
		if _, err := f.execute(t.Context(), "prepare", "--session", "s", "--output", filepath.Join(t.TempDir(), "request")); err == nil {
			t.Fatal("invalid organization became unknown")
		}
	}
}

func TestQueueCommandUnresolvedDesktopLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges")
	}
	for _, link := range []string{"store", "profile", "data"} {
		t.Run(link, func(t *testing.T) {
			f := newQueueFixture(t)
			f.must(t, "init")
			existingRuntime := f.dir
			before, err := os.ReadFile(filepath.Join(existingRuntime, "runtime.json"))
			if err != nil {
				t.Fatal(err)
			}
			store := filepath.Join(f.req.Machine.Home, ".clauderig", "desktop")
			alias := store
			if link == "profile" {
				alias = filepath.Join(store, "broken")
			}
			if link == "data" {
				alias = filepath.Join(store, "broken", "data")
			}
			if err := os.MkdirAll(filepath.Dir(alias), 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "missing")
			if err := os.Symlink(target, alias); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{target, filepath.Join(target, "nested")} {
				f.dir = dir
				if _, err := f.execute(t.Context(), "init"); !errors.Is(err, queue.ErrBinding) {
					t.Fatal("activated broken link", err)
				}
				if _, err := os.Lstat(target); !os.IsNotExist(err) {
					t.Fatal("created link target", err)
				}
			}
			f.dir = existingRuntime
			if _, err := f.execute(t.Context(), "status"); !errors.Is(err, queue.ErrBinding) || !strings.Contains(err.Error(), "repair profile paths or permissions") {
				t.Fatal("missing isolation diagnostic", err)
			}
			after, err := os.ReadFile(filepath.Join(existingRuntime, "runtime.json"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("changed refused runtime", err)
			}
			if err := os.Remove(alias); err != nil {
				t.Fatal(err)
			}
			f.must(t, "status") // Repair the local link, without resetting saved state.
		})
	}
}

func TestQueueCommandInvalidProfileStoreRetainsRuntime(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	store := filepath.Join(f.req.Machine.Home, ".clauderig", "desktop")
	if err := os.MkdirAll(filepath.Dir(store), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, []byte("invalid profile store"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.execute(t.Context(), "status"); !errors.Is(err, queue.ErrBinding) || !strings.Contains(err.Error(), "repair profile paths or permissions") {
		t.Fatal("missing isolation diagnostic", err)
	}
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	f.must(t, "status")
}
