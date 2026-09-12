package service_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func TestQueueRuntimeManualSyncPreservesLaterArrivalsAndOtherLifecycles(t *testing.T) {
	req, _, event, svc := queueSyncFixture(t)
	dir := filepath.Join(t.TempDir(), "runtime")
	r, err := service.CreateQueueRuntime(t.Context(), dir, req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	original, err := r.Enqueue(t.Context(), queueSyncIdentity, event, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateQueueRuntime(t.Context(), filepath.Join(t.TempDir(), "other"), req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Enqueue(t.Context(), queueSyncIdentity, event, time.Now()); err != nil {
		t.Fatal(err)
	}
	reads := 0
	svc.ReadIdentity = func() (service.Identity, error) { reads++; return queueSyncIdentity, nil }
	var late queue.Event
	svc.Observe = func(e service.Event) {
		if _, ok := e.(service.Captured); !ok {
			return
		}
		if _, release, err := storelock.Acquire(t.Context(), req.StagingDir, 0); err == nil {
			release()
			t.Fatal("staging lease released")
		}
		if err := r.RetryBlocked(t.Context(), original.BatchID); !errors.Is(err, storelock.ErrBusy) {
			t.Fatalf("worker not excluded: %v", err)
		}
		next := event
		next.EventID = "later"
		late, err = r.Enqueue(t.Context(), queueSyncIdentity, next, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := r.Sync(t.Context(), svc, req)
	if err != nil || reads != 1 {
		t.Fatalf("result=%+v err=%v reads=%d", result, err, reads)
	}
	reopened, err := service.OpenQueueRuntime(t.Context(), dir, req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	pending, err := reopened.Snapshot(t.Context())
	if err != nil || len(pending) != 1 || pending[0].ID != late.BatchID || len(pending[0].Events) != 2 {
		t.Fatalf("later arrival: %+v %v", pending, err)
	}
	pending, err = other.Snapshot(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Phase != queue.Queued {
		t.Fatalf("other lifecycle: %+v %v", pending, err)
	}
}

func TestQueueRuntimeManualSyncRefusesInvalidBindingBeforeCapture(t *testing.T) {
	for _, mode := range []string{"configuration", "metadata", "remote", "profiles"} {
		t.Run(mode, func(t *testing.T) {
			req, _, event, svc := queueSyncFixture(t)
			dir := filepath.Join(t.TempDir(), "runtime")
			profiles := engine.LocalProfileNames()
			if mode == "profiles" {
				profiles = append(profiles, "selected")
			}
			r, err := service.CreateQueueRuntime(t.Context(), dir, req, profiles)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Enqueue(t.Context(), queueSyncIdentity, event, time.Now()); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "configuration":
				req.Machine.Name += "-changed"
			case "metadata":
				if err := os.WriteFile(filepath.Join(dir, "runtime.json"), []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
			case "remote":
				req.Config.Remote += "-changed"
			}
			svc.ReadIdentity = func() (service.Identity, error) {
				t.Fatal("invalid runtime reached capture")
				return service.Identity{}, nil
			}
			result, err := r.Sync(t.Context(), svc, req)
			if err == nil {
				t.Fatalf("invalid runtime accepted: %+v %v", result, err)
			}
			pending, err := r.Snapshot(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Attempts != 0 {
				t.Fatalf("work mutated: %+v %v", pending, err)
			}
		})
	}
}

func TestQueueRuntimeManualSyncNeverAcknowledgesWork(t *testing.T) {
	for _, mode := range []string{"dry-run", "other-identity", "identity-error", "missing-source"} {
		t.Run(mode, func(t *testing.T) {
			req, _, event, svc := queueSyncFixture(t)
			r, err := service.CreateQueueRuntime(t.Context(), filepath.Join(t.TempDir(), "runtime"), req, engine.LocalProfileNames())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Enqueue(t.Context(), queueSyncIdentity, event, time.Now()); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "dry-run":
				req.DryRun = true
			case "other-identity":
				svc.ReadIdentity = func() (service.Identity, error) { return service.Identity{Email: "other@example.com"}, nil }
			case "identity-error":
				svc.ReadIdentity = func() (service.Identity, error) { return service.Identity{}, errors.New("identity unavailable") }
			case "missing-source":
				if err := os.Remove(filepath.Join(req.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl")); err != nil {
					t.Fatal(err)
				}
			}
			result, err := r.Sync(t.Context(), svc, req)
			if err != nil {
				t.Fatalf("unproven work: %+v %v", result, err)
			}
			pending, err := r.Snapshot(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Phase != queue.Queued || pending[0].Attempts != 0 {
				t.Fatalf("work mutated: %+v %v", pending, err)
			}
		})
	}
}

func TestQueueRuntimeManualSyncRevalidatesBeforePreparing(t *testing.T) {
	req, _, event, svc := queueSyncFixture(t)
	dir := filepath.Join(t.TempDir(), "runtime")
	r, err := service.CreateQueueRuntime(t.Context(), dir, req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue(t.Context(), queueSyncIdentity, event, time.Now()); err != nil {
		t.Fatal(err)
	}
	svc.ReadIdentity = func() (service.Identity, error) {
		if err := os.WriteFile(filepath.Join(dir, "runtime.json"), []byte("changed after preflight"), 0600); err != nil {
			t.Fatal(err)
		}
		return queueSyncIdentity, nil
	}
	result, err := r.Sync(t.Context(), svc, req)
	if err == nil {
		t.Fatalf("changed metadata accepted: %+v %v", result, err)
	}
	pending, err := r.Snapshot(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Attempts != 0 {
		t.Fatalf("work mutated: %+v %v", pending, err)
	}
}

func TestQueueRuntimeManualSyncRequiresCompleteProfileDiscovery(t *testing.T) {
	for _, mode := range []string{"missing", "malformed", "unreadable", "appears-during-capture"} {
		t.Run(mode, func(t *testing.T) {
			req, _, event, svc := queueSyncFixture(t)
			store := desktop.NewStore(filepath.Join(req.Machine.Home, ".clauderig", "desktop"))
			if _, err := store.Create("readable", "", ""); err != nil {
				t.Fatal(err)
			}
			profiles := engine.LocalProfileNames()
			if len(profiles) != 1 {
				t.Fatalf("readable profile missing: %v", profiles)
			}
			r, err := service.CreateQueueRuntime(t.Context(), filepath.Join(t.TempDir(), "runtime"), req, profiles)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Enqueue(t.Context(), queueSyncIdentity, event, time.Now()); err != nil {
				t.Fatal(err)
			}
			broken := filepath.Join(store.Root, "omitted")
			createBroken := func() {
				if err := os.MkdirAll(broken, 0700); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "malformed":
					if err := os.WriteFile(filepath.Join(broken, "profile.json"), []byte("not-json"), 0600); err != nil {
						t.Fatal(err)
					}
				case "unreadable":
					if err := os.Mkdir(filepath.Join(broken, "profile.json"), 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			reads := 0
			if mode != "appears-during-capture" {
				createBroken()
			}
			svc.ReadIdentity = func() (service.Identity, error) {
				reads++
				if mode == "appears-during-capture" {
					createBroken()
				}
				return queueSyncIdentity, nil
			}
			result, err := r.Sync(t.Context(), svc, req)
			if !errors.Is(err, queue.ErrBinding) || !strings.Contains(err.Error(), "complete Desktop profile coverage") {
				t.Fatalf("omitted profile accepted: %+v %v", result, err)
			}
			if mode != "appears-during-capture" && reads != 0 {
				t.Fatal("incomplete discovery reached capture")
			}
			pending, err := r.Snapshot(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Attempts != 0 {
				t.Fatalf("work mutated: %+v %v", pending, err)
			}
			// Restoring the original local profile set permits a retry in the same runtime.
			if err := os.RemoveAll(broken); err != nil {
				t.Fatal(err)
			}
			svc.ReadIdentity = func() (service.Identity, error) { return queueSyncIdentity, nil }
			result, err = r.Sync(t.Context(), svc, req)
			if err != nil {
				t.Fatalf("repaired profile retry: %+v %v", result, err)
			}
		})
	}
}

func TestQueueRuntimeManualSyncChecksLinkedProfileMetadata(t *testing.T) {
	req, _, event, svc := queueSyncFixture(t)
	r, err := service.CreateQueueRuntime(t.Context(), filepath.Join(t.TempDir(), "runtime"), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue(t.Context(), queueSyncIdentity, event, time.Now()); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	store := filepath.Join(req.Machine.Home, ".clauderig", "desktop")
	if err := os.MkdirAll(store, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(store, "linked")
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Fatalf("junction: %s %v", out, err)
		}
	} else if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	svc.ReadIdentity = func() (service.Identity, error) {
		t.Fatal("incomplete linked profile reached capture")
		return service.Identity{}, nil
	}
	_, err = r.Sync(t.Context(), svc, req)
	if !errors.Is(err, queue.ErrBinding) || !strings.Contains(err.Error(), "complete Desktop profile coverage") {
		t.Fatalf("linked profile metadata ignored: %v", err)
	}
	// Windows display discovery skips ModeIrregular junctions, even with valid
	// metadata. Manual sync must reject that omitted profile before capture.
	if runtime.GOOS == "windows" {
		if err := os.WriteFile(filepath.Join(target, "profile.json"), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		_, err = r.Sync(t.Context(), svc, req)
		if !errors.Is(err, queue.ErrBinding) || !strings.Contains(err.Error(), "manual capture omitted or changed") {
			t.Fatalf("junction omitted: %v", err)
		}
	}
	pending, err := r.Snapshot(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Attempts != 0 {
		t.Fatalf("work mutated: %+v %v", pending, err)
	}
}

func TestQueueRuntimeManualSyncRevalidationSurvivesIdentityFailureAndDryRun(t *testing.T) {
	for _, dry := range []bool{false, true} {
		for _, identityMode := range []string{"error", "invalid", "valid"} {
			for _, change := range []string{"runtime", "profile"} {
				t.Run(fmt.Sprintf("dry=%v/%s/%s", dry, identityMode, change), func(t *testing.T) {
					req, _, event, svc := queueSyncFixture(t)
					dir := filepath.Join(t.TempDir(), "runtime")
					r, err := service.CreateQueueRuntime(t.Context(), dir, req, nil)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := r.Enqueue(t.Context(), queueSyncIdentity, event, time.Now()); err != nil {
						t.Fatal(err)
					}
					req.DryRun = dry
					svc.ReadIdentity = func() (service.Identity, error) {
						if change == "runtime" {
							if err := os.WriteFile(filepath.Join(dir, "runtime.json"), []byte("changed after preflight"), 0600); err != nil {
								t.Fatal(err)
							}
						} else {
							if err := os.MkdirAll(filepath.Join(req.Machine.Home, ".clauderig", "desktop", "omitted"), 0700); err != nil {
								t.Fatal(err)
							}
						}
						switch identityMode {
						case "error":
							return service.Identity{}, errors.New("identity unavailable")
						case "invalid":
							return service.Identity{AccountUUID: "invalid"}, nil
						default:
							return queueSyncIdentity, nil
						}
					}
					svc.Observe = func(e service.Event) {
						if _, ok := e.(service.Captured); ok {
							t.Fatal("changed runtime reached captured state")
						}
					}
					result, err := r.Sync(t.Context(), svc, req)
					if !errors.Is(err, queue.ErrBinding) || result.Publication.Pushed {
						t.Fatalf("validation bypassed: %+v %v", result, err)
					}
					pending, err := r.Snapshot(t.Context())
					if err != nil || len(pending) != 1 || pending[0].Phase != queue.Queued || pending[0].Attempts != 0 {
						t.Fatalf("work mutated: %+v %v", pending, err)
					}
				})
			}
		}
	}
}

func TestQueueRuntimeManualSyncRevalidatesAfterCaptureAndPublication(t *testing.T) {
	for _, phase := range []string{"capture", "publication"} {
		for _, change := range []string{"profile", "runtime"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				req, _, event, svc := queueSyncFixture(t)
				dir := filepath.Join(t.TempDir(), "runtime")
				r, err := service.CreateQueueRuntime(t.Context(), dir, req, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := r.Enqueue(t.Context(), queueSyncIdentity, event, time.Now()); err != nil {
					t.Fatal(err)
				}
				changed := false
				svc.Observe = func(e service.Event) {
					_, captured := e.(service.Captured)
					_, published := e.(service.Published)
					if (phase == "capture" && !captured) || (phase == "publication" && !published) {
						return
					}
					changed = true
					if change == "runtime" {
						if err := os.WriteFile(filepath.Join(dir, "runtime.json"), []byte("changed"), 0600); err != nil {
							t.Fatal(err)
						}
					} else {
						store := desktop.NewStore(filepath.Join(req.Machine.Home, ".clauderig", "desktop"))
						if _, err := store.Create("arrived", "", ""); err != nil {
							t.Fatal(err)
						}
					}
				}
				result, err := r.Sync(t.Context(), svc, req)
				if !changed || !errors.Is(err, queue.ErrBinding) {
					t.Fatalf("stale capture accepted: %+v %v changed=%v", result, err, changed)
				}
				if phase == "capture" && result.Publication.Pushed {
					t.Fatal("published after changed capture policy")
				}
				if phase == "publication" && !result.Publication.Pushed {
					t.Fatal("test did not exercise published snapshot")
				}
				pending, err := r.Snapshot(t.Context())
				if err != nil || len(pending) != 1 || pending[0].Phase != queue.Queued || pending[0].Attempts != 0 {
					t.Fatalf("work mutated: %+v %v", pending, err)
				}
			})
		}
	}
}

func TestQueueRuntimeManualSyncRevalidatesAfterDryRunWalk(t *testing.T) {
	for _, change := range []string{"profile", "runtime"} {
		t.Run(change, func(t *testing.T) {
			req, _, event, svc := queueSyncFixture(t)
			dir := filepath.Join(t.TempDir(), "runtime")
			r, err := service.CreateQueueRuntime(t.Context(), dir, req, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Enqueue(t.Context(), queueSyncIdentity, event, time.Now()); err != nil {
				t.Fatal(err)
			}
			req.DryRun = true
			changed := false
			svc.Observe = func(e service.Event) {
				if _, ok := e.(service.DryRunStaged); !ok {
					return
				}
				changed = true
				if change == "runtime" {
					if err := os.WriteFile(filepath.Join(dir, "runtime.json"), []byte("changed"), 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.MkdirAll(filepath.Join(req.Machine.Home, ".clauderig", "desktop", "omitted"), 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			result, err := r.Sync(t.Context(), svc, req)
			if !changed || !errors.Is(err, queue.ErrBinding) || result.Publication.Pushed {
				t.Fatalf("dry-run validation bypassed: %+v %v changed=%v", result, err, changed)
			}
			pending, err := r.Snapshot(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Phase != queue.Queued || pending[0].Attempts != 0 {
				t.Fatalf("work mutated: %+v %v", pending, err)
			}
		})
	}
}

func TestQueueRuntimeManualSyncRefusesDivergentProcessHomeProfiles(t *testing.T) {
	req, _, event, svc := queueSyncFixture(t)
	store := desktop.NewStore(filepath.Join(req.Machine.Home, ".clauderig", "desktop"))
	if _, err := store.Create("source-profile", "", ""); err != nil {
		t.Fatal(err)
	}
	r, err := service.CreateQueueRuntime(t.Context(), filepath.Join(t.TempDir(), "runtime"), req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue(t.Context(), queueSyncIdentity, event, time.Now()); err != nil {
		t.Fatal(err)
	}
	otherHome := t.TempDir()
	t.Setenv("HOME", otherHome)
	t.Setenv("USERPROFILE", otherHome)
	svc.ReadIdentity = func() (service.Identity, error) {
		t.Fatal("divergent home reached capture")
		return service.Identity{}, nil
	}
	result, err := r.Sync(t.Context(), svc, req)
	if !errors.Is(err, queue.ErrBinding) {
		t.Fatalf("changed actual profile selection accepted: %+v %v", result, err)
	}
	pending, err := r.Snapshot(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Attempts != 0 {
		t.Fatalf("work mutated: %+v %v", pending, err)
	}
}
