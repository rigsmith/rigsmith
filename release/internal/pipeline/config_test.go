// Ported from net-changesets Release/ReleaseConfigServiceTests.cs (6 tests).
package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parseConfig(t *testing.T, content string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.jsonc")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return LoadConfig(path)
}

func mustParseConfig(t *testing.T, content string) *Config {
	t.Helper()
	config, err := parseConfig(t, content)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	return config
}

func TestLoadConfigReturnsEmptyConfigWhenFileMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "release.jsonc")

	config, err := LoadConfig(missing)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if config.Tool != "" {
		t.Errorf("Tool = %q, want empty", config.Tool)
	}
	if config.Order != nil {
		t.Errorf("Order = %v, want nil", config.Order)
	}
	if config.Steps != nil {
		t.Errorf("Steps = %v, want nil", config.Steps)
	}
}

func TestLoadConfigParsesJsoncWithCommentsAndTrailingCommas(t *testing.T) {
	config := mustParseConfig(t, `{
  // the tool that drives version/publish
  "tool": "npx changeset",
  "order": ["version", "commit"],
}`)

	if config.Tool != "npx changeset" {
		t.Errorf("Tool = %q", config.Tool)
	}
	if !equalStrings(config.Order, []string{"version", "commit"}) {
		t.Errorf("Order = %v", config.Order)
	}
}

func TestLoadConfigParsesCommandShapes(t *testing.T) {
	config := mustParseConfig(t, `{
  "steps": {
    "commit": { "before": "npm test" },
    "publish": {
      "before": ["npm test", "npm run lint"],
      "args": ["--otp", "${vars.npmOtp}"]
    },
    "fetch": { "run": [["op", "item", "get", "npm", "--otp"]] }
  }
}`)

	// single string -> one shell command
	commitBefore := config.Steps["commit"].Before
	if len(commitBefore) != 1 || !commitBefore[0].IsShell() || commitBefore[0].Shell() != "npm test" {
		t.Errorf("commit before = %#v, want one shell 'npm test'", commitBefore)
	}

	// array of strings -> list of shell commands
	publishBefore := config.Steps["publish"].Before
	if len(publishBefore) != 2 ||
		publishBefore[0].Shell() != "npm test" ||
		publishBefore[1].Shell() != "npm run lint" {
		t.Errorf("publish before = %#v", publishBefore)
	}
	if !equalStrings(config.Steps["publish"].Args, []string{"--otp", "${vars.npmOtp}"}) {
		t.Errorf("publish args = %v", config.Steps["publish"].Args)
	}

	// array containing an argv array -> one argv command
	fetchRun := config.Steps["fetch"].Run
	if len(fetchRun) != 1 {
		t.Fatalf("fetch run = %#v, want one command", fetchRun)
	}
	fetch := fetchRun[0]
	if fetch.IsShell() {
		t.Error("fetch run should be an argv command")
	}
	if !equalStrings(fetch.Argv(), []string{"op", "item", "get", "npm", "--otp"}) {
		t.Errorf("fetch argv = %v", fetch.Argv())
	}
}

func TestLoadConfigParsesHooksAndVars(t *testing.T) {
	config := mustParseConfig(t, `{
  "hooks": { "onError": ["./scripts/rollback.sh"] },
  "vars": { "npmOtp": { "command": ["op", "item", "get", "npm", "--otp"], "lazy": true } }
}`)

	onError := config.Hooks.OnError
	if len(onError) != 1 || !onError[0].IsShell() || onError[0].Shell() != "./scripts/rollback.sh" {
		t.Errorf("onError = %#v", onError)
	}

	npmOtp := config.Vars["npmOtp"]
	if !npmOtp.Lazy {
		t.Error("npmOtp should be lazy")
	}
	if npmOtp.Command == nil || !equalStrings(npmOtp.Command.Argv(), []string{"op", "item", "get", "npm", "--otp"}) {
		t.Errorf("npmOtp command = %#v", npmOtp.Command)
	}
}

func TestLoadConfigParsesConfirmAsBoolOrString(t *testing.T) {
	config := mustParseConfig(t, `{
  "steps": {
    "publish": { "confirm": true },
    "push": { "confirm": "Push to origin?" },
    "version": { "confirm": false }
  }
}`)

	publish := config.Steps["publish"].Confirm
	if publish == nil || !publish.Enabled || publish.Custom != nil {
		t.Errorf("publish confirm = %#v, want enabled with default prompt", publish)
	}

	push := config.Steps["push"].Confirm
	if push == nil || !push.Enabled || push.Custom == nil || *push.Custom != "Push to origin?" {
		t.Errorf("push confirm = %#v, want custom prompt", push)
	}

	// confirm: false parses as the absence of a gate.
	version := config.Steps["version"].Confirm
	if version != nil && version.Enabled {
		t.Errorf("version confirm = %#v, want no gate", version)
	}
	if got := confirmMessageFor("version", config.Steps["version"]); got != nil {
		t.Errorf("version resolved confirm = %q, want none", *got)
	}
}

func TestLoadConfigErrorsOnInvalidJSON(t *testing.T) {
	_, err := parseConfig(t, "{ not valid")

	if err == nil || !strings.Contains(err.Error(), "release config") {
		t.Errorf("err = %v, want a 'release config' parse error", err)
	}
}
