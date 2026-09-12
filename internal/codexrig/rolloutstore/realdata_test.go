package rolloutstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The gated check against this machine's own rollouts, and the one that proves
// chunking earns its keep. The synthetic tests pin the semantics on files built
// to exercise boundaries; this runs the biggest real conversation on the machine
// through the same path and compares hashes.
//
//	CODEXRIG_REAL_DATA=1 go test ./internal/codexrig/rolloutstore/
//
// Read-only against ~/.codex: it writes only into t.TempDir().
func TestTheBiggestRealRolloutRoundTrips(t *testing.T) {
	if os.Getenv("CODEXRIG_REAL_DATA") == "" {
		t.Skip("set CODEXRIG_REAL_DATA=1 to run this machine's own sessions through the store")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	var files []string
	_ = filepath.Walk(filepath.Join(home, ".codex", "sessions"), func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasSuffix(p, ".jsonl") {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		t.Skip("no rollouts on this machine")
	}
	sort.Slice(files, func(i, j int) bool { return size(files[i]) > size(files[j]) })

	biggest := files[0]
	n := size(biggest)
	t.Logf("largest rollout: %.1f MB", float64(n)/(1<<20))
	if n <= Threshold {
		t.Skipf("nothing here is over the %d MB threshold", Threshold>>20)
	}

	src, err := os.Open(biggest)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.New()
	staged := filepath.Join(t.TempDir(), "rollout.jsonl")
	start := time.Now()
	if err := Write(staged, io.TeeReader(src, want), time.Now()); err != nil {
		_ = src.Close()
		t.Fatal(err)
	}
	_ = src.Close()
	wrote := time.Since(start)

	f, err := Open(staged)
	if err != nil {
		t.Fatal(err)
	}
	got := sha256.New()
	read, err := io.Copy(got, f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if read != n {
		t.Fatalf("read back %d bytes, want %d", read, n)
	}
	if !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		t.Fatalf("the rollout came back with a different hash\n  wrote %s\n  read  %s",
			hex.EncodeToString(want.Sum(nil)), hex.EncodeToString(got.Sum(nil)))
	}

	idx, err := Decode(mustRead(t, staged))
	if err != nil {
		t.Fatal(err)
	}
	indexSize := size(staged)
	t.Logf("%d parts, index %d bytes (%.4f%% of the rollout), written in %s",
		len(idx.Parts), indexSize, float64(indexSize)/float64(n)*100, wrote.Round(time.Millisecond))

	// The point of the exercise: one more turn must cost a chunk, not 180 MB.
	// A real append is at the END, so every earlier part keeps its hash.
	before := map[string]bool{}
	for _, p := range idx.Parts {
		before[p.Hash] = true
	}
	grown := filepath.Join(t.TempDir(), "grown.jsonl")
	if err := appendTurn(biggest, grown); err != nil {
		t.Fatal(err)
	}
	src2, err := os.Open(grown)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(staged, src2, time.Now()); err != nil {
		_ = src2.Close()
		t.Fatal(err)
	}
	_ = src2.Close()

	idx2, err := Decode(mustRead(t, staged))
	if err != nil {
		t.Fatal(err)
	}
	reused, added := 0, 0
	for _, p := range idx2.Parts {
		if before[p.Hash] {
			reused++
		} else {
			added++
		}
	}
	t.Logf("after one more turn: %d parts reused, %d new", reused, added)
	if added > 2 {
		t.Errorf("an append rewrote %d parts; git would store %d MB again for one turn",
			added, added*ChunkSize>>20)
	}
	if reused < len(idx.Parts)-1 {
		t.Errorf("only %d of %d parts survived the append", reused, len(idx.Parts))
	}
}

// appendTurn copies src to dst with one more conversation record on the end.
func appendTurn(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	_, err = out.WriteString(`{"timestamp":"2026-09-12T00:00:00.000Z","ordinal":999999,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"one more turn"}]}}` + "\n")
	return err
}

func size(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
