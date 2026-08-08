package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReleaseEnv(t *testing.T) {
	root := t.TempDir()
	// A unique, test-only key: a common name (e.g. NPM_TOKEN) present in the
	// runner's ambient env would win over .env (file < ambient) and flake this.
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SHIPRIG_DOTENV_TEST_TOKEN=from-dotenv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMBIENT_TOKEN", "from-shell")

	// Default: the .env layer is loaded and sits under the ambient env.
	env, err := loadReleaseEnv(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if env["SHIPRIG_DOTENV_TEST_TOKEN"] != "from-dotenv" {
		t.Errorf("SHIPRIG_DOTENV_TEST_TOKEN = %q, want the .env value", env["SHIPRIG_DOTENV_TEST_TOKEN"])
	}
	if env["AMBIENT_TOKEN"] != "from-shell" {
		t.Errorf("AMBIENT_TOKEN = %q, want the ambient value", env["AMBIENT_TOKEN"])
	}

	// --no-env drops the file layer but keeps the ambient environment.
	noEnvResult, err := loadReleaseEnv(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := noEnvResult["SHIPRIG_DOTENV_TEST_TOKEN"]; ok {
		t.Error("--no-env should drop the .env file layer")
	}
	if noEnvResult["AMBIENT_TOKEN"] != "from-shell" {
		t.Error("--no-env should keep the ambient environment")
	}
}

func TestApplyReleaseEnvExportsFileLayer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"),
		[]byte("SHIPRIG_APPLY_TEST_TOKEN=from-dotenv\nSHIPRIG_APPLY_TEST_AMBIENT=from-dotenv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// t.Setenv records the pre-test state, so the exports applyReleaseEnv makes
	// on these keys are restored when the test ends.
	t.Setenv("SHIPRIG_APPLY_TEST_TOKEN", "")
	if err := os.Unsetenv("SHIPRIG_APPLY_TEST_TOKEN"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHIPRIG_APPLY_TEST_AMBIENT", "from-shell")

	if _, err := applyReleaseEnv(root, false); err != nil {
		t.Fatal(err)
	}
	// os.Getenv is the lookup the dotnet adapter (NUGET_API_KEY), core/auth's
	// `env:NAME` refs, and every inherited subprocess make.
	if got := os.Getenv("SHIPRIG_APPLY_TEST_TOKEN"); got != "from-dotenv" {
		t.Errorf("os.Getenv after applyReleaseEnv = %q, want the .env value", got)
	}
	if got := os.Getenv("SHIPRIG_APPLY_TEST_AMBIENT"); got != "from-shell" {
		t.Errorf("exported value = %q, want the ambient export to win over .env", got)
	}
}

func TestApplyReleaseEnvNoEnv(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SHIPRIG_APPLY_NOENV_TOKEN=from-dotenv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHIPRIG_APPLY_NOENV_TOKEN", "")
	if err := os.Unsetenv("SHIPRIG_APPLY_NOENV_TOKEN"); err != nil {
		t.Fatal(err)
	}

	if _, err := applyReleaseEnv(root, true); err != nil {
		t.Fatal(err)
	}
	if v, ok := os.LookupEnv("SHIPRIG_APPLY_NOENV_TOKEN"); ok {
		t.Errorf("--no-env exported %q; the file layer must be skipped", v)
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
