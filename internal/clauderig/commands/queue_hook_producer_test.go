package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/durable"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
	"github.com/spf13/cobra"
)

func executeHookAdmission(f *queueCommandFixture, ctx context.Context, inbox, payload string, args ...string) (string, string, error) {
	cmd := newQueueCmd(f.deps)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out, diagnostic bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&diagnostic)
	cmd.SetIn(strings.NewReader(payload))
	base := []string{"--dir", f.dir}
	for _, profile := range f.profiles {
		base = append(base, "--profile", profile)
	}
	cmd.SetArgs(append(append(base, "hook", "--inbox", inbox), args...))
	err := cmd.ExecuteContext(ctx)
	return out.String(), diagnostic.String(), err
}

func TestQueueHookProducerAdmissionAndRecovery(t *testing.T) {
	for _, event := range []string{"Stop", "SessionEnd"} {
		t.Run(event, func(t *testing.T) {
			f := newQueueFixture(t)
			f.must(t, "init")
			inbox := filepath.Join(t.TempDir(), "inbox")
			source := filepath.Join(f.req.Machine.Home, ".claude", "projects", "p", "s.jsonl")
			checks := f.privateChecks
			out, diagnostic, err := executeHookAdmission(f, t.Context(), inbox, queueHookJSON(t, event, "s", source))
			if err != nil || out != "" || !strings.Contains(diagnostic, "1 requests confirmed") || f.reads != 1 || checks != f.privateChecks {
				t.Fatal(out, diagnostic, err, f.reads)
			}
			r := f.open(t)
			pending, err := r.Snapshot(t.Context())
			if err != nil || len(pending) != 1 || len(pending[0].Events) != 1 {
				t.Fatal(pending, err)
			}
			request := pending[0].Events[0].Request
			want := queue.Normal
			if event == "SessionEnd" {
				want = queue.Selected
			}
			if request.Flush.Mode != want || request.SessionID != "s" {
				t.Fatal(request)
			}
			saved, err := (hookInbox{dir: inbox}).load(r.ScopeID())
			if err != nil || len(saved.Requests) != 0 {
				t.Fatal(saved, err)
			}
			f.deps.identity = func() (service.Identity, error) {
				t.Fatal("recovery consulted live account")
				return service.Identity{}, nil
			}
			f.must(t, "recover-hooks", "--inbox", inbox)
			pending, err = r.Snapshot(t.Context())
			if err != nil || len(pending[0].Events) != 1 {
				t.Fatal(pending, err)
			}
		})
	}
}

