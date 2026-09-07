package mergepolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

type chunkFilesFixture struct {
	sides [3]map[string][]byte
	added map[string][]byte
	reads int
}

func (f *chunkFilesFixture) Read(ctx context.Context, side commitartifact.ConflictSide, path string) ([]byte, error) {
	f.reads++
	b, ok := f.sides[side][path]
	if !ok {
		return nil, commitartifact.ErrConflict
	}
	return b, ctx.Err()
}
func (f *chunkFilesFixture) Add(ctx context.Context, path string, b []byte) error {
	f.added[path] = bytes.Clone(b)
	return ctx.Err()
}

func chunkSnapshot(t *testing.T, path, body string) ([]byte, map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	name := filepath.Join(root, filepath.FromSlash(path))
	if err := transcript.Write(name, strings.NewReader(body), time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string][]byte{}
	if err := filepath.WalkDir(name+transcript.Suffix, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		parts[filepath.ToSlash(rel)] = b
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return index, parts
}

func TestRetainedChunksAppend(t *testing.T) {
	const path = "cli/projects/-p/s.jsonl"
	// Exceed the normal auto-chunk threshold and split a JSON record across parts.
	line := "{\"text\":\"" + strings.Repeat("shared text ", 6000) + "\"}\r\n"
	base := strings.Repeat(line, 128)
	shared := "{\"uuid\":\"shared\"}\n"
	local := "{\"uuid\":\"local\",\"unknown\":{\"x\":true}}\n"
	incoming := "{\"uuid\":\"remote\"}\n"
	f := &chunkFilesFixture{added: map[string][]byte{}}
	var sides [3][]byte
	for i, body := range []string{base, base + shared + local, base + shared + incoming + local} {
		sides[i], f.sides[i] = chunkSnapshot(t, path, body)
	}
	got, err := ResolveRetainedFiles(t.Context(), path, sides[0], sides[1], sides[2], f)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := transcript.ReadStored(path, got, func(path string) ([]byte, error) { return f.added[path], nil }, 0)
	want := base + shared + local + incoming
	if err != nil || !bytes.Equal(restored, []byte(want)) {
		t.Fatalf("chunk union: len=%d want=%d err=%v", len(restored), len(want), err)
	}
	native, _ := chunkSnapshot(t, path, want)
	if !bytes.Equal(native, got) {
		t.Fatal("output differs from native chunk writer")
	}
}

func TestRetainedChunksRefusal(t *testing.T) {
	const path = "cli/projects/-p/s.jsonl"
	for _, kind := range []string{"missing", "corrupt", "edited", "uuid", "mixed", "unknown", "duplicate", "alias", "null-parts", "large", "cancel", "wrong-path", "non-json-whitespace", "future-version"} {
		t.Run(kind, func(t *testing.T) {
			f := &chunkFilesFixture{added: map[string][]byte{}}
			var sides [3][]byte
			bodies := []string{"{\"uuid\":\"base\"}\n", "{\"uuid\":\"base\"}\n{\"uuid\":\"local\"}\n", "{\"uuid\":\"base\"}\n{\"uuid\":\"remote\"}\n"}
			if kind == "edited" {
				bodies[2] = "{\"uuid\":\"edited\"}\n"
			}
			if kind == "uuid" {
				bodies[2] = bodies[0] + "{\"uuid\":\"local\",\"different\":true}\n"
			}
			for i, body := range bodies {
				sides[i], f.sides[i] = chunkSnapshot(t, path, body)
			}
			switch kind {
			case "missing":
				f.sides[0] = nil
			case "corrupt":
				for p, data := range f.sides[2] {
					f.sides[2][p] = bytes.Repeat([]byte("x"), len(data))
				}
			case "mixed":
				sides[0] = []byte(bodies[0])
			case "non-json-whitespace":
				sides[2] = append([]byte("\u00a0"), sides[2]...)
			case "future-version":
				sides[2] = bytes.Replace(sides[2], []byte(`"clauderig_chunked_transcript":1`), []byte(`"clauderig_chunked_transcript":2`), 1)
			case "unknown":
				sides[2] = bytes.Replace(sides[2], []byte(`"size":`), []byte(`"future":true,"size":`), 1)
			case "duplicate":
				sides[2] = bytes.Replace(sides[2], []byte(`"size":`), []byte(`"size":1,"size":`), 1)
			case "alias":
				sides[2] = bytes.Replace(sides[2], []byte(`"size":`), []byte(`"Size":`), 1)
			case "null-parts":
				sides[2] = []byte("{\"clauderig_chunked_transcript\":1,\"size\":0,\"parts\":null}\n")
			case "large":
				idx := transcript.Index{Version: 1, Size: 9 * transcript.ChunkSize}
				for range 9 {
					idx.Parts = append(idx.Parts, transcript.Part{Hash: strings.Repeat("a", 64), Size: transcript.ChunkSize})
				}
				sides[2], _ = json.Marshal(idx)
			}
			ctx := t.Context()
			if kind == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			name := path
			if kind == "wrong-path" {
				name = "cli/history.jsonl"
			}
			_, err := ResolveRetainedFiles(ctx, name, sides[0], sides[1], sides[2], f)
			if err == nil || len(f.added) != 0 {
				t.Fatalf("unsafe recovery: %v %d additions", err, len(f.added))
			}
			if kind == "large" && (!errors.Is(err, artifact.ErrTooLarge) || f.reads != 0) {
				t.Fatal("size bound applied too late", err, f.reads)
			}
		})
	}
}

func TestRetainedChunksEmptyBase(t *testing.T) {
	const path = "cli/projects/-p/s/subagents/agent.jsonl"
	f := &chunkFilesFixture{added: map[string][]byte{}}
	var sides [3][]byte
	for i, body := range []string{"", "{\"uuid\":\"local\"}\n", "{\"uuid\":\"remote\"}\n"} {
		sides[i], f.sides[i] = chunkSnapshot(t, path, body)
	}
	index, err := ResolveRetainedFiles(t.Context(), path, sides[0], sides[1], sides[2], f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := transcript.ReadStored(path, index, func(path string) ([]byte, error) { return f.added[path], nil }, 0)
	if err != nil || string(got) != "{\"uuid\":\"local\"}\n{\"uuid\":\"remote\"}\n" {
		t.Fatalf("empty-base append: %q %v", got, err)
	}
	if _, err := ResolveRetainedFiles(t.Context(), path, nil, sides[1], sides[2], f); !errors.Is(err, commitartifact.ErrConflict) {
		t.Fatal("accepted missing base", err)
	}
}
