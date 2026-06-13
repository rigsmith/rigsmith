package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSdkSatisfies(t *testing.T) {
	tests := []struct {
		name      string
		installed string
		pinned    string
		want      bool
	}{
		{"no pin", "8.0.100", "", true},
		{"exact major", "8.0.100", "8.0.100", true},
		{"newer major ok", "9.0.100", "8.0.400", true},
		{"older major fails", "7.0.400", "8.0.100", false},
		{"unparseable installed defers to ok", "preview", "8.0.100", true},
		{"unparseable pin defers to ok", "8.0.100", "latest", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sdkSatisfies(tt.installed, tt.pinned); got != tt.want {
				t.Fatalf("sdkSatisfies(%q, %q) = %v, want %v", tt.installed, tt.pinned, got, tt.want)
			}
		})
	}
}

func TestMajorOf(t *testing.T) {
	tests := []struct {
		in   string
		want int
		ok   bool
	}{
		{"8.0.100", 8, true},
		{"10", 10, true},
		{" 9.0 ", 9, true},
		{"preview", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := majorOf(tt.in)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("major = %d, want %d", got, tt.want)
			}
		})
	}
}

// ---- ports of the .NET rig's DoctorTests (ReadSdkPin) ----

func TestSdkSatisfies_DefersToSatisfiedWhenAPinIsAbsentOrUnparseable(t *testing.T) {
	if !sdkSatisfies("9.0.100", "") {
		t.Fatal("absent pin must defer to satisfied")
	}
	if !sdkSatisfies("9.0.100", "   ") {
		t.Fatal("whitespace pin must defer to satisfied")
	}
	if !sdkSatisfies("not-a-version", "9.0.100") {
		t.Fatal("an unparseable installed version must defer to satisfied")
	}
}

func TestReadSdkPin_ReturnsThePinnedVersionOrEmpty(t *testing.T) {
	pinned := t.TempDir()
	writeFile(t, filepath.Join(pinned, "global.json"),
		`{ "sdk": { "version": "9.0.100", "rollForward": "latestMinor" } }`)
	if got := readSdkPin(pinned); got != "9.0.100" {
		t.Fatalf("pinned = %q, want 9.0.100", got)
	}

	// The nearest global.json wins, pin or not: one without sdk.version is no pin.
	unpinned := t.TempDir()
	writeFile(t, filepath.Join(unpinned, "global.json"),
		`{ "test": { "runner": "Microsoft.Testing.Platform" } }`)
	if got := readSdkPin(unpinned); got != "" {
		t.Fatalf("unpinned = %q, want empty", got)
	}
}

func TestReadSdkPin_FindsAGlobalJsonInAnAncestorDirectory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "global.json"), `{ "sdk": { "version": "8.0.0" } }`)
	nested := filepath.Join(root, "src", "App")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := readSdkPin(nested); got != "8.0.0" {
		t.Fatalf("nested = %q, want 8.0.0", got)
	}
}

// writeFile writes content to path, failing the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNodeHasDependencies(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if nodeHasDependencies(dir) {
		t.Error("no package.json → no dependencies")
	}
	write(`{"name":"x"}`)
	if nodeHasDependencies(dir) {
		t.Error("package.json without deps → false")
	}
	write(`{"dependencies":{"left-pad":"^1"}}`)
	if !nodeHasDependencies(dir) {
		t.Error("dependencies present → true")
	}
	write(`{"devDependencies":{"vitest":"^2"}}`)
	if !nodeHasDependencies(dir) {
		t.Error("devDependencies present → true")
	}
}
