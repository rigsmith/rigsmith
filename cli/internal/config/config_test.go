// Ports of the .NET rig's RigConfigTests (15 cases) — the contract for the
// shared .rig.json schema, JSONC tolerance, global+repo merge, the dotnet
// namespace fold, and the three custom-command forms.
package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) Config {
	t.Helper()
	c, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q) failed: %v", src, err)
	}
	return c
}

// 1. Unknown_keys_are_detected_with_a_suggestion
func TestUnknownKeysAreDetectedWithASuggestion(t *testing.T) {
	unknown := UnknownKeys(`{ "defualtProject": "App", "test": {}, "totallyMadeUp": 1 }`)

	if len(unknown) != 2 {
		t.Fatalf("got %d unknown keys, want 2: %v", len(unknown), unknown)
	}
	byKey := map[string]string{}
	for _, u := range unknown {
		byKey[u.Key] = u.Suggestion
	}
	if got, ok := byKey["defualtProject"]; !ok || got != "defaultProject" {
		t.Errorf(`defualtProject suggestion = %q (present=%v), want "defaultProject"`, got, ok)
	}
	// a far-off key still reports, but without a (misleading) suggestion
	if got, ok := byKey["totallyMadeUp"]; !ok || got != "" {
		t.Errorf(`totallyMadeUp suggestion = %q (present=%v), want ""`, got, ok)
	}
}

// 2. Known_keys_produce_no_warnings
func TestKnownKeysProduceNoWarnings(t *testing.T) {
	if unknown := UnknownKeys(`{ "$schema": "x", "solution": "a.slnx", "aliases": {} }`); len(unknown) != 0 {
		t.Fatalf("got unknown keys %v, want none", unknown)
	}
}

// 3. Merge_lets_the_repo_win_per_key_and_unions_dictionaries
func TestMergeLetsTheRepoWinPerKeyAndUnionsDictionaries(t *testing.T) {
	global := mustParse(t, `
		{
		  "defaultProject": "GlobalApp",
		  "env": { "SHARED": "g", "ONLY_GLOBAL": "g" },
		  "aliases": { "coverage": "cov", "publish": "ship" }
		}`)
	repo := mustParse(t, `
		{
		  "defaultProject": "RepoApp",
		  "env": { "SHARED": "r", "ONLY_REPO": "r" },
		  "aliases": { "coverage": "c" }
		}`)

	merged := Merge(global, repo)

	if merged.DefaultProject != "RepoApp" { // repo wins
		t.Errorf("DefaultProject = %q, want RepoApp", merged.DefaultProject)
	}
	if merged.Env["SHARED"] != "r" { // repo wins per key
		t.Errorf(`Env["SHARED"] = %q, want "r"`, merged.Env["SHARED"])
	}
	if merged.Env["ONLY_GLOBAL"] != "g" { // global preserved
		t.Errorf(`Env["ONLY_GLOBAL"] = %q, want "g"`, merged.Env["ONLY_GLOBAL"])
	}
	if merged.Env["ONLY_REPO"] != "r" { // repo added
		t.Errorf(`Env["ONLY_REPO"] = %q, want "r"`, merged.Env["ONLY_REPO"])
	}
	if merged.Aliases["coverage"] != "c" { // repo override
		t.Errorf(`Aliases["coverage"] = %q, want "c"`, merged.Aliases["coverage"])
	}
	if merged.Aliases["publish"] != "ship" { // global-only kept
		t.Errorf(`Aliases["publish"] = %q, want "ship"`, merged.Aliases["publish"])
	}
}

// 4. Merge_unions_exclude_lists_and_repo_quiet_wins
func TestMergeUnionsExcludeListsAndRepoQuietWins(t *testing.T) {
	global := mustParse(t, `{ "exclude": ["*Bench"], "quiet": true }`)
	repo := mustParse(t, `{ "exclude": ["*.Demo", "*Bench"] }`)

	merged := Merge(global, repo)

	want := map[string]bool{"*Bench": true, "*.Demo": true}
	if len(merged.Exclude) != 2 {
		t.Fatalf("Exclude = %v, want union of *Bench and *.Demo, de-duped", merged.Exclude)
	}
	for _, e := range merged.Exclude {
		if !want[e] {
			t.Errorf("unexpected exclude entry %q", e)
		}
	}
	if !merged.IsQuiet() { // inherited from global (repo unset)
		t.Error("Quiet should be true (inherited from global)")
	}
}