func TestQueueHookInboxUncertainAdmissionAndRemoval(t *testing.T) {
	for _, failure := range []string{"full", "enqueue-uncertain", "save-before", "save-after", "clear-before", "clear-after", "cancel-after-enqueue"} {
		t.Run(failure, func(t *testing.T) {
			f := newQueueFixture(t)
			f.must(t, "init")
			r := f.open(t)
			s, err := newQueueSubmission(r, "s", queue.Flush{Mode: queue.Normal}, false, f.deps.identity)
			if err != nil {
				t.Fatal(err)
			}
			in := hookInbox{dir: filepath.Join(t.TempDir(), "inbox"), save: durable.Write}
			calls := 0
			in.save = func(ctx context.Context, path string, write func(*os.File) error) error {
				calls++
				fail := (calls == 1 && strings.HasPrefix(failure, "save-")) || (calls == 2 && strings.HasPrefix(failure, "clear-"))
				if fail && strings.HasSuffix(failure, "before") {
					return fmt.Errorf("injected write failure")
				}
				if err := durable.Write(ctx, path, write); err != nil {
					return err
				}
				if fail {
					return durable.ErrUncertain
				}
				return nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			enqueues := 0
			enqueue := func(ctx context.Context, identity service.Identity, req queue.Request, at time.Time) (queue.Event, error) {
				enqueues++
				if failure == "full" {
					return queue.Event{}, queue.ErrFull
				}
				accepted, err := r.Enqueue(ctx, identity, req, at)
				if err == nil && failure == "enqueue-uncertain" {
					err = queue.ErrUncertain
				}
				if failure == "cancel-after-enqueue" {
					cancel()
				}
				return accepted, err
			}
			if _, err := in.admit(ctx, r.ScopeID(), &s, enqueue); err == nil {
				t.Fatal("injection did not fail")
			}
			if strings.HasPrefix(failure, "save-") && enqueues != 0 {
				t.Fatal("enqueue before confirmed inbox save")
			}
			f.identity = service.Identity{Email: "later@example.com"}
			in.save = durable.Write
			if failure == "save-before" {
				if _, err := in.admit(t.Context(), r.ScopeID(), nil, r.Enqueue); err == nil {
					t.Fatal("recovery reset incomplete journal")
				}
				return
			}
			if _, err := in.admit(t.Context(), r.ScopeID(), nil, r.Enqueue); err != nil {
				t.Fatal(err)
			}
			pending, err := r.Snapshot(t.Context())
			if err != nil || len(pending) != 1 || len(pending[0].Events) != 1 {
				t.Fatal(pending, err)
			}
			event := pending[0].Events[0]
			if event.Request.EventID != s.Request.EventID || event.Request.ProvenanceID != s.Request.ProvenanceID || !event.EnqueuedAt.Equal(s.At) {
				t.Fatal("recovery changed original identity/event/time", event, s)
			}
			state, err := in.load(r.ScopeID())
			if err != nil || len(state.Requests) != 0 {
				t.Fatal(state, err)
			}
		})
	}
}

func TestQueueHookInboxConcurrentProducers(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	r := f.open(t)
	in := hookInbox{dir: filepath.Join(t.TempDir(), "inbox"), save: durable.Write}
	const n = 8
	requests := make([]queueSubmission, n)
	for i := range requests {
		var err error
		requests[i], err = newQueueSubmission(r, fmt.Sprintf("s%d", i), queue.Flush{Mode: queue.Normal}, false, f.deps.identity)
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := in.admit(t.Context(), r.ScopeID(), &requests[i], r.Enqueue)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	pending, err := r.Snapshot(t.Context())
	total := 0
	for _, work := range pending {
		total += len(work.Events)
	}
	if err != nil || total != n {
		t.Fatal(total, err)
	}
}

func TestQueueHookInboxRefusesCorruptOrForeignState(t *testing.T) {
	for _, mode := range []string{"missing", "checksum", "alias", "duplicate", "scope", "version", "hardlink", "symlink", "public"} {
		t.Run(mode, func(t *testing.T) {
			f := newQueueFixture(t)
			f.must(t, "init")
			r := f.open(t)
			in := hookInbox{dir: filepath.Join(t.TempDir(), "inbox"), save: durable.Write}
			s, err := newQueueSubmission(r, "s", queue.Flush{Mode: queue.Normal}, false, f.deps.identity)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := in.admit(t.Context(), r.ScopeID(), &s, func(context.Context, service.Identity, queue.Request, time.Time) (queue.Event, error) {
				return queue.Event{}, queue.ErrFull
			}); !errors.Is(err, queue.ErrFull) {
				t.Fatal(err)
			}
			path := filepath.Join(in.dir, "requests.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing":
				err = os.Remove(path)
			case "checksum":
				data = bytes.Replace(data, []byte(`"s"`), []byte(`"z"`), 1)
			case "alias":
				data = bytes.Replace(data, []byte(`"Version"`), []byte(`"version"`), 1)
			case "duplicate":
				data = bytes.Replace(data, []byte(`"Version":1`), []byte(`"Version":1,"Version":1`), 1)
			case "version":
				data = bytes.Replace(data, []byte(`"Version":1`), []byte(`"Version":2`), 1)
			case "scope": // The recovery runtime below uses a different binding.
			case "hardlink":
				err = os.Link(path, filepath.Join(t.TempDir(), "alias"))
			case "symlink":
				target := filepath.Join(t.TempDir(), "outside")
				if err = os.Rename(path, target); err == nil {
					err = os.Symlink(target, path)
				}
				if err != nil {
					t.Skip("symlinks unavailable", err)
				}
			case "public":
				if runtime.GOOS == "windows" {
					t.Skip("Windows privacy uses caller-provided ACLs")
				}
				err = os.Chmod(in.dir, 0755)
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "checksum" || mode == "alias" || mode == "duplicate" || mode == "version" {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			scope := r.ScopeID()
			if mode == "scope" {
				scope = "another-runtime"
			}
			if _, err := in.admit(t.Context(), scope, nil, func(context.Context, service.Identity, queue.Request, time.Time) (queue.Event, error) {
				t.Fatal("invalid state admitted")
				return queue.Event{}, nil
			}); err == nil {
				t.Fatal("accepted", mode)
			}
		})
	}
}

func TestQueueHookInboxCapacityPreservesSavedIntent(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	r := f.open(t)
	in := hookInbox{dir: filepath.Join(t.TempDir(), "inbox"), save: durable.Write}
	if err := os.Mkdir(in.dir, 0700); err != nil {
		t.Fatal(err)
	}
	state := hookInboxState{Version: 1, Scope: r.ScopeID(), Requests: []queueSubmission{}}
	for i := 0; i < hookInboxEvents; i++ {
		s, err := newQueueSubmission(r, "s", queue.Flush{Mode: queue.Normal}, false, f.deps.identity)
		if err != nil {
			t.Fatal(err)
		}
		state.Requests = append(state.Requests, s)
	}
	if err := in.persist(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(in.dir, "requests.json"))
	fresh, _ := newQueueSubmission(r, "s", queue.Flush{Mode: queue.Normal}, false, f.deps.identity)
	if _, err := in.admit(t.Context(), r.ScopeID(), &fresh, r.Enqueue); err == nil {
		t.Fatal("accepted overflow")
	}
	after, _ := os.ReadFile(filepath.Join(in.dir, "requests.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("overflow changed saved intent")
	}
}

func TestQueueHookInboxByteLimitPreservesSavedIntent(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	r := f.open(t)
	in := hookInbox{dir: filepath.Join(t.TempDir(), "inbox"), save: durable.Write}
	if err := os.Mkdir(in.dir, 0700); err != nil {
		t.Fatal(err)
	}
	state := hookInboxState{Version: 1, Scope: r.ScopeID(), Requests: []queueSubmission{}}
	if err := in.persist(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(in.dir, "requests.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < hookInboxEvents; i++ {
		session := strings.Repeat("s", 4000)
		request, err := newQueueSubmission(r, session, queue.Flush{Mode: queue.Selected, Paths: []string{"/projects/p/" + session + ".jsonl"}}, false, f.deps.identity)
		if err != nil {
			t.Fatal(err)
		}
		state.Requests = append(state.Requests, request)
	}
	if err := in.persist(t.Context(), state); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatal("byte limit not enforced", err)
	}
	after, err := os.ReadFile(filepath.Join(in.dir, "requests.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("byte overflow replaced journal", err)
	}
}

func TestQueueHookInboxSavesNewEventBehindBlockedBacklog(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	r := f.open(t)
	in := hookInbox{dir: filepath.Join(t.TempDir(), "inbox"), save: durable.Write}
	blocked := func(context.Context, service.Identity, queue.Request, time.Time) (queue.Event, error) {
		return queue.Event{}, queue.ErrFull
	}
	var original []queueSubmission
	for _, email := range []string{"first@example.com", "second@example.com"} {
		f.identity = service.Identity{Email: email}
		s, err := newQueueSubmission(r, "s", queue.Flush{Mode: queue.Normal}, false, f.deps.identity)
		if err != nil {
			t.Fatal(err)
		}
		original = append(original, s)
		if _, err := in.admit(t.Context(), r.ScopeID(), &s, blocked); !errors.Is(err, queue.ErrFull) {
			t.Fatal(err)
		}
	}
	index := 0
	if n, err := in.admit(t.Context(), r.ScopeID(), nil, func(ctx context.Context, identity service.Identity, request queue.Request, at time.Time) (queue.Event, error) {
		expected := original[index]
		index++
		if identity != expected.Identity || request.EventID != expected.Request.EventID || !at.Equal(expected.At) {
			t.Fatal("changed saved admission order or attribution")
		}
		return r.Enqueue(ctx, identity, request, at)
	}); err != nil || n != 2 || index != 2 {
		t.Fatal(n, index, err)
	}
}

func TestQueueHookProducerRefusesBadInputAndIsolation(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	root := filepath.Join(t.TempDir(), "inbox")
	if _, _, err := executeHookAdmission(f, t.Context(), root, "{}"); err == nil || f.reads != 0 {
		t.Fatal(err, f.reads)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("created inbox for invalid input", err)
	}
	source := filepath.Join(f.req.Machine.Home, ".claude", "projects", "p", "s.jsonl")
	for _, root := range []string{filepath.Join(f.dir, "inbox"), filepath.Join(f.req.StagingDir, "inbox"), filepath.Join(f.req.Machine.Home, ".claude", "inbox")} {
		if _, _, err := executeHookAdmission(f, t.Context(), root, queueHookJSON(t, "Stop", "s", source)); err == nil {
			t.Fatal("accepted nonprivate inbox", root)
		}
	}
	f.identity = service.Identity{}
	if _, _, err := executeHookAdmission(f, t.Context(), root, queueHookJSON(t, "Stop", "s", source)); err == nil {
		t.Fatal("silently accepted unknown identity")
	}
	if _, _, err := executeHookAdmission(f, t.Context(), root, queueHookJSON(t, "Stop", "s", source), "--unknown-identity"); err != nil {
		t.Fatal(err)
	}
}

func TestQueueHookProducerSupervisedDrain(t *testing.T) {
	f, runGit := newQueueSupervisedFixture(t)
	dir := filepath.Join(f.req.Machine.Home, ".claude", "projects", "-workspace-acme")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(source, []byte("{\"type\":\"user\",\"sessionId\":\"s\",\"uuid\":\"producer-message\",\"cwd\":\"/workspace/acme\",\"message\":{\"role\":\"user\",\"content\":\"admitted hook fixture\"}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.must(t, "init")
	inbox := filepath.Join(t.TempDir(), "inbox")
	if _, _, err := executeHookAdmission(f, t.Context(), inbox, queueHookJSON(t, "SessionEnd", "s", source)); err != nil {
		t.Fatal(err)
	}
	f.deps.identity = func() (service.Identity, error) { t.Fatal("worker reread identity"); return service.Identity{}, nil }
	f.must(t, "recover-hooks", "--inbox", inbox)
	f.must(t, "drain")
	data := runGit("--git-dir", f.req.Config.Remote, "show", "main:cli/projects/-workspace-acme/s.jsonl")
	if !strings.Contains(data, "admitted hook fixture") {
		t.Fatal("hook request not published")
	}
	pending, err := f.open(t).Snapshot(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
}

func TestQueueHookInboxPreservesExpiredAndLaterIntent(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	r := f.open(t)
	in := hookInbox{dir: filepath.Join(t.TempDir(), "inbox"), save: durable.Write}
	if err := os.Mkdir(in.dir, 0700); err != nil {
		t.Fatal(err)
	}
	state := hookInboxState{Version: 1, Scope: r.ScopeID(), Requests: []queueSubmission{}}
	cutoff := time.Now().UTC()
	for _, at := range []time.Time{cutoff.Add(-time.Minute), cutoff.Add(time.Minute)} {
		request, err := newQueueSubmission(r, "s", queue.Flush{Mode: queue.Normal}, false, f.deps.identity)
		if err != nil {
			t.Fatal(err)
		}
		request.At = at
		request.Checksum = queueRequestChecksum(request)
		state.Requests = append(state.Requests, request)
	}
	if err := in.persist(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(in.dir, "requests.json"))
	if err != nil {
		t.Fatal(err)
	}
	q, err := queue.Create(t.Context(), filepath.Join(t.TempDir(), "queue"), queue.Binding{Vendor: "claude", StoreID: "fixture", RootID: "fixture", RemoteID: "fixture", ConfigID: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CompactReceipts(t.Context(), cutoff); err != nil {
		t.Fatal(err)
	}
	calls := 0
	count, err := in.admit(t.Context(), r.ScopeID(), nil, func(ctx context.Context, _ service.Identity, request queue.Request, at time.Time) (queue.Event, error) {
		calls++
		return q.Enqueue(ctx, request, at)
	})
	if !errors.Is(err, queue.ErrExpired) || !strings.Contains(err.Error(), "explicit reconciliation") || count != 0 || calls != 1 {
		t.Fatal(count, calls, err)
	}
	after, err := os.ReadFile(filepath.Join(in.dir, "requests.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("expiry discarded saved intent", err)
	}
	pending, err := q.Snapshot(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
}

func TestQueueHookProducerAndRecoveryDeadlines(t *testing.T) {
	for _, recoverOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(recoverOnly), func(t *testing.T) {
			parent := &cobra.Command{Use: "queue", SilenceUsage: true, SilenceErrors: true}
			stop := errors.New("stop after observing operation context")
			caller, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			callerDeadline, _ := caller.Deadline()
			addQueueHookProducerCommands(parent, queueCommandDeps{}, func(ctx context.Context, _ bool) (*service.QueueRuntime, error) {
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("lost caller deadline")
				}
				if recoverOnly {
					if !deadline.Equal(callerDeadline) {
						t.Fatal("manual recovery unexpectedly capped", deadline)
					}
				} else if remaining := time.Until(deadline); remaining <= 0 || remaining > 10*time.Second {
					t.Fatal("fresh hook lacks bounded operation context", remaining)
				}
				return nil, stop
			})
			name := "hook"
			if recoverOnly {
				name = "recover-hooks"
			}
			parent.SetArgs([]string{name})
			input := queueHookJSON(t, "Stop", "s", "/fixture/projects/p/s.jsonl")
			if recoverOnly {
				input = "invalid input that recovery must not consume"
			}
			parent.SetIn(strings.NewReader(input))
			if err := parent.ExecuteContext(caller); !errors.Is(err, stop) {
				t.Fatal(err)
			}
		})
	}
}
