package scripts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallerManifestDisablesFilenameElevation(t *testing.T) {
	raw, err := os.ReadFile("install-winres.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]map[string]map[string]map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	manifest := cfg["RT_MANIFEST"]["#1"]["0409"]
	if got := manifest["execution-level"]; got != "as invoker" {
		t.Fatalf("execution-level = %v, want as invoker so go run is not blocked by Windows installer detection", got)
	}

	for _, installer := range []string{"dev-install", "source-install"} {
		for _, arch := range []string{"amd64", "arm64"} {
			path := filepath.Join(installer, "rsrc_windows_"+arch+".syso")
			if _, err := os.Stat(path); err != nil {
				t.Errorf("%s: %v", path, err)
			}
		}
	}
}
