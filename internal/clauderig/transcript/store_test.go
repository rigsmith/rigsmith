package transcript

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func snapshot(t *testing.T, p string, b []byte) *Index {
	t.Helper()
	if err := Write(p, bytes.NewReader(b), time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := Decode(raw)
	if err != nil || idx == nil {
		t.Fatalf("decode: %v", err)
	}
	return idx
}

func TestRoundTripAppendAndReadAt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "session.jsonl")
	// Includes arbitrary bytes, a split UTF-8 character, and a record larger than
	// a chunk. Chunk boundaries must never alter the original byte stream.
	data := append(bytes.Repeat([]byte("a"), ChunkSize-1), []byte("🌍\n")...)
	data = append(data, bytes.Repeat([]byte("b"), ChunkSize+100)...)
	first := snapshot(t, p, data)
	got, err := ReadFile(p)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("roundtrip: %v", err)
	}
	f, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, _ := f.Stat()
	if st.Size() != int64(len(data)) || !st.ModTime().Equal(time.Unix(1700000000, 0)) {
		t.Fatal("logical metadata lost")
	}
	for _, off := range []int64{0, ChunkSize - 2, ChunkSize, int64(len(data) - 3)} {
		b := make([]byte, 10)
		n, err := f.ReadAt(b, off)
		want := data[off:min(int(off)+10, len(data))]
		if !bytes.Equal(b[:n], want) || (err != nil && err != io.EOF) {
			t.Fatalf("ReadAt %d: %v", off, err)
		}
	}
	// An append reuses the first two full chunks; only its short tail changes.
	data = append(data, []byte("new turn\n")...)
	next := snapshot(t, p, data)
	if first.Parts[0] != next.Parts[0] || first.Parts[1] != next.Parts[1] || first.Parts[2] == next.Parts[2] {
		t.Fatal("sealed chunks not reused")
	}
	if err := Clean(filepath.Dir(p)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p+Suffix, first.Parts[2].Hash+".part")); !os.IsNotExist(err) {
		t.Fatal("old tail retained in current tree")
	}
	dest := filepath.Join(t.TempDir(), "native.jsonl")
	if err := Materialize(p, dest, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(dest)
	if err != nil || !bytes.Equal(data, got) {
		t.Fatalf("native restore: %v", err)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("injected read failure") }

func TestFailurePreservesPreviousSnapshotAndDestination(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	data := bytes.Repeat([]byte("old data\n"), 600000)
	idx := snapshot(t, p, data)
	if err := Write(p, io.MultiReader(bytes.NewReader(bytes.Repeat([]byte("x"), ChunkSize)), brokenReader{}), time.Now()); err == nil {
		t.Fatal("write accepted read failure")
	}
	got, err := ReadFile(p)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("old snapshot damaged: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "native.jsonl")
	if err := os.WriteFile(dest, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(p+Suffix, idx.Parts[0].Hash+".part")
	if err := os.WriteFile(part, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Materialize(p, dest, 0o600); err == nil {
		t.Fatal("accepted corrupt chunk")
	}
	got, _ = os.ReadFile(dest)
	if string(got) != "keep me" {
		t.Fatal("failed restore overwrote live destination")
	}
	if err := os.Remove(part); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(p); err == nil {
		t.Fatal("accepted missing chunk")
	}
}

func TestMigrationAndRollbackIncludingRemoteOnlyFiles(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "cli/projects/-remote/s.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("native data\n"), 900000)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	small := filepath.Join(filepath.Dir(p), "small.jsonl")
	if err := os.WriteFile(small, []byte("small\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, on := range []bool{true, true, false, false} {
		if err := ConvertTree(root, on); err != nil {
			t.Fatal(err)
		}
		mode, err := Enabled(root)
		if err != nil || mode != on {
			t.Fatalf("mode %t: %v", mode, err)
		}
		raw, _ := os.ReadFile(p)
		if IsIndex(raw) != on {
			t.Fatalf("wrong representation with mode %t", on)
		}
		got, err := ReadFile(p)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("migration changed bytes: %v", err)
		}
		smallRaw, _ := os.ReadFile(small)
		if string(smallRaw) != "small\n" {
			t.Fatal("small transcript rewritten")
		}
	}
	if _, err := os.Stat(p + Suffix); !os.IsNotExist(err) {
		t.Fatal("rollback left chunk files")
	}
}

func TestInvalidIndexesAndVersion(t *testing.T) {
	for _, raw := range []string{
		`{"clauderig_chunked_transcript":2,"size":0,"parts":[]}`,
		`{"clauderig_chunked_transcript":1,"size":1,"parts":[]}`,
		`{"clauderig_chunked_transcript":1,"size":1,"parts":[{"sha256":"../outside","size":1}]}`,
		`{"clauderig_chunked_transcript":1`,
	} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid index %q", raw)
		}
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, StorageFile), []byte(`{"version":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Enabled(root); err == nil {
		t.Fatal("accepted future storage format")
	}
}

func TestStoredRevisionAndPrefix(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	data := bytes.Repeat([]byte("record\n"), 900000)
	snapshot(t, p, data)
	raw, _ := os.ReadFile(p)
	calls := 0
	load := func(rel string) ([]byte, error) {
		calls++
		return os.ReadFile(filepath.Join(filepath.Dir(p), filepath.FromSlash(rel)))
	}
	got, err := ReadStored("s.jsonl", raw, load, 100)
	if err != nil || !bytes.Equal(got, data[:100]) || calls != 1 {
		t.Fatalf("prefix: calls=%d, %v", calls, err)
	}
	got, err = ReadStored("s.jsonl", raw, load, 0)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("stored revision: %v", err)
	}
}

func TestChunkSymlinkRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	idx := snapshot(t, p, []byte("body\n"))
	part := filepath.Join(p+Suffix, idx.Parts[0].Hash+".part")
	other := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(other, []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(part); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, part); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadFile(p); err == nil {
		t.Fatal("read followed chunk symlink")
	}
	if err := Clean(filepath.Dir(p)); err == nil {
		t.Fatal("cleanup followed chunk symlink")
	}
}
