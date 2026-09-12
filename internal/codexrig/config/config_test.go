package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// pathmap treats every OS token that is not "windows" as POSIX, so a typo in
// machines[*].os did not fail — it resolved paths for the wrong platform.
func TestLoadRejectsAnUnknownMachineOS(t *testing.T) {
	dir := writeConfig(t, `{"schema":1,"machines":{"mbp":{"name":"mbp","os":"macOS","home":"/Users/x"}}}`)
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "macOS") {
		t.Fatalf("err = %v, want a refusal naming the bad os", err)
	}
	for _, ok := range []string{"macos", "windows", "linux"} {
		dir := writeConfig(t, `{"schema":1,"machines":{"mbp":{"name":"mbp","os":"`+ok+`","home":"/Users/x"}}}`)
		if _, err := Load(dir); err != nil {
			t.Errorf("os %q refused: %v", ok, err)
		}
	}
}

// The schema is stamped on every write, and was never read: a config from a
// newer codexrig parsed as this version, with any renamed field read under
// its old meaning.
func TestLoadRefusesAConfigFromTheFuture(t *testing.T) {
	dir := writeConfig(t, `{"schema":99}`)
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "upgrade") {
		t.Fatalf("err = %v, want a refusal that says to upgrade", err)
	}
}

// The chunking tri-state: absent is not false. Default() is the machine with
// no opinion, so it must not carry one.
func TestDefaultHasNoOpinionOnChunking(t *testing.T) {
	if Default().ChunkRollouts != nil {
		t.Fatal("Default() sets chunkRollouts explicitly, so a new machine can never follow what the repo already does")
	}
}
