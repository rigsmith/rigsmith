package storelock

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestRecoveryEvidenceTransitionAndStaleCompletion(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	ctx, release := take(t, t.Context(), dir)
	fence, err := BeginRecoverableFence(ctx, []byte("prepared"))
	if err != nil {
		t.Fatal(err)
	}
	stale := &Fence{file: fence.file, token: bytes.Clone(fence.token)}
	if err := fence.SetRecoveryEvidence([]byte("owned group")); err != nil {
		t.Fatal(err)
	}
	if err := stale.Clear(); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale phase cleared fence: %v", err)
	}
	got, err := InheritedFence(fence.file)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := got.RecoveryEvidence()
	if err != nil || string(evidence) != "owned group" {
		t.Fatalf("lost evidence: %q %v", evidence, err)
	}
	release()
	recovered, err := RecoverFence(t.Context(), dir, func(evidence []byte) error {
		if string(evidence) != "owned group" {
			t.Fatalf("wrong recovery phase: %q", evidence)
		}
		if _, rel, err := Acquire(t.Context(), dir, 0); !errors.Is(err, ErrBusy) {
			if rel != nil {
				rel()
			}
			t.Fatalf("recovery did not retain exclusive ownership: %v", err)
		}
		return nil
	})
	if err != nil || !recovered {
		t.Fatalf("recovery: %v %v", recovered, err)
	}
	_, next := take(t, t.Context(), dir)
	next()
	if changed, err := RecoverFence(t.Context(), dir, func([]byte) error { t.Fatal("verified empty fence"); return nil }); err != nil || changed {
		t.Fatalf("clean recovery: %v %v", changed, err)
	}
}

func TestRecoveryRefusesBusyUnprovedAndCanceledRecords(t *testing.T) {
	for _, mode := range []string{"busy", "unproved", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "store")
			held, release := take(t, t.Context(), dir)
			_, err := BeginRecoverableFence(held, []byte("owned"))
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if mode != "busy" {
				release()
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			proofErr := errors.New("writers still active")
			changed, err := RecoverFence(ctx, dir, func([]byte) error {
				switch mode {
				case "busy":
					t.Fatal("inspected live owner's fence")
				case "unproved":
					return proofErr
				case "canceled":
					cancel()
				}
				return nil
			})
			if changed || err == nil {
				t.Fatalf("unsafe recovery succeeded: %v %v", changed, err)
			}
			if mode == "unproved" && !errors.Is(err, proofErr) {
				t.Fatal(err)
			}
			release()
			requireFenced(t, t.Context(), dir)
		})
	}
}

func TestRecoveryRefusesChangedRecord(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	ctx, release := take(t, t.Context(), dir)
	defer release()
	fence, err := BeginRecoverableFence(ctx, []byte("owned"))
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the transaction with the already-locked handle. Windows locks
	// forbid writes from a separately opened handle, even in the same process.
	// Never replace or unlock the inode while injecting this record change.
	next := recoveryRecord(make([]byte, 32), []byte("new command"))
	changed, err := recoverLockedFence(t.Context(), fence.file, func([]byte) error {
		_, err := fence.file.WriteAt(next, 0)
		return err
	})
	if changed || !errors.Is(err, ErrFenced) {
		t.Fatalf("cleared changed record: %v %v", changed, err)
	}
	got, err := readFence(fence.file)
	if err != nil || !bytes.Equal(got, next) {
		t.Fatalf("changed replacement record: %v", err)
	}
	release()
	requireFenced(t, t.Context(), dir)
}

func TestRecoveryRefusesLegacyDamagedOrAbsentEvidence(t *testing.T) {
	for _, mode := range []string{"legacy", "checksum", "truncated", "length", "missing"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "missing-parent", "store")
			var path string
			if mode != "missing" {
				ctx, release := take(t, t.Context(), dir)
				fence, err := BeginFence(ctx)
				if err != nil {
					t.Fatal(err)
				}
				path = fence.file.Name()
				if mode != "legacy" {
					data := recoveryRecord(make([]byte, 32), []byte("owned"))
					if mode == "checksum" {
						data[len(data)-1] ^= 1
					} else if mode == "length" {
						binary.BigEndian.PutUint32(data[len(recoveryPrefix)+32:], ^uint32(0))
						sum := sha256.Sum256(data[:len(data)-sha256.Size])
						copy(data[len(data)-sha256.Size:], sum[:])
					} else {
						data = data[:len(data)-1]
					}
					if _, err := fence.file.WriteAt(data, 0); err != nil {
						t.Fatal(err)
					}
				}
				release()
			}
			before, _ := os.ReadFile(path)
			changed, err := RecoverFence(t.Context(), dir, func([]byte) error { t.Fatal("invalid evidence reached verifier"); return nil })
			if changed || err == nil {
				t.Fatalf("invalid recovery: %v %v", changed, err)
			}
			if mode == "missing" {
				if _, err := os.Stat(filepath.Dir(dir)); !os.IsNotExist(err) {
					t.Fatalf("created recovery path: %v", err)
				}
			} else {
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("changed invalid record", err)
				}
			}
		})
	}
}

