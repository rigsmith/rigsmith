package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/durable"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

func runtimeFixture(t *testing.T) (*QueueRuntime, SyncRequest) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Roots = cfg.Roots[:1]
	cfg.Remote = "https://github.com/acme/private-backup.git"
	req := SyncRequest{Config: cfg, Machine: config.Machine{Name: "fixture", OS: config.OSToken(), Home: filepath.Join(root, "home")}, StagingDir: filepath.Join(root, "staging")}
	r, err := CreateQueueRuntime(t.Context(), filepath.Join(root, "runtime"), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r, req
}
func runtimeRequest(id string) queue.Request {
	return queue.Request{EventID: id, SessionID: "s", Flush: queue.Flush{Mode: queue.Normal}}
}

func TestQueueRuntimeIdentityDurabilityBeforeAcceptance(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed", true: "uncertain"}[uncertain], func(t *testing.T) {
			r, _ := runtimeFixture(t)
			r.save = func(ctx context.Context, path string, write func(*os.File) error) error {
				if uncertain {
					if err := durable.Write(ctx, path, write); err != nil {
						return err
					}
					return durable.ErrUncertain
				}
				return os.ErrPermission
			}
			identity := Identity{Email: "producer@example.com"}
			req := runtimeRequest("one")
			at := time.Now()
			if _, err := r.Enqueue(t.Context(), identity, req, at); err == nil {
				t.Fatal("save failure accepted")
			}
			jobs, err := r.q.Snapshot(t.Context())
			if err != nil || len(jobs) != 0 {
				t.Fatal(jobs, err)
			}
			writes := 0
			r.save = func(ctx context.Context, path string, write func(*os.File) error) error {
				writes++
				return durable.Write(ctx, path, write)
			}
			first, err := r.Enqueue(t.Context(), identity, req, at)
			if err != nil {
				t.Fatal(err)
			}
			again, err := r.Enqueue(t.Context(), identity, req, at)
			if err != nil || again.Generation != first.Generation || writes != 2 {
				t.Fatal(again, err, writes)
			}
			if _, err := r.Enqueue(t.Context(), Identity{Email: "other@example.com"}, req, at); !errors.Is(err, queue.ErrDuplicate) {
				t.Fatal(err)
			}
		})
	}
}

