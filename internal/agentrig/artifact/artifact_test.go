package artifact

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) Store {
	t.Helper()
	return Store{Dir: filepath.Join(t.TempDir(), "captures")}
}
func TestSealedCaptureReuseMetadataAndRoundTrip(t *testing.T) {
	s := testStore(t)
	key := Key([]byte("binding and sealed events"))
	calls := 0
	payload := bytes.Repeat([]byte("native\x00bytes\r\n"), 10000)
	build := func(ctx context.Context, dir string, meta *Metadata) error {
		calls++
		meta.BaseReference = "retained-seed-commit"
		if err := os.Mkdir(filepath.Join(dir, "nested"), 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "nested", "capture.bin"), payload, 0600)
	}
	ref, err := s.BuildWithMetadata(t.Context(), key, build)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := s.path(key)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.BuildWithMetadata(t.Context(), key, build)
	if err != nil || again != ref || calls != 1 {
		t.Fatalf("reused: %s %d %v", again, calls, err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("retry did not rewrite/flush the existing capture")
	}
	meta, err := s.Metadata(t.Context(), ref)
	if err != nil || meta.BaseReference != "retained-seed-commit" {
		t.Fatalf("metadata %+v %v", meta, err)
	}
	dest := filepath.Join(t.TempDir(), "new-tree")
	if err = s.Extract(t.Context(), ref, dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "nested", "capture.bin"))
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatal("bytes changed", err)
	}
	if err = s.Extract(t.Context(), ref, dest); !os.IsExist(err) {
		t.Fatal("overwrote existing destination", err)
	}
}

func TestFailedBuildAndCapacityNeverPublishPartialCapture(t *testing.T) {
	for _, mode := range []string{"builder-error", "capacity", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "capacity" {
				s.MaxBytes = 1024
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("builder failed")
			_, err := s.Build(ctx, Key([]byte(mode)), func(_ context.Context, dir string) error {
				if err := os.WriteFile(filepath.Join(dir, "partial"), []byte(strings.Repeat("x", 2048)), 0600); err != nil {
					return err
				}
				if mode == "builder-error" {
					return cause
				}
				if mode == "cancel" {
					cancel()
				}
				return nil
			})
			if err == nil {
				t.Fatal("partial capture succeeded")
			}
			files, err := os.ReadDir(s.Dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 0 {
				t.Fatal("failed capture retained partial output", files)
			}
		})
	}
}

func TestCorruptionCannotTriggerRecaptureOrExtraction(t *testing.T) {
	s := testStore(t)
	key := Key([]byte("one"))
	ref, err := s.Build(t.Context(), key, func(_ context.Context, dir string) error {
		return os.WriteFile(filepath.Join(dir, "data"), []byte("capture"), 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	path, _ := s.path(key)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Build(t.Context(), key, func(context.Context, string) error { t.Fatal("corruption caused recapture"); return nil }); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err = s.Extract(t.Context(), ref, dest); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err = os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("created destination before verifying", err)
	}
}

func forgedArchive(t *testing.T, s Store, key string, h *tar.Header) string {
	t.Helper()
	var b bytes.Buffer
	b.WriteString(magic + key)
	metadata, _ := json.Marshal(Metadata{})
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(metadata)))
	b.Write(n[:])
	b.Write(metadata)
	tw := tar.NewWriter(&b)
	if err := tw.WriteHeader(h); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b.Bytes())
	b.Write(sum[:])
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	path, _ := s.path(key)
	if err := os.WriteFile(path, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return key + ":" + hex.EncodeToString(sum[:])
}
func TestExtractionRejectsTraversalGitMetadataAndLinks(t *testing.T) {
	for _, h := range []*tar.Header{{Name: "../escape", Typeflag: tar.TypeReg}, {Name: ".git/config", Typeflag: tar.TypeReg}, {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../escape"}, {Name: "link", Typeflag: tar.TypeLink, Linkname: "../escape"}} {
		t.Run(h.Name+string(h.Typeflag), func(t *testing.T) {
			s := testStore(t)
			ref := forgedArchive(t, s, Key([]byte(h.Name)), h)
			dest := filepath.Join(t.TempDir(), "out")
			if err := s.Extract(t.Context(), ref, dest); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatal("failed extraction left partial files", err)
			}
		})
	}
}
func TestBuildPreservesMtime(t *testing.T) {
	s := testStore(t)
	mtime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	ref, err := s.Build(t.Context(), Key([]byte("mtime")), func(_ context.Context, dir string) error {
		p := filepath.Join(dir, "file")
		if err := os.WriteFile(p, []byte("data"), 0700); err != nil {
			return err
		}
		return os.Chtimes(p, mtime, mtime)
	})
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "tree")
	if err = s.Extract(t.Context(), ref, dest); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dest, "file"))
	if err != nil || !st.ModTime().Equal(mtime) {
		t.Fatal("mtime changed", err)
	}
}

func TestBuildRefusesLinkedContent(t *testing.T) {
	s := testStore(t)
	_, err := s.Build(t.Context(), Key([]byte("link")), func(_ context.Context, dir string) error {
		if err := os.Symlink("missing", filepath.Join(dir, "linked")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		return nil
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatal("linked content sealed", err)
	}
}
