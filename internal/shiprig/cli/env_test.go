package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReleaseEnv(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("NPM_TOKEN=from-dotenv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMBIENT_TOKEN", "from-shell")

	// Default: the .env layer is loaded and sits under the ambient env.
	env, err := loadReleaseEnv(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if env["NPM_TOKEN"] != "from-dotenv" {
		t.Errorf("NPM_TOKEN = %q, want the .env value", env["NPM_TOKEN"])
	}
	if env["AMBIENT_TOKEN"] != "from-shell" {
		t.Errorf("AMBIENT_TOKEN = %q, want the ambient value", env["AMBIENT_TOKEN"])
	}

	// --no-env drops the file layer but keeps the ambient environment.
	noEnvResult, err := loadReleaseEnv(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := noEnvResult["NPM_TOKEN"]; ok {
		t.Error("--no-env should drop the .env file layer")
	}
	if noEnvResult["AMBIENT_TOKEN"] != "from-shell" {
		t.Error("--no-env should keep the ambient environment")
	}
}

func TestLoadReleaseEnvAmbientWins(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=from-dotenv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOKEN", "from-shell")

	env, err := loadReleaseEnv(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if env["TOKEN"] != "from-shell" {
		t.Errorf("TOKEN = %q, want the exported value to win over .env", env["TOKEN"])
	}
}
