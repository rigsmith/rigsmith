package scripts

import (
	"bytes"
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

	// The checked-in resources are what actually ships, and a stale or
	// hand-edited one would pass a JSON check while go run stays blocked. The
	// manifest is embedded as plain XML, so read it back out of each object.
	for _, installer := range []string{"dev-install", "source-install"} {
		for _, arch := range []string{"amd64", "arm64"} {
			path := filepath.Join(installer, "rsrc_windows_"+arch+".syso")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("%s: %v", path, err)
				continue
			}
			for _, want := range []string{`<requestedExecutionLevel level="asInvoker"`, `<longPathAware`, `>true</longPathAware>`} {
				if !bytes.Contains(data, []byte(want)) {
					t.Errorf("%s does not embed %s — regenerate it with go generate in %s", path, want, installer)
				}
			}
			if bytes.Contains(data, []byte(`requireAdministrator`)) || bytes.Contains(data, []byte(`highestAvailable`)) {
				t.Errorf("%s asks for elevation, which is what the manifest exists to stop", path)
			}
		}
	}
}
