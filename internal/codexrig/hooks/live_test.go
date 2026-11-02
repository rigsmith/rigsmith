package hooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/codexrig/appserver"
)

// The live check against the real Codex CLI, and the reason this package can be
// trusted at all. Every fact in its doc comment — PascalCase event keys, the
// snake_case trust key, the hash Codex computes — came from asking a running
// codex rather than from documentation, and nothing but a running codex can
// notice when one of them changes. Gated because it needs the binary:
//
//	CODEXRIG_LIVE_CODEX=1 go test ./internal/codexrig/hooks/
//
// It writes only into t.TempDir(), with CODEX_HOME pointed there, so the
// developer's own Codex is never read or touched.
func TestInstalledHooksAreSeenAndTrustedByCodex(t *testing.T) {
	if os.Getenv("CODEXRIG_LIVE_CODEX") == "" || !appserver.Available() {
		t.Skip("needs the codex CLI and CODEXRIG_LIVE_CODEX=1")
	}
	home := t.TempDir()
	cwd := t.TempDir()
	path := filepath.Join(home, FileName)
	added, _, err := Install(path, SyncPlans())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("installed: %v", added)
	b, _ := os.ReadFile(path)
	t.Logf("hooks.json:\n%s", b)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	before, err := Check(ctx, home, cwd, GlobalScope(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range before.Ours {
		t.Logf("before: %-14s trust=%-10s cmd=%q", h.EventName, h.TrustStatus, h.Command)
	}
	if len(before.Ours) != 2 {
		t.Fatalf("codex saw %d of our hooks, want 2 — the file shape is wrong", len(before.Ours))
	}

	res, err := Trust(ctx, home, cwd, GlobalScope(home))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("trusted: %v already: %v", res.Trusted, res.Already)

	after, err := Check(ctx, home, cwd, GlobalScope(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range after.Ours {
		t.Logf("after:  %-14s trust=%-10s", h.EventName, h.TrustStatus)
		if !h.Trusted() {
			t.Errorf("%s is still %s after Trust", h.EventName, h.TrustStatus)
		}
	}
	cfg, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	t.Logf("config.toml:\n%s", cfg)
}

// Read-only RPC probe; all edits are to disposable fixture files. Keep this
// alongside the manual 0.166.0 probe so field/hash behavior can be rechecked.
func TestHookLiveReviewFields(t *testing.T) {
	if os.Getenv("CODEXRIG_LIVE_CODEX") == "" || !appserver.Available() {
		t.Skip("needs codex and CODEXRIG_LIVE_CODEX=1")
	}
	home, cwd := t.TempDir(), t.TempDir()
	scope := GlobalScope(home)
	if _, _, err := InstallScope(scope); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	client, err := appserver.Start(ctx, home, cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var original string
	for _, field := range []string{"", "commandWindows", "timeout", "statusMessage"} {
		doc := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "codexrig sync --hook"}}}}}}
		if field != "" {
			value := any("probe only; never executed")
			if field == "timeout" {
				value = 20
			}
			firstHandler(doc, "Stop")[field] = value
		}
		if err := save(scope.path, doc); err != nil {
			t.Fatal(err)
		}
		list, err := client.ListHooks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 {
			t.Fatalf("unexpected listing: %+v", list)
		}
		if field == "" {
			original = list[0].CurrentHash
		}
		t.Logf("field=%q command=%q hash=%q changed=%v", field, list[0].Command, list[0].CurrentHash, list[0].CurrentHash != original)
		if field != "" {
			checked, err := CheckWithClient(ctx, client, scope)
			if err != nil || len(checked.Lookalikes) != 1 {
				t.Fatalf("field %q accepted: %+v %v", field, checked, err)
			}
		}
	}
}