// 5. Merge_blank_repo_license_does_not_shadow_the_global_one
func TestMergeBlankRepoLicenseDoesNotShadowTheGlobalOne(t *testing.T) {
	// The repo's scaffolded `coverage.license: ""` must fall through to the
	// real key set once in ~/.rig.json — the whole point of a global config.
	global := mustParse(t, `{ "coverage": { "license": "PRO-KEY" } }`)
	repo := mustParse(t, `{ "coverage": { "license": "", "collector": "mtp" } }`)

	merged := Merge(global, repo)

	if merged.Coverage == nil {
		t.Fatal("Coverage is nil")
	}
	if merged.Coverage.License != "PRO-KEY" { // blank "" treated as unset
		t.Errorf("License = %q, want PRO-KEY", merged.Coverage.License)
	}
	if merged.Coverage.Collector != "mtp" { // repo's real value still wins
		t.Errorf("Collector = %q, want mtp", merged.Coverage.Collector)
	}
}

// 6. Empty_whitespace_or_malformed_config_degrades_to_defaults_without_throwing
func TestEmptyWhitespaceOrMalformedConfigDegradesToDefaultsWithoutThrowing(t *testing.T) {
	if c := mustParse(t, ""); c.DefaultProject != "" {
		t.Errorf("Parse(\"\") DefaultProject = %q, want empty", c.DefaultProject)
	}
	if c := mustParse(t, "   \n "); c.DefaultProject != "" {
		t.Errorf("Parse(whitespace) DefaultProject = %q, want empty", c.DefaultProject)
	}

	dir := t.TempDir()
	// 0-byte file
	if err := os.WriteFile(filepath.Join(dir, FileName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(dir); err != nil || c.DefaultProject != "" {
		t.Errorf("Load(empty file) = (%+v, %v), want defaults and nil error", c, err)
	}
	// malformed
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil || c.DefaultProject != "" {
		t.Errorf("Load(malformed) = (%+v, %v), want defaults and nil error", c, err)
	}
	if len(c.Warnings) == 0 {
		t.Error("Load(malformed) should carry a warning explaining the degrade")
	}
}

// 7. Missing_file_yields_defaults
func TestMissingFileYieldsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "no", "such", "dir"))
	if err != nil {
		t.Fatalf("Load on a missing file errored: %v", err)
	}
	if cfg.DefaultProject != "" {
		t.Errorf("DefaultProject = %q, want empty", cfg.DefaultProject)
	}
	if cfg.Commands != nil {
		t.Errorf("Commands = %v, want nil", cfg.Commands)
	}
}

