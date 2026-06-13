package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNodeUsesVite(t *testing.T) {
	// vite.config.* signals Vite.
	a := t.TempDir()
	if err := os.WriteFile(filepath.Join(a, "vite.config.ts"), []byte("export default {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !NodeUsesVite(a) {
		t.Error("vite.config.ts should signal Vite")
	}

	// a vite devDependency signals Vite.
	b := t.TempDir()
	if err := os.WriteFile(filepath.Join(b, "package.json"), []byte(`{"devDependencies":{"vite":"^5"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !NodeUsesVite(b) {
		t.Error("vite devDependency should signal Vite")
	}

	// neither → not Vite.
	c := t.TempDir()
	if err := os.WriteFile(filepath.Join(c, "package.json"), []byte(`{"devDependencies":{"webpack":"^5"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if NodeUsesVite(c) {
		t.Error("non-vite project should not signal Vite")
	}
}

func TestViteDevPort(t *testing.T) {
	// default when nothing overrides it.
	def := t.TempDir()
	os.WriteFile(filepath.Join(def, "vite.config.ts"), []byte("export default {}"), 0o644)
	if got := ViteDevPort(def); got != 5173 {
		t.Errorf("default port = %d, want 5173", got)
	}

	// dev-script --port wins.
	scr := t.TempDir()
	os.WriteFile(filepath.Join(scr, "package.json"), []byte(`{"scripts":{"dev":"vite --port 4321"}}`), 0o644)
	if got := ViteDevPort(scr); got != 4321 {
		t.Errorf("dev-script port = %d, want 4321", got)
	}

	// vite.config server.port (best-effort).
	cfg := t.TempDir()
	os.WriteFile(filepath.Join(cfg, "vite.config.ts"), []byte("export default { server: { port: 5180 } }"), 0o644)
	if got := ViteDevPort(cfg); got != 5180 {
		t.Errorf("config port = %d, want 5180", got)
	}
}
