package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/codexrig/appserver"
)

func TestHookLiveInlineLegacyApproval(t *testing.T) {
	if os.Getenv("CODEXRIG_LIVE_CODEX") == "" || !appserver.Available() {
		t.Skip("needs codex and CODEXRIG_LIVE_CODEX=1")
	}
	home, cwd := t.TempDir(), t.TempDir()
	config := "[[hooks.SessionStart]]\n[[hooks.SessionStart.hooks]]\ntype=\"command\"\ncommand=\"codexrig pull; echo probe-not-executed\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	client, err := appserver.Start(ctx, home, cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	list, err := client.ListHooks(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("inline fixture: %+v %v", list, err)
	}
	// Simulate approval recorded by an older version, only in disposable state.
	if err := client.WriteConfig(ctx, trustKeyPath(list[0].Key), list[0].CurrentHash); err != nil {
		t.Fatal(err)
	}
	result, err := TrustWithClient(ctx, client, home, GlobalScope(home))
	if err == nil || len(result.StillTrusted) != 1 || len(result.Trusted) != 0 {
		t.Fatalf("inline approval hidden: %+v %v", result, err)
	}
}

func TestTrustListedLegacyApprovalsFromAnySource(t *testing.T) {
	for _, project := range []bool{false, true} {
		repo, home := t.TempDir(), t.TempDir()
		scope := GlobalScope(home)
		if project {
			scope = ProjectScope(repo, home)
		}
		base := installFixture(t, scope)
		for _, source := range []string{filepath.Join(home, "config.toml"), filepath.Join(repo, ".codex", "config.toml"), filepath.Join(repo, ".codex", ".codex", FileName), filepath.Join(repo, "sub", ".codex", FileName), filepath.Join(t.TempDir(), FileName)} {
			h := base.hooks[0]
			h.SourcePath = source
			h.Key = source + ":session_start:0:0"
			h.Command = "codexrig pull; evil"
			h.TrustStatus = "trusted"
			c := &fakeTrustClient{hooks: append(append([]appserver.HookInfo{}, base.hooks...), h)}
			result, err := TrustWithClient(context.Background(), c, home, scope)
			checked, cerr := CheckWithClient(context.Background(), c, scope)
			if err == nil || cerr != nil || len(result.StillTrusted) != 1 || len(checked.StillTrusted) != 1 || result.StillTrusted[0].SourcePath != source {
				t.Fatalf("project=%v source=%s result=%+v check=%+v err=%v/%v", project, source, result, checked, err, cerr)
			}
			var out bytes.Buffer
			WriteTrustResult(&out, result)
			for _, text := range []string{"STILL TRUSTED", source, h.Command} {
				if !strings.Contains(out.String(), Display(text)) {
					t.Fatalf("missing %q: %s", text, out.String())
				}
			}
		}
	}
}

func TestTrustHealthyOtherScopeIsQuiet(t *testing.T) {
	repo, home := t.TempDir(), t.TempDir()
	global, project := GlobalScope(home), ProjectScope(repo, home)
	gc, pc := installFixture(t, global), installFixture(t, project)
	for i := range gc.hooks {
		gc.hooks[i].TrustStatus = "trusted"
	}
	c := &fakeTrustClient{hooks: append(pc.hooks, gc.hooks...)}
	result, err := TrustWithClient(context.Background(), c, home, project)
	if err != nil || len(result.Outside) != 2 || len(result.StillTrusted) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var out bytes.Buffer
	WriteTrustResult(&out, result)
	for _, h := range gc.hooks {
		if strings.Contains(out.String(), h.Command) {
			t.Fatalf("healthy global hook produced noise: %s", out.String())
		}
	}
}