func TestQueueRuntimeRefusesResetAndCrossLifecycleState(t *testing.T) {
	for _, mode := range []string{"missing-queue", "foreign-queue", "missing-descriptor", "corrupt-descriptor", "linked-captures"} {
		t.Run(mode, func(t *testing.T) {
			r, req := runtimeFixture(t)
			qdir := filepath.Join(r.dir, "queue")
			switch mode {
			case "missing-queue":
				if err := os.Rename(qdir, qdir+"-retained"); err != nil {
					t.Fatal(err)
				}
			case "foreign-queue":
				other, _ := runtimeFixture(t)
				data, err := os.ReadFile(filepath.Join(other.dir, "queue", "queue.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(qdir, "queue.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-descriptor":
				if err := os.Remove(filepath.Join(r.dir, "runtime.json")); err != nil {
					t.Fatal(err)
				}
			case "corrupt-descriptor":
				if err := os.WriteFile(filepath.Join(r.dir, "runtime.json"), []byte("invalid"), 0600); err != nil {
					t.Fatal(err)
				}
			case "linked-captures":
				if err := os.Symlink(t.TempDir(), filepath.Join(r.dir, "captures")); err != nil {
					t.Skip(err)
				}
			}
			if _, err := OpenQueueRuntime(t.Context(), r.dir, req, nil); err == nil {
				t.Fatal("opened invalid lifecycle")
			}
			if _, err := CreateQueueRuntime(t.Context(), r.dir, req, nil); err == nil {
				t.Fatal("reset invalid lifecycle")
			}
			if _, err := r.Enqueue(t.Context(), Identity{}, runtimeRequest("refused"), time.Now()); err == nil {
				t.Fatal("enqueued invalid lifecycle")
			}
			if mode == "missing-queue" {
				if _, err := os.Stat(qdir); !os.IsNotExist(err) {
					t.Fatal("recreated missing queue", err)
				}
			}
		})
	}
}

func TestQueueRuntimeValidatesMetadataAndFreshResolution(t *testing.T) {
	r, req := runtimeFixture(t)
	if _, err := r.Enqueue(t.Context(), Identity{Email: "invalid"}, runtimeRequest("secret"), time.Now()); err == nil {
		t.Fatal("invalid identity")
	}
	if _, err := r.Enqueue(t.Context(), Identity{}, runtimeRequest("unknown"), time.Now()); err != nil {
		t.Fatal(err)
	}
	p, _ := CaptureProvenance(Identity{})
	reopened, err := OpenQueueRuntime(t.Context(), r.dir, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	in, err := reopened.inputs(t.Context(), QueueRuntimeInputs{Sync: req}, p)
	if err != nil || in.Identity != (Identity{}) || in.Captures.Dir != filepath.Join(r.dir, "captures") {
		t.Fatal(in, err)
	}
	if _, err := reopened.inputs(t.Context(), QueueRuntimeInputs{Sync: req}, "missing"); !errors.Is(err, queue.ErrBinding) {
		t.Fatal(err)
	}
	changed := req
	cfg := *req.Config
	cfg.Remote = "https://github.com/acme/other.git"
	changed.Config = &cfg
	if _, err := reopened.inputs(t.Context(), QueueRuntimeInputs{Sync: changed}, p); !errors.Is(err, queue.ErrBinding) {
		t.Fatal(err)
	}
	s, err := r.load()
	if err != nil {
		t.Fatal(err)
	}
	s.Identities[p] = Identity{Email: "wrong@example.com"}
	if err := r.persist(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenQueueRuntime(t.Context(), r.dir, req, nil); !errors.Is(err, queue.ErrBinding) {
		t.Fatal("accepted mismatched provenance", err)
	}
}

func TestQueueRuntimeConcurrentProducersAndLocationBinding(t *testing.T) {
	r, req := runtimeFixture(t)
	other, err := OpenQueueRuntime(t.Context(), r.dir, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := string(rune('a' + i))
			producer := r
			if i%2 == 0 {
				producer = other
			}
			if _, err := producer.Enqueue(t.Context(), Identity{Email: id + "@example.com"}, runtimeRequest(id), time.Now()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	jobs, err := r.q.Snapshot(t.Context())
	if err != nil || len(jobs) != 12 {
		t.Fatal(len(jobs), err)
	}
	s, err := r.load()
	if err != nil || len(s.Identities) != 12 {
		t.Fatal(len(s.Identities), err)
	}
	moved := r.dir + "-moved"
	if err := os.Rename(r.dir, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenQueueRuntime(t.Context(), moved, req, nil); !errors.Is(err, queue.ErrBinding) {
		t.Fatal("accepted transplanted lifecycle", err)
	}
}

func TestQueueRuntimeInitializationAndIdentityCapacity(t *testing.T) {
	r, req := runtimeFixture(t)
	for _, dir := range []string{req.StagingDir, filepath.Join(req.Machine.Home, ".claude", "runtime")} {
		if _, err := CreateQueueRuntime(t.Context(), dir, req, nil); !errors.Is(err, queue.ErrBinding) {
			t.Fatal(dir, err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("created overlapping root", err)
		}
	}
	missing := filepath.Join(filepath.Dir(r.dir), "missing", "runtime")
	if _, err := OpenQueueRuntime(t.Context(), missing, req, nil); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatal("open created parent", err)
	}
	empty := filepath.Join(filepath.Dir(r.dir), "incomplete")
	if err := os.Mkdir(empty, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateQueueRuntime(t.Context(), empty, req, nil); err == nil {
		t.Fatal("repaired incomplete root")
	}
	s, err := r.load()
	if err != nil {
		t.Fatal(err)
	}
	for i := range runtimeIdentityLimit {
		identity := Identity{Email: fmt.Sprintf("person%d@example.com", i)}
		p, _ := CaptureProvenance(identity)
		s.Identities[p] = identity
	}
	if err := r.persist(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue(t.Context(), Identity{}, runtimeRequest("full"), time.Now()); !errors.Is(err, queue.ErrFull) {
		t.Fatal(err)
	}
	existing := Identity{Email: "person0@example.com"}
	if _, err := r.Enqueue(t.Context(), existing, runtimeRequest("existing"), time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestQueueRuntimeCreationPersistenceFailures(t *testing.T) {
	for _, mode := range []string{"before-write", "uncertain-complete", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			original, req := runtimeFixture(t)
			dir := filepath.Join(filepath.Dir(original.dir), "new-runtime")
			r, err := prepareQueueRuntime(dir, req, nil)
			if err != nil {
				t.Fatal(err)
			}
			r.capture, err = CaptureBinding(req, nil)
			if err != nil {
				t.Fatal(err)
			}
			r.save = func(ctx context.Context, path string, write func(*os.File) error) error {
				switch mode {
				case "before-write":
					return os.ErrPermission
				case "truncated":
					if err := os.WriteFile(path, []byte(`{"Payload":`), 0600); err != nil {
						return err
					}
					return durable.ErrUncertain
				default:
					if err := durable.Write(ctx, path, write); err != nil {
						return err
					}
					return durable.ErrUncertain
				}
			}
			if _, err := createQueueRuntime(t.Context(), r); err == nil {
				t.Fatal("expected persistence failure")
			}
			queuePath := filepath.Join(dir, "queue", "queue.json")
			before, err := os.ReadFile(queuePath)
			if err != nil {
				t.Fatal(err)
			}
			opened, oerr := OpenQueueRuntime(t.Context(), dir, req, nil)
			retried, cerr := CreateQueueRuntime(t.Context(), dir, req, nil)
			if mode == "uncertain-complete" {
				if oerr != nil || cerr != nil || opened.id != r.id || retried.id != r.id {
					t.Fatal(oerr, cerr)
				}
			} else if oerr == nil || cerr == nil {
				t.Fatal("repaired incomplete descriptor", oerr, cerr)
			}
			after, err := os.ReadFile(queuePath)
			if err != nil || string(before) != string(after) {
				t.Fatal("changed queue during reopen", err)
			}
		})
	}
}

func TestQueueRuntimeDescriptorUpdateReopen(t *testing.T) {
	for _, truncate := range []bool{false, true} {
		t.Run(fmt.Sprint(truncate), func(t *testing.T) {
			r, req := runtimeFixture(t)
			r.save = func(ctx context.Context, path string, write func(*os.File) error) error {
				if truncate {
					if err := os.WriteFile(path, []byte(`{"Payload":`), 0600); err != nil {
						return err
					}
				} else if err := durable.Write(ctx, path, write); err != nil {
					return err
				}
				return durable.ErrUncertain
			}
			request := runtimeRequest("failed-update")
			at := time.Now()
			if _, err := r.Enqueue(t.Context(), Identity{}, request, at); !errors.Is(err, durable.ErrUncertain) {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(r.dir, "queue", "queue.json"))
			if err != nil {
				t.Fatal(err)
			}
			opened, oerr := OpenQueueRuntime(t.Context(), r.dir, req, nil)
			_, cerr := CreateQueueRuntime(t.Context(), r.dir, req, nil)
			if truncate {
				if oerr == nil || cerr == nil {
					t.Fatal("opened truncation")
				}
			} else {
				if oerr != nil || cerr != nil {
					t.Fatal(oerr, cerr)
				}
				jobs, err := opened.Snapshot(t.Context())
				if err != nil || len(jobs) != 0 {
					t.Fatal(jobs, err)
				}
			}
			after, err := os.ReadFile(filepath.Join(r.dir, "queue", "queue.json"))
			if err != nil || string(before) != string(after) {
				t.Fatal("accepted work or reset queue", err)
			}
		})
	}
}

func TestQueueRuntimeDuplicateDoesNotConsumeIdentityCapacity(t *testing.T) {
	r, _ := runtimeFixture(t)
	request := runtimeRequest("accepted")
	at := time.Now()
	if _, err := r.Enqueue(t.Context(), Identity{}, request, at); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		if _, err := r.Enqueue(t.Context(), Identity{Email: fmt.Sprintf("rejected%d@example.com", i)}, request, at); !errors.Is(err, queue.ErrDuplicate) {
			t.Fatal(err)
		}
	}
	s, err := r.load()
	if err != nil || len(s.Identities) != 1 {
		t.Fatal(len(s.Identities), err)
	}
}
