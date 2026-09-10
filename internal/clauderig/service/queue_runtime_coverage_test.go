package service_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

func TestQueueRuntimeManualCoveragePreservesLaterArrivalsAndOtherLifecycles(t *testing.T) {
	req, _, event, svc := coverageFixture(t)
	dir := filepath.Join(t.TempDir(), "runtime")
	r, err := service.CreateQueueRuntime(t.Context(), dir, req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	original, err := r.Enqueue(t.Context(), coverageIdentity, event, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateQueueRuntime(t.Context(), filepath.Join(t.TempDir(), "other"), req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Enqueue(t.Context(), coverageIdentity, event, time.Now()); err != nil {
		t.Fatal(err)
	}
	reads := 0
	svc.ReadIdentity = func() (service.Identity, error) { reads++; return coverageIdentity, nil }
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
		late, err = r.Enqueue(t.Context(), coverageIdentity, next, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := r.SyncWithCoverage(t.Context(), svc, req)
	if err != nil || !reflect.DeepEqual(result.Acknowledged, []uint64{original.Generation}) || reads != 1 {
		t.Fatalf("result=%+v err=%v reads=%d", result, err, reads)
	}
	reopened, err := service.OpenQueueRuntime(t.Context(), dir, req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	pending, err := reopened.Snapshot(t.Context())
	if err != nil || len(pending) != 1 || pending[0].ID != late.BatchID {
		t.Fatalf("later arrival: %+v %v", pending, err)
	}
	pending, err = other.Snapshot(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Phase != queue.Queued {
		t.Fatalf("other lifecycle: %+v %v", pending, err)
	}
}

func TestQueueRuntimeManualCoverageRefusesInvalidBindingBeforeCapture(t *testing.T) {
	for _, mode := range []string{"configuration", "metadata", "remote", "profiles"} {
		t.Run(mode, func(t *testing.T) {
			req, _, event, svc := coverageFixture(t)
			dir := filepath.Join(t.TempDir(), "runtime")
			profiles := engine.LocalProfileNames()
			if mode == "profiles" {
				profiles = append(profiles, "selected")
			}
			r, err := service.CreateQueueRuntime(t.Context(), dir, req, profiles)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Enqueue(t.Context(), coverageIdentity, event, time.Now()); err != nil {
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
			result, err := r.SyncWithCoverage(t.Context(), svc, req)
			if err == nil || len(result.Acknowledged) != 0 {
				t.Fatalf("invalid runtime accepted: %+v %v", result, err)
			}
			pending, err := r.Snapshot(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Attempts != 0 {
				t.Fatalf("work mutated: %+v %v", pending, err)
			}
		})
	}
}

func TestQueueRuntimeManualCoverageKeepsUnprovenWork(t *testing.T) {
	for _, mode := range []string{"dry-run", "other-identity", "identity-error", "missing-source"} {
		t.Run(mode, func(t *testing.T) {
			req, _, event, svc := coverageFixture(t)
			r, err := service.CreateQueueRuntime(t.Context(), filepath.Join(t.TempDir(), "runtime"), req, engine.LocalProfileNames())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Enqueue(t.Context(), coverageIdentity, event, time.Now()); err != nil {
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
			result, err := r.SyncWithCoverage(t.Context(), svc, req)
			if err != nil || len(result.Acknowledged) != 0 {
				t.Fatalf("unproven work: %+v %v", result, err)
			}
			pending, err := r.Snapshot(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Phase != queue.Queued || pending[0].Attempts != 0 {
				t.Fatalf("work mutated: %+v %v", pending, err)
			}
		})
	}
}

func TestQueueRuntimeManualCoverageRevalidatesBeforePreparing(t *testing.T) {
	req, _, event, svc := coverageFixture(t)
	dir := filepath.Join(t.TempDir(), "runtime")
	r, err := service.CreateQueueRuntime(t.Context(), dir, req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue(t.Context(), coverageIdentity, event, time.Now()); err != nil {
		t.Fatal(err)
	}
	svc.ReadIdentity = func() (service.Identity, error) {
		if err := os.WriteFile(filepath.Join(dir, "runtime.json"), []byte("changed after preflight"), 0600); err != nil {
			t.Fatal(err)
		}
		return coverageIdentity, nil
	}
	result, err := r.SyncWithCoverage(t.Context(), svc, req)
	if err == nil || len(result.Acknowledged) != 0 {
		t.Fatalf("changed metadata accepted: %+v %v", result, err)
	}
	pending, err := r.Snapshot(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Attempts != 0 {
		t.Fatalf("work mutated: %+v %v", pending, err)
	}
}

func TestQueueRuntimeManualCoverageRequiresCompleteProfileDiscovery(t *testing.T) {
	for _, mode := range []string{"missing", "malformed", "unreadable", "appears-during-capture"} {
		t.Run(mode, func(t *testing.T) {
			req, _, event, svc := coverageFixture(t)
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
			if _, err := r.Enqueue(t.Context(), coverageIdentity, event, time.Now()); err != nil {
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
				return coverageIdentity, nil
			}
			result, err := r.SyncWithCoverage(t.Context(), svc, req)
			if !errors.Is(err, queue.ErrBinding) || !strings.Contains(err.Error(), "complete Desktop profile coverage") || len(result.Acknowledged) != 0 {
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
			svc.ReadIdentity = func() (service.Identity, error) { return coverageIdentity, nil }
			result, err = r.SyncWithCoverage(t.Context(), svc, req)
			if err != nil || len(result.Acknowledged) != 1 {
				t.Fatalf("repaired profile retry: %+v %v", result, err)
			}
		})
	}
}

func TestQueueRuntimeManualCoverageChecksLinkedProfileMetadata(t *testing.T) {
	req, _, event, svc := coverageFixture(t)
	r, err := service.CreateQueueRuntime(t.Context(), filepath.Join(t.TempDir(), "runtime"), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue(t.Context(), coverageIdentity, event, time.Now()); err != nil {
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
	_, err = r.SyncWithCoverage(t.Context(), svc, req)
	if !errors.Is(err, queue.ErrBinding) || !strings.Contains(err.Error(), "complete Desktop profile coverage") {
		t.Fatalf("linked profile metadata ignored: %v", err)
	}
	// Windows display discovery skips ModeIrregular junctions, even with valid
	// metadata. The bridge must reject that omitted profile before acknowledging.
	if runtime.GOOS == "windows" {
		if err := os.WriteFile(filepath.Join(target, "profile.json"), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		_, err = r.SyncWithCoverage(t.Context(), svc, req)
		if !errors.Is(err, queue.ErrBinding) || !strings.Contains(err.Error(), "manual capture omitted or changed") {
			t.Fatalf("junction omitted: %v", err)
		}
	}
	pending, err := r.Snapshot(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Attempts != 0 {
		t.Fatalf("work mutated: %+v %v", pending, err)
	}
}

func TestQueueRuntimeManualCoverageRevalidationSurvivesIdentityFailureAndDryRun(t *testing.T) {
	for _, dry := range []bool{false, true} {
		for _, identityMode := range []string{"error", "invalid", "valid"} {
			for _, change := range []string{"runtime", "profile"} {
				t.Run(fmt.Sprintf("dry=%v/%s/%s", dry, identityMode, change), func(t *testing.T) {
					req, _, event, svc := coverageFixture(t)
					dir := filepath.Join(t.TempDir(), "runtime")
					r, err := service.CreateQueueRuntime(t.Context(), dir, req, nil)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := r.Enqueue(t.Context(), coverageIdentity, event, time.Now()); err != nil {
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
							return coverageIdentity, nil
						}
					}
					svc.Observe = func(e service.Event) {
						if _, ok := e.(service.Captured); ok {
							t.Fatal("changed runtime reached captured state")
						}
					}
					result, err := r.SyncWithCoverage(t.Context(), svc, req)
					if !errors.Is(err, queue.ErrBinding) || len(result.Acknowledged) != 0 || result.Sync.Publication.Pushed {
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

func TestQueueRuntimeManualCoverageRevalidatesAfterCaptureAndPublication(t *testing.T) {
	for _, phase := range []string{"capture", "publication"} {
		for _, change := range []string{"profile", "runtime"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				req, _, event, svc := coverageFixture(t)
				dir := filepath.Join(t.TempDir(), "runtime")
				r, err := service.CreateQueueRuntime(t.Context(), dir, req, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := r.Enqueue(t.Context(), coverageIdentity, event, time.Now()); err != nil {
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
				result, err := r.SyncWithCoverage(t.Context(), svc, req)
				if !changed || !errors.Is(err, queue.ErrBinding) || len(result.Acknowledged) != 0 {
					t.Fatalf("stale capture acknowledged: %+v %v changed=%v", result, err, changed)
				}
				if phase == "capture" && result.Sync.Publication.Pushed {
					t.Fatal("published after changed capture policy")
				}
				if phase == "publication" && !result.Sync.Publication.Pushed {
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