func TestTrustGlobalAliasRefusesBeforeListing(t *testing.T) {
	repo := t.TempDir()
	home := filepath.Join(repo, "global-home")
	global := GlobalScope(home)
	c := installFixture(t, global)
	for i := range c.hooks {
		c.hooks[i].TrustStatus = "trusted"
	}
	if err := os.Symlink(home, filepath.Join(repo, ".codex")); err != nil {
		t.Skip(err)
	}
	project := ProjectScope(repo, home)
	if project.Contains(global.path) {
		t.Fatal("global-file alias accepted inside repo")
	}
	before, _ := os.ReadFile(global.path)
	result, err := TrustWithClient(context.Background(), c, home, project)
	if err == nil || c.lists != 0 || len(result.Refused) != 1 || len(result.StillTrusted) != 0 || strings.Contains(result.Refused[0].NextStep, "trusted_hash") {
		t.Fatalf("unsafe scope classified real hooks: %+v calls=%d err=%v", result, c.lists, err)
	}
	if _, err := UninstallScope(project); err == nil {
		t.Fatal("uninstall wrote through global alias")
	}
	after, _ := os.ReadFile(global.path)
	if !bytes.Equal(before, after) {
		t.Fatal("global file changed")
	}
	checked, cerr := CheckWithClient(context.Background(), c, global, project)
	if cerr != nil || len(checked.Ours) != 2 || len(checked.StillTrusted) != 0 || len(checked.ScopeErrors) != 1 {
		t.Fatalf("global row poisoned: %+v %v", checked, cerr)
	}
}

func TestTrustListingMustAgreeWithFile(t *testing.T) {
	scope := GlobalScope(t.TempDir())
	c := installFixture(t, scope)
	// Both plans are valid, but a Stop command cannot be approved at the
	// SessionStart key/indices. This isolates the snapshot comparison itself.
	c.hooks[0].EventName = "stop"
	c.hooks[0].Command = "codexrig sync --hook"
	result, err := TrustWithClient(context.Background(), c, "", scope)
	if err == nil || len(result.Lookalikes) != 1 || !strings.Contains(result.Lookalikes[0].Reason, "listing disagrees") || len(c.writes) != 1 {
		t.Fatalf("listing mismatch accepted: %+v %v", result, err)
	}
}

func TestTrustUnlistedHandlerIsReported(t *testing.T) {
	scope := GlobalScope(t.TempDir())
	c := installFixture(t, scope)
	c.hooks = c.hooks[:1]
	result, err := TrustWithClient(context.Background(), c, "", scope)
	if err == nil || len(result.Refused) != 1 || result.Refused[0].Command != "codexrig sync --hook" {
		t.Fatalf("unlisted handler hidden: %+v %v", result, err)
	}
}

