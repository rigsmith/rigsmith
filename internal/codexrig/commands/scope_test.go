package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/codexrig/appserver"
	"github.com/rigsmith/rigsmith/internal/codexrig/guard"
	"github.com/rigsmith/rigsmith/internal/codexrig/hooks"
	"github.com/spf13/cobra"
)

type scopeFakeClient struct {
	list   []appserver.HookInfo
	writes []string
	err    error
}

func (c *scopeFakeClient) ListHooks(context.Context) ([]appserver.HookInfo, error) {
	return c.list, nil
}
func (c *scopeFakeClient) WriteConfig(_ context.Context, key string, value any) error {
	c.writes = append(c.writes, key)
	return c.err
}
func scopeListing(t *testing.T, path string) []appserver.HookInfo {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string                           `json:"matcher"`
			Hooks   []struct{ Type, Command string } `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var out []appserver.HookInfo
	for event, groups := range doc.Hooks {
		for gi, g := range groups {
			for hi, h := range g.Hooks {
				out = append(out, appserver.HookInfo{SourcePath: path, EventName: strings.ToLower(event[:1]) + event[1:], Key: path + ":" + hooks.TrustKeyEvent(hooks.Event(event)) + ":" + fmtInt(gi) + ":" + fmtInt(hi), Command: h.Command, HandlerType: h.Type, Matcher: g.Matcher, CurrentHash: "sha256:listed", TrustStatus: "untrusted", Enabled: true})
			}
		}
	}
	return out
}
func fmtInt(i int) string { raw, _ := json.Marshal(i); return string(raw) }
func scopedCommand(t *testing.T, project bool, verb string) (*cobra.Command, string, string, []hooks.Plan) {
	t.Helper()
	repo, home := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	parent, path, plans := ScopeCommands()[0], filepath.Join(home, hooks.FileName), hooks.SyncPlans()
	if project {
		parent, path, plans = ScopeCommands()[1], filepath.Join(repo, ".codex", hooks.FileName), hooks.GuardPlans(guard.Matcher())
	}
	cmd, _, err := parent.Find([]string{verb})
	if err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(context.Background())
	return cmd, path, home, plans
}
func TestScopeTrustSelectsExplicitScope(t *testing.T) {
	for _, project := range []bool{false, true} {
		cmd, path, home, plans := scopedCommand(t, project, "trust")
		if _, _, err := hooks.Install(path, plans); err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(t.TempDir(), hooks.FileName)
		if _, _, err := hooks.Install(other, hooks.SyncPlans()); err != nil {
			t.Fatal(err)
		}
		client := &scopeFakeClient{list: append(scopeListing(t, path), scopeListing(t, other)...)}
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := trustScope(cmd, func(context.Context) (string, error) { return path, nil }, client); err != nil {
			t.Fatal(err)
		}
		if len(client.writes) != len(plans) {
			t.Fatalf("project=%v wrote=%+v output=%s", project, client.writes, out.String())
		}
		for _, key := range client.writes {
			if !strings.Contains(key, path) || strings.Contains(key, other) {
				t.Fatalf("scope leaked: %s home=%s", key, home)
			}
		}
	}
}
func TestScopeTrustReportsExactCommandsAndReturnsError(t *testing.T) {
	for _, project := range []bool{false, true} {
		for _, asJSON := range []bool{false, true} {
			cmd, path, _, plans := scopedCommand(t, project, "trust")
			command := "curl evil # codexrig\r\ntrusted forged\x1b]52;c;YWJj\a"
			plans[0].Command = command
			if _, _, err := hooks.Install(path, plans); err != nil {
				t.Fatal(err)
			}
			client := &scopeFakeClient{list: scopeListing(t, path), err: errors.New("write failed")}
			if asJSON {
				if err := cmd.Flags().Set("json", "true"); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			cmd.SetOut(&out)
			err := trustScope(cmd, func(context.Context) (string, error) { return path, nil }, client)
			if err == nil {
				t.Fatal("lookalike succeeded")
			}
			if asJSON {
				var result hooks.TrustResult
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatalf("invalid JSON: %v: %s", err, out.String())
				}
				if len(result.Lookalikes) != 1 || result.Lookalikes[0].Command != command {
					t.Fatalf("wrong JSON decisions: %s", out.String())
				}
			} else {
				if strings.ContainsAny(out.String(), "\r\x1b\a") || strings.Contains(out.String(), "\ntrusted forged") || !strings.Contains(out.String(), hooks.Display(command)) {
					t.Fatalf("unsafe/refusal missing: %q", out.String())
				}
			}
		}
	}
}
func TestScopeTrustEmptyJSONAndUnrelated(t *testing.T) {
	for _, project := range []bool{false, true} {
		cmd, path, _, _ := scopedCommand(t, project, "trust")
		if err := cmd.Flags().Set("json", "true"); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := trustScope(cmd, func(context.Context) (string, error) { return path, nil }, &scopeFakeClient{}); err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		for key, value := range result {
			if value == nil {
				t.Fatalf("null list %s: %s", key, out.String())
			}
		}
	}
}
func TestScopeTrustProjectInstallRejectsEscapingLink(t *testing.T) {
	cmd, path, home, _ := scopedCommand(t, true, "install")
	if _, _, err := hooks.Install(filepath.Join(home, hooks.FileName), hooks.SyncPlans()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(home, filepath.Dir(path)); err != nil {
		t.Skip(err)
	}
	if err := installScope(cmd, func(context.Context) (string, error) { return path, nil }, guardPlans); err == nil {
		t.Fatal("project install accepted escaping .codex symlink")
	}
}

func TestScopeTrustProjectUninstallRejectsEscapingLink(t *testing.T) {
	_, path, home, _ := scopedCommand(t, true, "uninstall")
	global := filepath.Join(home, hooks.FileName)
	if _, _, err := hooks.Install(global, hooks.SyncPlans()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(global)
	if err := os.Symlink(home, filepath.Dir(path)); err != nil {
		t.Skip(err)
	}
	parent := scopeCmd("project", nil, "project", "project", func(context.Context) (string, error) { return path, nil }, guardPlans)
	parent.SetContext(context.Background())
	parent.SetArgs([]string{"uninstall"})
	var out bytes.Buffer
	parent.SetOut(&out)
	parent.SetErr(&out)
	if err := parent.Execute(); err == nil {
		t.Fatal("project uninstall wrote through global alias")
	}
	after, _ := os.ReadFile(global)
	if !bytes.Equal(before, after) {
		t.Fatal("global backup hooks removed")
	}
}
func TestScopeTrustThreeWayStatusOutput(t *testing.T) {
	for _, project := range []bool{false, true} {
		for _, mode := range []string{"healthy", "unrelated", "stale", "lookalike", "disabled", "unreported"} {
			t.Run(fmtInt(map[bool]int{false: 0, true: 1}[project])+mode, func(t *testing.T) {
				cmd, path, _, plans := scopedCommand(t, project, "status")
				if mode == "unrelated" {
					plans = []hooks.Plan{{Event: hooks.Stop, Command: "acme notify"}}
				}
				if mode == "stale" || mode == "lookalike" {
					plans[0].Command = "codexrig unknown --old"
				}
				if _, _, err := hooks.Install(path, plans); err != nil {
					t.Fatal(err)
				}
				scope := commandHookScope(cmd, path)
				status, err := hooks.Status(path, scope)
				if err != nil {
					t.Fatal(err)
				}
				var out bytes.Buffer
				cmd.SetOut(&out)
				if mode == "unreported" {
					status.Unreported = status.Ours
					status.Ours = nil
				} else {
					for i := range status.Ours {
						status.Ours[i].TrustStatus = "trusted"
						status.Ours[i].Enabled = mode != "disabled"
					}
				}
				printCheckedTrustState(cmd, path, status)
				text := out.String()
				switch mode {
				case "healthy":
					if !strings.Contains(text, "Codex will run them") {
						t.Fatalf("%s", text)
					}
				case "disabled":
					if !strings.Contains(text, "disabled") || strings.Contains(text, "Codex will run them") {
						t.Fatalf("%s", text)
					}
				case "unreported":
					if !strings.Contains(text, "trust unknown") {
						t.Fatalf("%s", text)
					}
				case "stale", "lookalike":
					if !strings.Contains(text, "lookalike") || !strings.Contains(text, plans[0].Command) {
						t.Fatalf("%s", text)
					}
				case "unrelated":
					if !strings.Contains(text, "left alone") || strings.Contains(text, "refused") {
						t.Fatalf("%s", text)
					}
				}
			})
		}
	}
}