func TestRecoveryRefusesSubstitutedLockPath(t *testing.T) {
	for _, mode := range []string{"symlink", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "store")
			ctx, release := take(t, t.Context(), dir)
			defer release()
			fence, err := BeginRecoverableFence(ctx, []byte("owned"))
			if err != nil {
				t.Fatal(err)
			}
			path := fence.file.Name()
			before := bytes.Clone(fence.token)
			release()
			moved := path + ".moved"
			movedCreated := false
			if mode == "symlink" {
				if err := os.Rename(path, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				movedCreated = true
			}
			changed, err := RecoverFence(t.Context(), dir, func([]byte) error {
				if mode == "symlink" {
					t.Fatal("followed substituted lock symlink")
				}
				// Inject pathname replacement during verification. Neither the
				// retained handle nor this new inode may be cleared afterward.
				if err := os.Rename(path, moved); err != nil {
					// Windows may prevent replacement of the open lock itself.
					// Exercise refusal and preservation in that stronger OS case.
					if runtime.GOOS == "windows" && (os.IsPermission(err) || errors.Is(err, syscall.Errno(32))) { // ERROR_SHARING_VIOLATION
						return err
					}
					t.Fatal(err)
				}
				movedCreated = true
				if err := os.WriteFile(path, before, 0600); err != nil {
					t.Fatal(err)
				}
				return nil
			})
			if changed || !errors.Is(err, ErrFenced) {
				t.Fatalf("recovered substituted lock: %v %v", changed, err)
			}
			paths := []string{path}
			if movedCreated {
				paths = append(paths, moved)
			}
			for _, name := range paths {
				got, err := os.ReadFile(name)
				if err != nil || !bytes.Equal(got, before) {
					t.Fatalf("modified substituted record %s: %v", name, err)
				}
			}
		})
	}
}

func TestRecoveryTransitionPersistenceFailures(t *testing.T) {
	for _, phase := range []string{"owned", "stopped"} {
		for _, failure := range []string{"write", "partial", "sync"} {
			t.Run(phase+"/"+failure, func(t *testing.T) {
				dir := filepath.Join(t.TempDir(), "store")
				ctx, release := take(t, t.Context(), dir)
				defer release()
				previous := "prepared"
				if phase == "stopped" {
					previous = "owned"
				}
				fence, err := BeginRecoverableFence(ctx, []byte(previous))
				if err != nil {
					t.Fatal(err)
				}
				original := bytes.Clone(fence.token)
				fault := errors.New("injected persistence error")
				err = fence.setRecoveryEvidence([]byte(phase), func(next []byte) error {
					if failure == "write" {
						return fault
					}
					data := next
					if failure == "partial" {
						data = data[:len(data)/2]
					}
					if _, err := fence.file.WriteAt(data, 0); err != nil {
						t.Fatal(err)
					}
					// For sync failure, a complete new frame is visible even
					// though durability could not be confirmed to the caller.
					return fault
				})
				if !errors.Is(err, fault) || !bytes.Equal(fence.token, original) {
					t.Fatalf("failed transition advanced completion token: %v", err)
				}
				info, err := fence.file.Stat()
				if err != nil || info.Size() != int64(recoverySize) {
					t.Fatal("transition changed record size", err)
				}
				if failure != "write" {
					if err := fence.Clear(); !errors.Is(err, ErrFenced) {
						t.Fatal("stale token cleared failed transition", err)
					}
				}
				release()
				verified := false
				changed, err := RecoverFence(t.Context(), dir, func(data []byte) error {
					verified = true
					if string(data) == "prepared" || string(data) == "stopped" {
						return nil
					}
					return errors.New("owned phase has no cleanup proof")
				})
				want := failure == "write" && previous == "prepared" || failure == "sync" && phase == "stopped"
				if changed != want || (err == nil) != want || (failure == "partial" && verified) {
					t.Fatalf("unsafe recovery after %s/%s: %v %v (verified %v)", phase, failure, changed, err, verified)
				}
			})
		}
	}
}