func TestTrustCheckMalformedScopeKeepsOtherDiagnostics(t *testing.T) {
	global := GlobalScope(t.TempDir())
	project := ProjectScope(t.TempDir(), filepath.Dir(global.path))
	c := installFixture(t, global)
	installFixture(t, project)
	if err := os.WriteFile(project.path, []byte(`{"hooks":`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy := c.hooks[0]
	legacy.SourcePath = filepath.Join(filepath.Dir(project.path), "config.toml")
	legacy.Key = legacy.SourcePath + ":session_start:0:0"
	legacy.Command = "codexrig pull; evil"
	legacy.TrustStatus = "trusted"
	c.hooks = append(c.hooks, legacy)
	for _, scopes := range [][]Scope{{project, global}, {global, project}} {
		checked, err := CheckWithClient(context.Background(), c, scopes...)
		if err != nil || len(checked.ScopeErrors) != 1 || !strings.Contains(checked.ScopeErrors[0], project.path) || len(checked.Ours) != 2 || len(checked.StillTrusted) != 1 || checked.StillTrusted[0].Command != legacy.Command {
			t.Fatalf("malformed scope hid other diagnostics: %+v %v", checked, err)
		}
	}
}

func TestInstallRepairsSyncMatcherPresenceAndType(t *testing.T) {
	for _, matcher := range []any{"", nil, false, 42} {
		t.Run(fmt.Sprint(matcher), func(t *testing.T) {
			scope := GlobalScope(t.TempDir())
			installFixture(t, scope)
			editFixture(t, scope, func(doc map[string]any) {
				doc["hooks"].(map[string]any)["Stop"].([]any)[0].(map[string]any)["matcher"] = matcher
			})
			if !CanInstall(scope) {
				t.Fatal("installer does not offer to repair an unsupported matcher")
			}
			_, updated, err := InstallScope(scope)
			if err != nil || len(updated) != 1 || updated[0] != "Stop" {
				t.Fatalf("matcher repair: updated=%v err=%v", updated, err)
			}
			status, err := Status(scope.path, scope)
			if err != nil || len(status.Ours) != 2 || len(status.Lookalikes) != 0 || CanInstall(scope) {
				t.Fatalf("repair did not converge: %+v %v", status, err)
			}
			c := listedFixture(t, scope)
			trusted, err := TrustWithClient(context.Background(), c, "", scope)
			if err != nil || len(trusted.Trusted) != 2 {
				t.Fatalf("repaired hooks not trusted: %+v %v", trusted, err)
			}
		})
	}
}

func TestTrustFutureListingFieldNeedsUpdateNotRevocation(t *testing.T) {
	scope := GlobalScope(t.TempDir())
	c := installFixture(t, scope)
	c.hooks[0].TrustStatus = "trusted"
	raw, _ := json.Marshal(c.hooks[0])
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	fields["futureCodexField"] = "new metadata"
	raw, _ = json.Marshal(fields)
	if err := json.Unmarshal(raw, &c.hooks[0]); err != nil {
		t.Fatal(err)
	}
	result, err := TrustWithClient(context.Background(), c, "", scope)
	checked, cerr := CheckWithClient(context.Background(), c, scope)
	if err == nil || cerr != nil || len(result.Incompatible) != 1 || len(result.StillTrusted) != 0 || len(checked.Incompatible) != 1 {
		t.Fatalf("future listing misdiagnosed: %+v %+v %v/%v", result, checked, err, cerr)
	}
	var out bytes.Buffer
	WriteTrustResult(&out, result)
	if !strings.Contains(out.String(), "futureCodexField") || !strings.Contains(out.String(), "update codexrig") || strings.Contains(out.String(), "remove trusted_hash") {
		t.Fatalf("bad compatibility advice: %s", out.String())
	}
	value := "changed execution field"
	c.hooks[0].StatusMessage = &value
	result, err = TrustWithClient(context.Background(), c, "", scope)
	if err == nil || len(result.StillTrusted) != 1 || len(result.Incompatible) != 0 {
		t.Fatalf("unknown metadata hid a known execution-field mismatch: %+v %v", result, err)
	}
}

func TestTrustCodexWarningsSurviveWithHooks(t *testing.T) {
	scope := GlobalScope(t.TempDir())
	c := installFixture(t, scope)
	c.warnings = []string{"duplicate key\x1b]52;c;YWJj\a"}
	result, err := TrustWithClient(context.Background(), c, "", scope)
	checked, cerr := CheckWithClient(context.Background(), c, scope)
	if err != nil || cerr != nil || len(result.Warnings) != 1 || len(checked.Warnings) != 1 {
		t.Fatalf("warnings lost: %+v %+v %v/%v", result, checked, err, cerr)
	}
	var out bytes.Buffer
	WriteTrustResult(&out, result)
	if !strings.Contains(out.String(), "duplicate key") || strings.ContainsAny(out.String(), "\x1b\a") {
		t.Fatalf("unsafe/missing warning: %q", out.String())
	}
}

func TestInstallRefusesMalformedShapesAndPreservesNumbers(t *testing.T) {
	scope := GlobalScope(t.TempDir())
	for _, raw := range []string{`{"hooks":["keep"]}`, `{"hooks":{"PreToolUse":{"note":"mine"}}}`, `{"hooks":{"Stop":[{"hooks":"mine"}]}}`, `{"hooks":{"Stop":[{"hooks":["mine"]}]}}`} {
		if err := os.WriteFile(scope.path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := InstallScope(scope); err == nil {
			t.Fatalf("malformed shape rewritten: %s", raw)
		}
		if _, err := UninstallScope(scope); err == nil {
			t.Fatal("malformed shape uninstalled")
		}
		after, _ := os.ReadFile(scope.path)
		if string(after) != raw {
			t.Fatalf("file changed: %s", after)
		}
	}
	raw := `{"metadata":12345678901234567890,"hooks":{}}`
	if err := os.WriteFile(scope.path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InstallScope(scope); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallScope(scope); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(scope.path)
	if !bytes.Contains(after, []byte("12345678901234567890")) {
		t.Fatalf("number mangled: %s", after)
	}
}

func TestInstallKeepsSiblingIndicesAndUninstallReportsMoves(t *testing.T) {
	scope := GlobalScope(t.TempDir())
	raw := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"codexrig sync"},{"type":"command","command":"keep me"},{"type":"command","command":"say codexrig finished"}]}]}}`
	if err := os.WriteFile(scope.path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, _, err := InstallScope(scope, &out); err != nil {
		t.Fatal(err)
	}
	snap := readSnapshot(scope)
	found := false
	for _, h := range snap.hooks {
		if h.info.Command == "keep me" {
			found = true
			if h.group != 0 || h.handler != 1 {
				t.Fatalf("install moved sibling: %+v", h)
			}
		}
	}
	if !found || out.Len() != 0 {
		t.Fatalf("unexpected move: %s", out.String())
	}
	if _, err := UninstallScope(scope, &out); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(scope.path)
	for _, command := range []string{"keep me", "say codexrig finished"} {
		if !bytes.Contains(after, []byte(command)) {
			t.Fatalf("uninstall removed lookalike/unrelated hook: %s", after)
		}
	}
	if !strings.Contains(out.String(), "trust key changed for keep me") || !strings.Contains(out.String(), "re-approve") {
		t.Fatalf("silent trust-key move: %s", out.String())
	}
}

func TestInstallRemovesLegacySiblingWhenExactExists(t *testing.T) {
	for _, legacyFirst := range []bool{false, true} {
		for _, separateGroups := range []bool{false, true} {
			for _, keepUnrelated := range []bool{false, true} {
				t.Run(fmt.Sprintf("legacyFirst=%v/separateGroups=%v/unrelated=%v", legacyFirst, separateGroups, keepUnrelated), func(t *testing.T) {
					scope := GlobalScope(t.TempDir())
					installFixture(t, scope)
					editFixture(t, scope, func(doc map[string]any) {
						commands := []string{"codexrig sync --hook", "codexrig sync"}
						if legacyFirst {
							commands[0], commands[1] = commands[1], commands[0]
						}
						var groups, handlers []any
						for _, command := range commands {
							h := map[string]any{"type": "command", "command": command}
							if separateGroups {
								groups = append(groups, map[string]any{"hooks": []any{h}})
							} else {
								handlers = append(handlers, h)
							}
						}
						if keepUnrelated {
							h := map[string]any{"type": "command", "command": "acme backup"}
							if separateGroups {
								groups = append(groups, map[string]any{"hooks": []any{h}})
							} else {
								handlers = append(handlers, h)
							}
						}
						if !separateGroups {
							groups = append(groups, map[string]any{"hooks": handlers})
						}
						doc["hooks"].(map[string]any)["Stop"] = groups
					})
					_, updated, err := InstallScope(scope)
					if err != nil || len(updated) != 1 || updated[0] != "Stop" {
						t.Fatalf("legacy repair: updated=%v err=%v", updated, err)
					}
					exact, unrelated := 0, 0
					for _, h := range readSnapshot(scope).hooks {
						switch h.info.Command {
						case "codexrig sync":
							t.Fatal("legacy sibling remains")
						case "codexrig sync --hook":
							exact++
						case "acme backup":
							unrelated++
						}
					}
					if exact != 1 || (keepUnrelated && unrelated != 1) || CanInstall(scope) {
						t.Fatalf("repair did not converge: exact=%d unrelated=%d", exact, unrelated)
					}
					if !keepUnrelated {
						drifted, err := Drift(scope.path, scope.Plans())
						if err != nil || len(drifted) != 0 {
							t.Fatalf("repaired hook still drifts: %v %v", drifted, err)
						}
					}
				})
			}
		}
	}
}
