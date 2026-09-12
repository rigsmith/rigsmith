package rolloutstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// body builds a rollout of roughly n bytes out of plausible JSONL records, so
// the chunk boundaries fall mid-record the way they will in practice.
func body(n int) []byte {
	line := `{"timestamp":"2026-09-05T11:23:00.000Z","ordinal":1,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` +
		strings.Repeat("x", 200) + `"}]}}` + "\n"
	var b bytes.Buffer
	for b.Len() < n {
		b.WriteString(line)
	}
	return b.Bytes()
}

func stage(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestARoundTripReturnsExactlyTheSameBytes(t *testing.T) {
	// Boundary-crossing sizes on purpose: a rollout is not a multiple of the
	// chunk size, and the last part is the one an append rewrites.
	for _, size := range []int{1, ChunkSize - 1, ChunkSize, ChunkSize + 1, 3*ChunkSize + 77} {
		want := body(size)
		p := stage(t, nil)
		if err := Write(p, bytes.NewReader(want), time.Now()); err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		got, err := ReadFile(p)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("size %d came back changed: %d bytes vs %d", size, len(got), len(want))
		}
	}
}

func TestStatReportsTheConversationsSizeNotTheIndexs(t *testing.T) {
	// Every size comparison in the sync engine goes through this, so a chunked
	// file has to compare against its source the way a plain one does.
	want := body(3 * ChunkSize)
	p := stage(t, nil)
	if err := Write(p, bytes.NewReader(want), time.Now()); err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Size() >= int64(len(want)) {
		t.Fatal("setup: the index should be far smaller than the rollout")
	}
	st, err := Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != int64(len(want)) {
		t.Errorf("Stat = %d, want the logical size %d", st.Size(), len(want))
	}
}

func TestAnAppendRewritesOnlyTheLastPart(t *testing.T) {
	// The whole reason this package exists: a turn added to a long conversation
	// should cost a chunk, not a copy.
	p := stage(t, nil)
	first := body(3 * ChunkSize)
	if err := Write(p, bytes.NewReader(first), time.Now()); err != nil {
		t.Fatal(err)
	}
	before := partNames(t, p)

	grown := append(append([]byte{}, first...), body(1000)...)
	if err := Write(p, bytes.NewReader(grown), time.Now()); err != nil {
		t.Fatal(err)
	}
	after := partNames(t, p)

	// Every full part from before survives by name, because its content is
	// unchanged and its name IS its content.
	kept := 0
	for _, name := range before {
		if contains(after, name) {
			kept++
		}
	}
	if kept < len(before)-1 {
		t.Errorf("only %d of %d parts survived an append; git would store the whole rollout again", kept, len(before))
	}
	got, err := ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, grown) {
		t.Error("the appended rollout did not read back correctly")
	}
}

func TestAnEarlierVersionsPartsAreSweptUp(t *testing.T) {
	p := stage(t, nil)
	if err := Write(p, bytes.NewReader(body(3*ChunkSize)), time.Now()); err != nil {
		t.Fatal(err)
	}
	// A rewrite rather than an append: nothing from the first version is
	// referenced any more.
	if err := Write(p, bytes.NewReader(bytes.Repeat([]byte("z"), ChunkSize+10)), time.Now()); err != nil {
		t.Fatal(err)
	}
	idx := readIndex(t, p)
	names := partNames(t, p)
	if len(names) != len(idx.Parts) {
		t.Errorf("%d part files for an index naming %d — the old version's parts are still on disk", len(names), len(idx.Parts))
	}
}

func TestACorruptPartIsRefusedRatherThanReturned(t *testing.T) {
	// A silently wrong part reconstructs a conversation that never happened.
	p := stage(t, nil)
	if err := Write(p, bytes.NewReader(body(3*ChunkSize)), time.Now()); err != nil {
		t.Fatal(err)
	}
	names := partNames(t, p)
	victim := filepath.Join(p+Suffix, names[0])
	orig, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte{}, orig...)
	tampered[0] ^= 0xFF
	if err := os.WriteFile(victim, tampered, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadFile(p); err == nil {
		t.Fatal("a tampered part was read back as if it were fine")
	} else if !strings.Contains(err.Error(), "hash") {
		t.Errorf("error = %v, want one naming the hash mismatch", err)
	}
}

