package commands

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/durable"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/hooks"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
	"github.com/spf13/cobra"
)

func setupHookRouting(t *testing.T, f *queueCommandFixture) (string, string) {
	t.Helper()
	path := filepath.Join(f.req.Machine.Home, ".clauderig", "queue-hooks.json")
	settings := filepath.Join(f.req.Machine.Home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.Install(settings, hooks.SyncPlans()); err != nil {
		t.Fatal(err)
	}
	f.deps.routingPath = func() (string, error) { return path, nil }
	f.deps.hooksPath = func() (string, error) { return settings, nil }
	return path, settings
}

func routedSync(f *queueCommandFixture, ctx context.Context, payload string, args ...string) (string, string, error) {
	c := newSyncCmd(f.deps)
	c.SilenceErrors, c.SilenceUsage = true, true
	var out, diagnostic bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&diagnostic)
	c.SetIn(strings.NewReader(payload))
	c.SetArgs(args)
	err := c.ExecuteContext(ctx)
	return out.String(), diagnostic.String(), err
}

func TestQueueHookRoutingOptInAndRollback(t *testing.T) {
	f := newQueueFixture(t)
	path, settings := setupHookRouting(t, f)
	original, _ := os.ReadFile(settings)
	f.must(t, "init")
	f.must(t, "enable-hooks")
	f.must(t, "enable-hooks") // idempotent, no new event/identity
	if f.reads != 0 {
		t.Fatal("enable read live identity")
	}
	s, err := loadHookRouting(path)
	if err != nil || !s.Enabled {
		t.Fatal(s, err)
	}
	if _, err := f.execute(t.Context(), "enable-hooks", "--unknown-identity"); err == nil {
		t.Fatal("changed active attribution policy")
	}
	f.must(t, "disable-hooks")
	f.must(t, "disable-hooks") // reflush a potentially uncertain disabled write
	after, _ := os.ReadFile(settings)
	if !bytes.Equal(original, after) {
		t.Fatal("changed portable hook settings")
	}
	s, err = loadHookRouting(path)
	if err != nil || s.Enabled {
		t.Fatal(s, err)
	}
	in, err := (hookInbox{dir: s.Inbox}).load(s.Scope)
	if err != nil || len(in.Requests) != 0 {
		t.Fatal(in, err)
	}
	f.must(t, "enable-hooks", "--unknown-identity")
	f.deps.identity = func() (service.Identity, error) {
		t.Fatal("unknown mode read identity")
		return service.Identity{}, nil
	}
	source := filepath.Join(f.req.Machine.Home, ".claude", "projects", "-fixture", "s.jsonl")
	out, diagnostic, err := routedSync(f, t.Context(), queueHookJSON(t, "Stop", "s", source), "--hook")
	if err != nil || out != "" || !strings.Contains(diagnostic, "admission complete") {
		t.Fatal(out, diagnostic, err)
	}
	work, err := f.open(t).Snapshot(t.Context())
	if err != nil || len(work) != 1 || work[0].Events[0].Request.Flush.Mode != queue.Normal {
		t.Fatal(work, err)
	}
	if _, err := f.execute(t.Context(), "disable-hooks"); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatal("disabled pending queue", err)
	}
	s, err = loadHookRouting(path)
	if err != nil || !s.Enabled {
		t.Fatal("failed rollback changed routing", s, err)
	}
}

func TestQueueHookRoutingSessionEndAndInputRefusal(t *testing.T) {
	f := newQueueFixture(t)
	path, _ := setupHookRouting(t, f)
	f.must(t, "init")
	f.must(t, "enable-hooks")
	source := filepath.Join(f.req.Machine.Home, ".claude", "projects", "-fixture", "s.jsonl")
	for _, tc := range []struct{ payload, flag string }{
		{"", "--hook"}, {"\n", "--flush"}, {"{}", "--flush"},
		{queueHookJSON(t, "Stop", "s", source), "--flush"}, {queueHookJSON(t, "SessionEnd", "s", source), "--hook"},
	} {
		if _, _, err := routedSync(f, t.Context(), tc.payload, tc.flag); err == nil {
			t.Fatal("accepted", tc)
		}
	}
	if f.reads != 0 {
		t.Fatal("bad input observed identity")
	}
	out, diagnostic, err := routedSync(f, t.Context(), queueHookJSON(t, "SessionEnd", " S ", source), "--flush")
	if err != nil || out != "" || !strings.Contains(diagnostic, "admission complete") {
		t.Fatal(out, diagnostic, err)
	}
	work, err := f.open(t).Snapshot(t.Context())
	if err != nil || len(work) != 1 || work[0].Events[0].Request.Flush.Mode != queue.Selected || work[0].Events[0].Request.Flush.Paths[0] != source {
		t.Fatal(work, err)
	}
	// Config drift fails closed, retaining routing and work.
	f.req.Config.Remote = "https://github.com/acme/another.git"
	if _, _, err := routedSync(f, t.Context(), queueHookJSON(t, "Stop", "s", source), "--hook"); err == nil {
		t.Fatal("accepted changed configuration")
	}
	s, err := loadHookRouting(path)
	if err != nil || !s.Enabled {
		t.Fatal(s, err)
	}
}