// 8. Parses_full_schema_with_jsonc_comments_and_trailing_commas
func TestParsesFullSchemaWithJSONCCommentsAndTrailingCommas(t *testing.T) {
	cfg := mustParse(t, `
		{
		  // a JSONC comment
		  "$schema": "ignored",
		  "solution": "App.slnx",
		  "defaultProject": "App.Desktop",
		  "test": {
		    "project": "tests/App.Tests/App.Tests.csproj",
		    "envPresets": { "log": { "APP_LOG": "1" } }
		  },
		  "coverage": { "settings": "cov.runsettings", "collector": "auto", "license": "KEY", "open": true, "full": false, "min": 80 },
		  "kill": { "match": ["App.Desktop"] },
		  "rebuild": { "skip": ["vendor", "node_modules"] },
		  "publish": { "rid": "osx-arm64", "selfContained": true, "singleFile": false, "output": "dist/{rid}" },
		  "env": { "GLOBAL": "g" },
		}`)

	if cfg.Solution != "App.slnx" {
		t.Errorf("Solution = %q", cfg.Solution)
	}
	if cfg.DefaultProject != "App.Desktop" {
		t.Errorf("DefaultProject = %q", cfg.DefaultProject)
	}
	if cfg.Test == nil || cfg.Test.Project != "tests/App.Tests/App.Tests.csproj" {
		t.Errorf("Test = %+v", cfg.Test)
	}
	if cfg.Test.EnvPresets["log"]["APP_LOG"] != "1" {
		t.Errorf("Test.EnvPresets = %v", cfg.Test.EnvPresets)
	}
	cov := cfg.Coverage
	if cov == nil || cov.License != "KEY" || cov.Collector != "auto" {
		t.Fatalf("Coverage = %+v", cov)
	}
	if cov.Open == nil || !*cov.Open {
		t.Error("Coverage.Open should be true")
	}
	if cov.Full == nil || *cov.Full {
		t.Error("Coverage.Full should be false (and set)")
	}
	if cov.Min == nil || *cov.Min != 80 {
		t.Errorf("Coverage.Min = %v, want 80", cov.Min)
	}
	if len(cfg.Kill.Match) != 1 || cfg.Kill.Match[0] != "App.Desktop" {
		t.Errorf("Kill.Match = %v", cfg.Kill.Match)
	}
	if cfg.Rebuild == nil || strings.Join(cfg.Rebuild.Skip, ",") != "vendor,node_modules" {
		t.Errorf("Rebuild = %+v", cfg.Rebuild)
	}
	if cfg.Publish == nil || cfg.Publish.Rid != "osx-arm64" {
		t.Fatalf("Publish = %+v", cfg.Publish)
	}
	if cfg.Publish.SelfContained == nil || !*cfg.Publish.SelfContained {
		t.Error("Publish.SelfContained should be true")
	}
	if cfg.Env["GLOBAL"] != "g" {
		t.Errorf("Env = %v", cfg.Env)
	}
}

// 9. Folds_the_dotnet_namespace_and_top_level_envPresets_onto_canonical_fields
func TestFoldsTheDotnetNamespaceAndTopLevelEnvPresetsOntoCanonicalFields(t *testing.T) {
	cfg := mustParse(t, `
		{
		  "defaultProject": "App.Desktop",
		  "envPresets": { "log": { "APP_LOG": "1" } },
		  "coverage": { "open": true, "min": 80 },
		  "dotnet": {
		    "solution": "App.slnx",
		    "test": { "project": "tests/App.Tests/App.Tests.csproj" },
		    "coverage": { "settings": "cov.runsettings", "collector": "auto", "license": "KEY" },
		    "rebuild": { "skip": ["vendor"] },
		    "publish": { "rid": "osx-arm64", "selfContained": true }
		  }
		}`)

	// dotnet.* folds onto the canonical top-level fields verbs read.
	if cfg.Solution != "App.slnx" {
		t.Errorf("Solution = %q", cfg.Solution)
	}
	if cfg.Test == nil || cfg.Test.Project != "tests/App.Tests/App.Tests.csproj" {
		t.Fatalf("Test = %+v", cfg.Test)
	}
	cov := cfg.Coverage
	if cov == nil || cov.Settings != "cov.runsettings" || cov.Collector != "auto" || cov.License != "KEY" {
		t.Fatalf("Coverage = %+v", cov)
	}
	// shared coverage knobs stay top-level.
	if cov.Open == nil || !*cov.Open {
		t.Error("Coverage.Open should be true")
	}
	if cov.Min == nil || *cov.Min != 80 {
		t.Errorf("Coverage.Min = %v, want 80", cov.Min)
	}
	if cfg.Rebuild == nil || strings.Join(cfg.Rebuild.Skip, ",") != "vendor" {
		t.Errorf("Rebuild = %+v", cfg.Rebuild)
	}
	if cfg.Publish == nil || cfg.Publish.Rid != "osx-arm64" {
		t.Fatalf("Publish = %+v", cfg.Publish)
	}
	if cfg.Publish.SelfContained == nil || !*cfg.Publish.SelfContained {
		t.Error("Publish.SelfContained should be true")
	}
	// top-level envPresets folds onto test.envPresets.
	if cfg.Test.EnvPresets["log"]["APP_LOG"] != "1" {
		t.Errorf("Test.EnvPresets = %v", cfg.Test.EnvPresets)
	}
	// the transient namespace is consumed.
	if cfg.Dotnet != nil {
		t.Errorf("Dotnet = %+v, want nil after normalize", cfg.Dotnet)
	}
}

