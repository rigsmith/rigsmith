package cfgfind

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func spec(root string) Spec {
	cs := filepath.Join(root, ".changeset")
	return Spec{
		Label: "release config",
		Probe: []DirNames{
			{Dir: cs, Names: []string{"release", "shiprig"}},
			{Dir: root, Names: []string{"release", "shiprig"}},
		},
		RigPath: filepath.Join(root, ".rig.json"),
		RigKeys: []string{"shiprig", "release"},
	}
}

func TestFind_NoneUsesDefaults(t *testing.T) {
	src, err := Find(spec(t.TempDir()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src != nil {
		t.Fatalf("want nil source (use defaults), got %+v", src)
	}
}

func TestFind_SingleFile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".changeset"), "release.jsonc", `{"tool":"shiprig"}`)

	src, err := Find(spec(root))
	if err != nil {
		t.Fatal(err)
	}
	if src == nil || !strings.Contains(string(src.Data), "shiprig") {
		t.Fatalf("want the release.jsonc contents, got %+v", src)
	}
	if src.BaseDir != filepath.Join(root, ".changeset") {
		t.Errorf("BaseDir = %q, want the .changeset dir", src.BaseDir)
	}
}

func TestFind_RigKeyInline(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".rig.json", `{
		// a comment, since .rig.json is JSONC
		"shiprig": { "tool": "shiprig" }
	}`)

	src, err := Find(spec(root))
	if err != nil {
		t.Fatal(err)
	}
	if src == nil || !strings.Contains(string(src.Data), `"tool"`) {
		t.Fatalf("want the inline shiprig key, got %+v", src)
	}
	if src.BaseDir != root {
		t.Errorf("BaseDir = %q, want repo root for an inline key", src.BaseDir)
	}
}

// keyedSpec resolves the release config either as a whole file or as a `release`
// key inside config.{jsonc,json} — the unified-file scenario.
func keyedSpec(root string) Spec {
	cs := filepath.Join(root, ".changeset")
	s := spec(root)
	s.Keyed = []KeyedProbe{
		{Dir: cs, Names: []string{"config", "changerig"}, Keys: []string{"release"}},
	}
	return s
}

func TestFind_KeyedInsideOtherFile(t *testing.T) {
	root := t.TempDir()
	cs := filepath.Join(root, ".changeset")
	write(t, cs, "config.json", `{
		"versioning": { "source": "commits" },
		"release": { "tool": "shiprig" }
	}`)

	src, err := Find(keyedSpec(root))
	if err != nil {
		t.Fatal(err)
	}
	if src == nil || !strings.Contains(string(src.Data), `"tool"`) {
		t.Fatalf("want the embedded release key, got %+v", src)
	}
	if src.Path != "" {
		t.Errorf("Path = %q, want empty for a keyed source", src.Path)
	}
	if src.File != filepath.Join(cs, "config.json") {
		t.Errorf("File = %q, want the containing config.json", src.File)
	}
}

func TestFind_KeyedAbsentLeavesWholeFile(t *testing.T) {
	// A config.json with no `release` key must not produce a release candidate,
	// so a standalone release.jsonc resolves cleanly (existing two-file setup).
	root := t.TempDir()
	cs := filepath.Join(root, ".changeset")
	write(t, cs, "config.json", `{ "versioning": { "source": "commits" } }`)
	write(t, cs, "release.jsonc", `{ "tool": "shiprig" }`)

	src, err := Find(keyedSpec(root))
	if err != nil {
		t.Fatalf("two-file setup should resolve cleanly: %v", err)
	}
	if src == nil || src.Path != filepath.Join(cs, "release.jsonc") {
		t.Fatalf("want the standalone release.jsonc, got %+v", src)
	}
}

func TestFind_KeyedAndWholeFileIsAmbiguous(t *testing.T) {
	root := t.TempDir()
	cs := filepath.Join(root, ".changeset")
	write(t, cs, "config.json", `{ "release": { "tool": "a" } }`)
	write(t, cs, "release.jsonc", `{ "tool": "b" }`)

	_, err := Find(keyedSpec(root))
	if err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("defining release twice should be a loud error, got %v", err)
	}
}

func TestFind_RigKeyNullSkipped(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".rig.json", `{"shiprig": null, "release": null}`)
	src, err := Find(spec(root))
	if err != nil || src != nil {
		t.Fatalf("null keys should not count: src=%+v err=%v", src, err)
	}
}

func TestFind_AmbiguousAcrossFiles(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".changeset"), "release.jsonc", `{}`)
	write(t, root, "shiprig.json", `{}`)

	_, err := Find(spec(root))
	if err == nil {
		t.Fatal("two config files should be ambiguous")
	}
	for _, want := range []string{"multiple release config", "release.jsonc", "shiprig.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestFind_AmbiguousJsonAndJsonc(t *testing.T) {
	root := t.TempDir()
	cs := filepath.Join(root, ".changeset")
	write(t, cs, "release.json", `{}`)
	write(t, cs, "release.jsonc", `{}`)

	if _, err := Find(spec(root)); err == nil {
		t.Fatal("release.json + release.jsonc should be ambiguous, not silently picked")
	}
}

func TestFind_UnreadableFileSurfacesError(t *testing.T) {
	root := t.TempDir()
	// A directory where a config file is expected: os.ReadFile fails with a
	// non-NotExist error, which must surface rather than be skipped.
	if err := os.MkdirAll(filepath.Join(root, ".changeset", "release.jsonc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Find(spec(root)); err == nil {
		t.Fatal("an unreadable config path should surface an error, not fall back to defaults")
	}
}

func TestFind_MalformedRigJSONSurfacesError(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".rig.json", `{ not: valid json `)
	_, err := Find(spec(root))
	if err == nil || !strings.Contains(err.Error(), ".rig.json") {
		t.Fatalf("a malformed .rig.json should surface a parse error naming it: %v", err)
	}
}

func TestFind_AmbiguityHintIsOptional(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".changeset"), "release.jsonc", `{}`)
	write(t, root, "shiprig.json", `{}`)

	s := spec(root)
	s.FlagHint = "--config"
	if _, err := Find(s); err == nil || !strings.Contains(err.Error(), "--config") {
		t.Fatalf("FlagHint should appear in the message: %v", err)
	}
	s.FlagHint = ""
	if _, err := Find(s); err == nil || strings.Contains(err.Error(), "--config") {
		t.Fatalf("no FlagHint → no --config in the message: %v", err)
	}
}

func TestFind_AmbiguousFileAndRigKey(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".changeset"), "release.jsonc", `{}`)
	write(t, root, ".rig.json", `{"shiprig": {}}`)

	_, err := Find(spec(root))
	if err == nil || !strings.Contains(err.Error(), ".rig.json") {
		t.Fatalf("a file + a .rig.json key should be ambiguous and name both: %v", err)
	}
}