func TestQueueHookRoutingMissingAndDisabledLeaveLegacyInputUntouched(t *testing.T) {
	f := newQueueFixture(t)
	path, _ := setupHookRouting(t, f)
	for _, disabled := range []bool{false, true} {
		if disabled {
			f.must(t, "init")
			f.must(t, "enable-hooks")
			f.must(t, "disable-hooks")
		}
		input := strings.NewReader("legacy payload")
		c := &cobra.Command{}
		c.SetContext(t.Context())
		c.SetIn(input)
		handled, err := routeQueuedSync(c, f.deps, false, true, false)
		if handled || err != nil || input.Len() != len("legacy payload") {
			t.Fatal(handled, err, input.Len())
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestQueueHookRoutingPreservesBlockedInboxAndWorker(t *testing.T) {
	f := newQueueFixture(t)
	path, _ := setupHookRouting(t, f)
	f.must(t, "init")
	f.must(t, "enable-hooks")
	s, err := loadHookRouting(path)
	if err != nil {
		t.Fatal(err)
	}
	in := hookInbox{dir: s.Inbox, save: durable.Write}
	request, err := newQueueSubmission(f.open(t), "s", queue.Flush{Mode: queue.Normal}, false, f.deps.identity)
	if err != nil {
		t.Fatal(err)
	}
	state := hookInboxState{Version: 1, Scope: s.Scope, Requests: []queueSubmission{request}}
	if err := in.persist(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(s.Inbox, "requests.json"))
	if _, err := f.execute(t.Context(), "disable-hooks"); err == nil || !strings.Contains(err.Error(), "inbox still") {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(s.Inbox, "requests.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("changed pending inbox")
	}
	state.Requests = []queueSubmission{}
	if err := in.persist(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	// Maintain refuses even an idle-but-owned worker. Find its shared queue root
	// from the runtime layout, without starting any subprocess or network access.
	_, release, err := storelock.Acquire(t.Context(), filepath.Join(f.dir, "queue", "worker"), 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.execute(t.Context(), "disable-hooks")
	release()
	if err == nil {
		t.Fatal("disabled while worker owned queue")
	}
	f.must(t, "disable-hooks")
}

func TestQueueHookRoutingCorruptionFailsClosed(t *testing.T) {
	for _, tc := range []string{"checksum", "alias", "symlink", "missing-inbox", "missing-journal", "public"} {
		t.Run(tc, func(t *testing.T) {
			if tc == "public" && runtime.GOOS == "windows" {
				t.Skip("Windows uses caller-provisioned private ACLs")
			}
			f := newQueueFixture(t)
			path, _ := setupHookRouting(t, f)
			f.must(t, "init")
			f.must(t, "enable-hooks")
			s, err := loadHookRouting(path)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			switch tc {
			case "checksum":
				data = bytes.Replace(data, []byte(`"Enabled":true`), []byte(`"Enabled":false`), 1)
				err = os.WriteFile(path, data, 0600)
			case "alias":
				data = bytes.Replace(data, []byte(`"Enabled"`), []byte(`"enabled"`), 1)
				err = os.WriteFile(path, data, 0600)
			case "symlink":
				target := filepath.Join(t.TempDir(), "state")
				if err = os.Rename(path, target); err == nil {
					err = os.Symlink(target, path)
				}
				if err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "missing-inbox":
				err = os.RemoveAll(s.Inbox)
			case "missing-journal":
				err = os.Remove(filepath.Join(s.Inbox, "requests.json"))
			case "public":
				err = os.Chmod(path, 0644)
			}
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(f.req.Machine.Home, ".claude", "projects", "-fixture", "s.jsonl")
			if _, _, err := routedSync(f, t.Context(), queueHookJSON(t, "Stop", "s", source), "--hook"); err == nil {
				t.Fatal("routing fell through corrupt state")
			}
			if f.reads != 0 {
				t.Fatal("corrupt routing read account")
			}
			if _, err := f.execute(t.Context(), "disable-hooks"); err == nil {
				t.Fatal("disabled corrupt state")
			}
		})
	}
}

func TestQueueHookRoutingManualContextAndAdmissionConcurrency(t *testing.T) {
	f := newQueueFixture(t)
	path, _ := setupHookRouting(t, f)
	f.must(t, "init")
	f.must(t, "enable-hooks")
	// Pause manual sync at its first privacy check, after routing but before worker
	// startup. A hook must still be able to acquire routing and enqueue meanwhile.
	entered := make(chan struct{})
	resume := make(chan struct{})
	f.deps.private = func(ctx context.Context, _ string) error {
		if d, ok := ctx.Deadline(); !ok || time.Until(d) < 30*time.Second {
			return errors.New("manual sync inherited a hook deadline")
		}
		close(entered)
		<-resume
		return errors.New("stop before network")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := routedSync(f, ctx, "", "--flush"); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	source := filepath.Join(f.req.Machine.Home, ".claude", "projects", "-fixture", "s.jsonl")
	_, _, err := routedSync(f, ctx, queueHookJSON(t, "Stop", "s", source), "--hook")
	close(resume)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "stop before network") {
		t.Fatal(err)
	}
	s, err := loadHookRouting(path)
	if err != nil || !s.Enabled {
		t.Fatal(s, err)
	}
}

func TestQueueHookRoutingSupervisedSyncAndRollback(t *testing.T) {
	f, runGit := newQueueSupervisedFixture(t)
	path, settings := setupHookRouting(t, f)
	portable, _ := os.ReadFile(settings)
	f.must(t, "init")
	f.must(t, "enable-hooks")
	source := filepath.Join(f.req.Machine.Home, ".claude", "projects", "-fixture", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("{\"type\":\"user\",\"sessionId\":\"s\",\"uuid\":\"routing\",\"message\":{\"role\":\"user\",\"content\":\"routed hook fixture\"}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := routedSync(f, t.Context(), queueHookJSON(t, "SessionEnd", "s", source), "--flush"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := routedSync(f, t.Context(), "", "--dry-run"); err != nil {
		t.Fatal(err)
	}
	work, err := f.open(t).Snapshot(t.Context())
	if err != nil || len(work) != 1 {
		t.Fatal("dry run acknowledged", work, err)
	}
	if _, _, err := routedSync(f, t.Context(), "", "--flush"); err != nil {
		t.Fatal(err)
	}
	work, err = f.open(t).Snapshot(t.Context())
	if err != nil || len(work) != 0 {
		t.Fatal(work, err)
	}
	if data := runGit("--git-dir", f.req.Config.Remote, "show", "main:cli/projects/-fixture/s.jsonl"); !strings.Contains(data, "routed hook fixture") {
		t.Fatal(data)
	}
	f.must(t, "disable-hooks")
	after, _ := os.ReadFile(settings)
	if !bytes.Equal(portable, after) {
		t.Fatal("edited installed hooks")
	}
	s, err := loadHookRouting(path)
	if err != nil || s.Enabled {
		t.Fatal(s, err)
	}
}

func TestQueueHookRoutingEnableRefusals(t *testing.T) {
	for _, tc := range []string{"missing-hooks", "incomplete-inbox", "source-inbox", "runtime-inbox", "foreign-inbox", "foreign-runtime", "disabled-cli", "extra-profile"} {
		t.Run(tc, func(t *testing.T) {
			f := newQueueFixture(t)
			path, settings := setupHookRouting(t, f)
			if tc == "disabled-cli" {
				f.req.Config.Roots[0].Enabled = false
			}
			if tc == "extra-profile" {
				f.profiles = []string{"missing"}
			}
			f.must(t, "init")
			inbox := filepath.Join(filepath.Dir(path), "hook-inbox")
			switch tc {
			case "missing-hooks":
				if err := os.Remove(settings); err != nil {
					t.Fatal(err)
				}
			case "incomplete-inbox":
				if err := os.Mkdir(inbox, 0700); err != nil {
					t.Fatal(err)
				}
			case "source-inbox":
				inbox = filepath.Join(f.req.Machine.Home, ".claude", "inbox")
			case "runtime-inbox":
				inbox = filepath.Join(f.dir, "inbox")
			case "foreign-inbox":
				if err := os.Mkdir(inbox, 0700); err != nil {
					t.Fatal(err)
				}
				if err := (hookInbox{dir: inbox, save: durable.Write}).persist(t.Context(), hookInboxState{Version: 1, Scope: "foreign", Requests: []queueSubmission{}}); err != nil {
					t.Fatal(err)
				}
			case "foreign-runtime":
				f.req.Config.Remote = "https://github.com/acme/another.git"
			}
			if _, err := f.execute(t.Context(), "enable-hooks", "--inbox", inbox); err == nil {
				t.Fatal("enabled", tc)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("wrote routing on failed enable", err)
			}
		})
	}
}