// 10. Dotnet_namespace_wins_over_legacy_top_level_keys
func TestDotnetNamespaceWinsOverLegacyTopLevelKeys(t *testing.T) {
	cfg := mustParse(t, `
		{
		  "solution": "Legacy.slnx",
		  "dotnet": { "solution": "New.slnx" }
		}`)

	if cfg.Solution != "New.slnx" {
		t.Errorf("Solution = %q, want New.slnx", cfg.Solution)
	}
}

// 11. A_node_namespace_is_ignored_not_flagged_as_unknown
func TestANodeNamespaceIsIgnoredNotFlaggedAsUnknown(t *testing.T) {
	json := `{ "node": { "anything": true }, "defaultProject": "App" }`

	if unknown := UnknownKeys(json); len(unknown) != 0 {
		t.Errorf("UnknownKeys = %v, want none", unknown)
	}
	if cfg := mustParse(t, json); cfg.DefaultProject != "App" {
		t.Errorf("DefaultProject = %q, want App", cfg.DefaultProject)
	}
}

// 12. Command_string_form_is_a_shell_command
func TestCommandStringFormIsAShellCommand(t *testing.T) {
	cfg := mustParse(t, `{ "commands": { "deploy": "./deploy.sh --prod" } }`)

	spec := cfg.Commands["deploy"].Resolve()
	if spec == nil || !spec.IsShell() {
		t.Fatalf("Resolve() = %+v, want shell form", spec)
	}
	if spec.Shell != "./deploy.sh --prod" {
		t.Errorf("Shell = %q", spec.Shell)
	}
}

// 13. Command_array_form_bypasses_the_shell
func TestCommandArrayFormBypassesTheShell(t *testing.T) {
	cfg := mustParse(t, `{ "commands": { "fmt": ["dotnet", "csharpier", "."] } }`)

	spec := cfg.Commands["fmt"].Resolve()
	if spec == nil || spec.IsShell() {
		t.Fatalf("Resolve() = %+v, want argv form", spec)
	}
	if strings.Join(spec.Argv, " ") != "dotnet csharpier ." {
		t.Errorf("Argv = %v", spec.Argv)
	}
}

// 14. Command_object_form_with_description_env_and_cwd
func TestCommandObjectFormWithDescriptionEnvAndCwd(t *testing.T) {
	cfg := mustParse(t, `
		{
		  "commands": {
		    "release": {
		      "description": "Cut a release",
		      "command": "./release.sh",
		      "env": { "CI": "true" },
		      "cwd": "scripts"
		    }
		  }
		}`)

	def := cfg.Commands["release"]
	if def.Description != "Cut a release" {
		t.Errorf("Description = %q", def.Description)
	}
	if def.Cwd != "scripts" {
		t.Errorf("Cwd = %q", def.Cwd)
	}
	if def.Env["CI"] != "true" {
		t.Errorf("Env = %v", def.Env)
	}
	if spec := def.Resolve(); spec == nil || spec.Shell != "./release.sh" {
		t.Errorf("Resolve() = %+v, want shell ./release.sh", spec)
	}
}

// 15. Command_object_resolves_per_os_override
func TestCommandObjectResolvesPerOSOverride(t *testing.T) {
	cfg := mustParse(t, `
		{
		  "commands": {
		    "package": {
		      "os": {
		        "macos": "./build-mac.sh",
		        "windows": ["pwsh", "build.ps1"],
		        "linux": "./build-linux.sh"
		      }
		    }
		  }
		}`)

	resolved := cfg.Commands["package"].Resolve()
	if resolved == nil {
		t.Fatal("Resolve() = nil")
	}
	switch runtime.GOOS {
	case "darwin":
		if resolved.Shell != "./build-mac.sh" {
			t.Errorf("Shell = %q, want ./build-mac.sh", resolved.Shell)
		}
	case "windows":
		if strings.Join(resolved.Argv, " ") != "pwsh build.ps1" {
			t.Errorf("Argv = %v, want [pwsh build.ps1]", resolved.Argv)
		}
	default:
		if resolved.Shell != "./build-linux.sh" {
			t.Errorf("Shell = %q, want ./build-linux.sh", resolved.Shell)
		}
	}
}
