package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/rolloutstore"
)

const rolloutRel = "cli/sessions/2026/09/05/rollout-2026-09-05T11-22-59-01a0722a-7356-7592-922a-336289bdc101.jsonl"

func stage(t *testing.T, dir string, body []byte, chunked bool) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rolloutRel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if chunked {
		if err := rolloutstore.Write(p, bytes.NewReader(body), time.Now()); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// An explicit setting is followed; an absent one follows the repo, because
// the engine converts EVERY staged rollout to the chosen representation and a
// new machine with no opinion must not rewrite a whole-file repository into
// parts, or back.
func TestAnUnconfiguredMachineFollowsTheRepositorysChunking(t *testing.T) {
	big := bytes.Repeat([]byte("y"), int(rolloutstore.Threshold)+1)
	for _, tc := range []struct {
		name string
		seed func(dir string)
		want bool
	}{
		{"empty repo defaults on", func(string) {}, true},
		{"repo holds a chunk index", func(d string) { stage(t, d, big, true) }, true},
		{"repo holds a large plain rollout", func(d string) { stage(t, d, big, false) }, false},
		{"repo holds only a small plain rollout", func(d string) { stage(t, d, []byte("tiny\n"), false) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.seed(dir)
			if got := chunkRollouts(config.Default(), dir); got != tc.want {
				t.Errorf("chunkRollouts = %v, want %v", got, tc.want)
			}
		})
	}
	off := false
	dir := t.TempDir()
	stage(t, dir, big, true)
	cfg := config.Default()
	cfg.ChunkRollouts = &off
	if chunkRollouts(cfg, dir) {
		t.Error("an explicit false was overridden by the repo")
	}
}

// The journal timestamp comes from the injected clock like everything else in
// Sync, or a test of the overdue branch cannot control it.
func TestRecordForUsesTheClockItIsGiven(t *testing.T) {
	then := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rec := recordFor(then, "mbp", nil, nil)
	if !rec.At.Equal(then) {
		t.Errorf("At = %v, want %v", rec.At, then)
	}
	_ = context.Background()
	_ = strings.TrimSpace
}
