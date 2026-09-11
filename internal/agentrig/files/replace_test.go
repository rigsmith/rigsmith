package files

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func replacementFixture(t *testing.T) (string, *Source, *Replacements) {
	t.Helper()
	root := t.TempDir()
	s, err := OpenSource(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	b, err := BeginReplace(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return root, s, b
}

func TestReplacementsStageThenInstallAndRelease(t *testing.T) {
	root, s, b := replacementFixture(t)
	old := []byte("old private bytes")
	if err := os.WriteFile(filepath.Join(root, "existing"), old, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"existing", "new"} {
		expected := ExpectedFile{}
		if name == "existing" {
			expected = ExpectedFile{Exists: true, SHA256: sha256.Sum256(old)}
		}
		if err := b.Stage(t.Context(), name, expected, []byte("new private bytes"), 100); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(filepath.Join(root, "existing"))
	if err != nil || string(got) != string(old) {
		t.Fatal("staging changed destination", err)
	}
	if _, err := os.Stat(filepath.Join(root, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("staging created destination")
	}
	if other, err := BeginReplace(t.Context(), s); !errors.Is(err, ErrReplacementBusy) || other != nil {
		t.Fatal("competing writer entered", err)
	}
	for _, name := range []string{"existing", "new"} {
		if err := b.Apply(t.Context(), name); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(got) != "new private bytes" {
			t.Fatal("wrong installed bytes", err)
		}
		st, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
			t.Fatal("replacement not private", st.Mode())
		}
		if err := b.Apply(t.Context(), name); !errors.Is(err, ErrReplacementState) {
			t.Fatal("entry reused", err)
		}
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), replacementPrefix) {
			t.Fatal("scratch leaked")
		}
	}
	if _, err := os.Stat(filepath.Join(root, replacementLock)); err != nil {
		t.Fatal("fixed lock removed", err)
	}
	next, err := BeginReplace(t.Context(), s)
	if err != nil {
		t.Fatal("lock not released", err)
	}
	next.Close()
	if err := b.Stage(t.Context(), "later", ExpectedFile{}, nil, 100); !errors.Is(err, ErrReplacementState) {
		t.Fatal("closed batch accepted")
	}
}

func TestReplacementRejectsObservedChanges(t *testing.T) {
	for _, change := range []string{"edit", "same-size edit", "replace object", "mode", "new arrival", "temp bytes", "temp object", "cancel"} {
		t.Run(change, func(t *testing.T) {
			root, _, b := replacementFixture(t)
			name := "target"
			path := filepath.Join(root, name)
			expected := ExpectedFile{}
			if change != "new arrival" {
				if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
				expected = ExpectedFile{Exists: true, SHA256: sha256.Sum256([]byte("old"))}
			}
			if err := b.Stage(t.Context(), name, expected, []byte("new"), 100); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch change {
			case "edit", "new arrival":
				if err := os.WriteFile(path, []byte("concurrent"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "same-size edit":
				st, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("OLD"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, st.ModTime(), st.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "replace object":
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(path, 0o400); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(path, 0o600) })
			case "temp bytes":
				tmp := filepath.Join(root, b.entries[name].temp)
				stamp := b.entries[name].staged.ModTime()
				if err := os.WriteFile(tmp, []byte("bad"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(tmp, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			case "temp object":
				tmp := filepath.Join(root, b.entries[name].temp)
				if err := os.Rename(tmp, tmp+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.Apply(ctx, name); err == nil {
				t.Fatal("change ignored")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatal("destination changed on refusal", err)
			}
			if change == "temp object" {
				if err := b.Close(); !errors.Is(err, ErrReplacementCleanup) {
					t.Fatal("foreign scratch deleted", err)
				}
			}
		})
	}
}

func TestReplacementInputAndScratchCleanup(t *testing.T) {
	root, _, b := replacementFixture(t)
	for _, name := range []string{"../escape", "/absolute", "nested/name", ".", replacementLock, replacementPrefix + "reserved"} {
		if err := b.Stage(t.Context(), name, ExpectedFile{}, []byte("private"), 100); !errors.Is(err, ErrReplacementState) {
			t.Fatal("invalid target accepted", name, err)
		}
	}
	if err := b.Stage(t.Context(), "large", ExpectedFile{}, []byte("too large"), 2); !errors.Is(err, ErrSourceLimit) {
		t.Fatal(err)
	}
	if err := b.Stage(t.Context(), "empty", ExpectedFile{}, nil, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "empty")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unapplied file installed")
	}
}

func TestReplacementRejectsReservedCaseAliases(t *testing.T) {
	root, s, b := replacementFixture(t)
	before, err := os.Stat(filepath.Join(root, replacementLock))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		strings.ToUpper(replacementLock), ".AgentRig-Replace.Lock",
		strings.ToUpper(replacementPrefix) + "scratch", ".AgentRig-Replace-scratch",
	} {
		// Supply the lock's actual empty content so a case-insensitive alias
		// cannot pass staging by matching the existing lock's content.
		expected := ExpectedFile{Exists: true, SHA256: sha256.Sum256(nil)}
		if err := b.Stage(t.Context(), name, expected, []byte("replacement"), 100); !errors.Is(err, ErrReplacementState) {
			t.Fatalf("reserved alias %q was not rejected: %v", name, err)
		}
	}
	after, err := os.Stat(filepath.Join(root, replacementLock))
	if err != nil || !os.SameFile(before, after) || after.Size() != 0 {
		t.Fatal("reserved alias changed the lock", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != replacementLock {
		t.Fatal("reserved alias created scratch", err)
	}
	other, err := BeginReplace(t.Context(), s)
	if other != nil {
		other.Close()
	}
	if !errors.Is(err, ErrReplacementBusy) {
		t.Fatal("reserved alias damaged writer exclusion", err)
	}
}

func TestReplacementRejectsLinksAndLockSubstitution(t *testing.T) {
	t.Run("target link", func(t *testing.T) {
		root, _, b := replacementFixture(t)
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "target")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := b.Stage(t.Context(), "target", ExpectedFile{Exists: true, SHA256: sha256.Sum256([]byte("old"))}, []byte("new"), 100); !errors.Is(err, ErrSource) {
			t.Fatal("link followed", err)
		}
	})
	t.Run("lock link", func(t *testing.T) {
		root := t.TempDir()
		s, err := OpenSource(t.Context(), root)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, replacementLock)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if b, err := BeginReplace(t.Context(), s); b != nil || !errors.Is(err, ErrSource) {
			t.Fatal("lock link followed", err)
		}
	})
	t.Run("lock substitution", func(t *testing.T) {
		root, _, b := replacementFixture(t)
		// The source reader opens existing locks with delete sharing on Windows;
		// creation handles may prevent this move, which is itself protective.
		if err := os.Rename(filepath.Join(root, replacementLock), filepath.Join(root, "old-lock")); err != nil {
			t.Skipf("OS prevents moving held lock: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, replacementLock), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := b.Stage(t.Context(), "target", ExpectedFile{}, nil, 100); !errors.Is(err, ErrSourceChanged) {
			t.Fatal("substituted lock accepted", err)
		}
	})
}

func TestFailedStageCannotBeApplied(t *testing.T) {
	root, _, b := replacementFixture(t)
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &checkpointContext{Context: base, at: 2, action: cancel}
	if err := b.Stage(ctx, "target", ExpectedFile{}, []byte("private"), 100); !errors.Is(err, context.Canceled) {
		t.Fatal("staging cancellation missed", err)
	}
	if b.entries["target"] == nil {
		t.Fatal("fixture did not interrupt an actual scratch write")
	}
	if err := b.Apply(t.Context(), "target"); !errors.Is(err, ErrReplacementState) {
		t.Fatal("failed stage installed", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != replacementLock {
		t.Fatal("failed-stage scratch remained", err)
	}
}

type installedCancellation struct {
	context.Context
	path   string
	cancel context.CancelFunc
}

func (c *installedCancellation) Err() error {
	if data, err := os.ReadFile(c.path); err == nil && string(data) == "new" {
		c.cancel()
	}
	return c.Context.Err()
}

func TestReplacementCancellationAfterInstallIsUncertain(t *testing.T) {
	root, _, b := replacementFixture(t)
	path := filepath.Join(root, "target")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Stage(t.Context(), "target", ExpectedFile{Exists: true, SHA256: sha256.Sum256([]byte("old"))}, []byte("new"), 100); err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &installedCancellation{Context: base, path: path, cancel: cancel}
	if err := b.Apply(ctx, "target"); !errors.Is(err, ErrReplacementUncertain) {
		t.Fatal("post-install cancellation was not uncertain", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatal("fixture did not cancel after installation", err)
	}
	if err := b.Apply(t.Context(), "target"); !errors.Is(err, ErrReplacementState) {
		t.Fatal("uncertain installation retried", err)
	}
}