func TestAMissingPartIsAnErrorNotASilentTruncation(t *testing.T) {
	p := stage(t, nil)
	if err := Write(p, bytes.NewReader(body(3*ChunkSize)), time.Now()); err != nil {
		t.Fatal(err)
	}
	names := partNames(t, p)
	if err := os.Remove(filepath.Join(p+Suffix, names[0])); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(p); err == nil {
		t.Fatal("a rollout with a missing part read back without complaint")
	}
}

func TestDecodeRefusesAnIndexThatCouldMakeAReaderMisbehave(t *testing.T) {
	valid := func() Index {
		h := sha256.Sum256([]byte("x"))
		return Index{Version: 1, Size: ChunkSize + 5, Parts: []Part{
			{Hash: hex.EncodeToString(h[:]), Size: ChunkSize},
			{Hash: hex.EncodeToString(h[:]), Size: 5},
		}}
	}
	cases := map[string]func(*Index){
		"unknown version":             func(i *Index) { i.Version = 2 },
		"size disagrees":              func(i *Index) { i.Size = 999 },
		"non-hex hash":                func(i *Index) { i.Parts[0].Hash = strings.Repeat("z", 64) },
		"short hash":                  func(i *Index) { i.Parts[0].Hash = "abc" },
		"oversized part":              func(i *Index) { i.Parts[0].Size = ChunkSize + 1; i.Size = 2*ChunkSize + 6 },
		"zero-length part":            func(i *Index) { i.Parts[1].Size = 0; i.Size = ChunkSize },
		"short part that is not last": func(i *Index) { i.Parts[0].Size = 10; i.Size = 15 },
	}
	for name, break_ := range cases {
		idx := valid()
		break_(&idx)
		b, err := json.Marshal(idx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	b, err := json.Marshal(valid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(b); err != nil {
		t.Errorf("a valid index was refused: %v", err)
	}
}

func TestAPlainRolloutOpensUnchanged(t *testing.T) {
	// Both representations live at the same path, which is what lets every
	// reader stay unaware of which one it got.
	want := body(1000)
	p := stage(t, want)
	got, err := ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("a plain rollout did not read back verbatim")
	}
	st, err := Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != int64(len(want)) {
		t.Errorf("Stat = %d, want %d", st.Size(), len(want))
	}
}

func TestConvertGoesBothWaysAndLeavesSmallRolloutsAlone(t *testing.T) {
	want := body(3 * ChunkSize)
	p := stage(t, want)
	changed, err := Convert(p, true)
	if err != nil || !changed {
		t.Fatalf("to chunks: changed=%v err=%v", changed, err)
	}
	if raw, _ := os.ReadFile(p); !IsIndex(raw) {
		t.Fatal("the file was not replaced by an index")
	}

	changed, err = Convert(p, false)
	if err != nil || !changed {
		t.Fatalf("back to plain: changed=%v err=%v", changed, err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("the round trip through both representations changed the bytes")
	}
	if _, err := os.Stat(p + Suffix); !os.IsNotExist(err) {
		t.Error("the parts directory was left behind")
	}

	// Below the threshold the index and the directory cost more than they save.
	small := stage(t, body(1000))
	if changed, err := Convert(small, true); err != nil || changed {
		t.Errorf("a small rollout was chunked: changed=%v err=%v", changed, err)
	}
}

func TestIsPartPathRecognisesAPartAndNothingElse(t *testing.T) {
	yes := []string{
		"cli/sessions/2026/09/05/rollout-x.jsonl.chunks/" + strings.Repeat("a", 64) + ".part",
	}
	no := []string{
		"cli/sessions/2026/09/05/rollout-x.jsonl",
		"cli/config.toml",
		"cli/sessions/2026/09/05/rollout-x.jsonl.chunks/notes.txt",
		"cli/skills/a.part",
	}
	for _, rel := range yes {
		if !IsPartPath(rel) {
			t.Errorf("IsPartPath(%q) = false", rel)
		}
	}
	for _, rel := range no {
		if IsPartPath(rel) {
			t.Errorf("IsPartPath(%q) = true", rel)
		}
	}
}

func TestSeekingReadsFromTheMiddle(t *testing.T) {
	// The tail reader seeks; it must land on the right bytes across a boundary.
	want := body(3 * ChunkSize)
	p := stage(t, nil)
	if err := Write(p, bytes.NewReader(want), time.Now()); err != nil {
		t.Fatal(err)
	}
	f, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, off := range []int64{0, 10, ChunkSize - 5, ChunkSize, ChunkSize + 5, 2*ChunkSize + 1} {
		buf := make([]byte, 64)
		n, err := f.ReadAt(buf, off)
		if err != nil && err != io.EOF {
			t.Fatalf("offset %d: %v", off, err)
		}
		if !bytes.Equal(buf[:n], want[off:off+int64(n)]) {
			t.Errorf("offset %d read the wrong bytes", off)
		}
	}
}

func partNames(t *testing.T, p string) []string {
	t.Helper()
	entries, err := os.ReadDir(p + Suffix)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			out = append(out, e.Name())
		}
	}
	return out
}

func readIndex(t *testing.T, p string) *Index {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// This answer exempts a file from the allowlist and the audit, so it has to be
// exactly the shape a chunk has and nothing looser.
func TestIsPartPathRequiresTheExactShape(t *testing.T) {
	h := strings.Repeat("ab", 32)
	for p, want := range map[string]bool{
		"cli/sessions/2026/x.jsonl.chunks/" + h + ".part":                  true,
		"cli/sessions/2026/x.jsonl.chunks/nested/" + h + ".part":           false,
		"cli/sessions/2026/x.jsonl.chunks/secret.part":                     false,
		"cli/sessions/2026/x.jsonl.chunks/" + strings.ToUpper(h) + ".part": false,
		"cli/sessions/2026/x.jsonl.chunks/" + h + ".txt":                   false,
		"cli/x.jsonl/" + h + ".part":                                       false,
	} {
		if got := IsPartPath(p); got != want {
			t.Errorf("IsPartPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestReadAtHonoursTheReaderAtContract(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.jsonl")
	body := bytes.Repeat([]byte("z"), 3*ChunkSize/2+7)
	if err := Write(p, bytes.NewReader(body), time.Now()); err != nil {
		t.Fatal(err)
	}
	f, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ra := f.(io.ReaderAt)
	if _, err := ra.ReadAt(make([]byte, 4), -1); err == nil {
		t.Error("a negative offset was accepted")
	}
	buf := make([]byte, 100)
	n, err := ra.ReadAt(buf, int64(len(body))-10)
	if n != 10 || err != io.EOF {
		t.Errorf("read past the end returned n=%d err=%v, want 10 and io.EOF", n, err)
	}
}

// The scrub rewrites a rollout as plain bytes over whatever was there; a chunked
// one left its .chunks directory behind, holding parts the audit skips and
// `git add -A` publishes. Convert sees every staged rollout, so it cleans up.
func TestConvertRemovesAStaleSidecarBesideAPlainRollout(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.jsonl")
	if err := Write(p, bytes.NewReader(bytes.Repeat([]byte("z"), ChunkSize+1)), time.Now()); err != nil {
		t.Fatal(err)
	}
	// Now something (the scrub) replaces the index with plain bytes.
	if err := os.WriteFile(p, []byte("plain\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Convert(p, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(p + Suffix); !os.IsNotExist(err) {
		t.Error("the orphaned .chunks directory survived")
	}
}
