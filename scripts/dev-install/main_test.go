package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDevLauncherServesInstalledZshScriptBeforeBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher")
	}
	home := t.TempDir()
	file := filepath.Join(home, "rigsmith", "zsh", "site-functions", "_rig-dev")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	const script = "#compdef rig-dev\n# cached completion\n"
	if err := os.WriteFile(file, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(t.TempDir(), "rig-dev")
	if err := writeLauncher(launcher, devWrapperSh, map[string]string{
		"{{NAME}}": "rig", "{{ROUTE}}": "/nonexistent-route", "{{REPO}}": "/nonexistent-repo", "{{MOD}}": "./cmd/rig",
	}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(launcher, "completion", "zsh")
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != script {
		t.Fatalf("launcher ran build or returned wrong script: output=%q err=%v", strings.TrimSpace(string(out)), err)
	}
}

func TestDevInstallerWritesFamilyZshCompletions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("zsh installation is for Unix")
	}
	tools := t.TempDir()
	fakeGo := `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then
    shift
    cat > "$1" <<'EOF'
#!/bin/sh
name=$(basename "$0")
dir="${XDG_DATA_HOME}/rigsmith/zsh/site-functions"
mkdir -p "$dir"
printf '#compdef %s-dev\n' "$name" > "$dir/_${name}-dev"
EOF
    chmod +x "$1"
    exit 0
  fi
  shift
done
exit 1
`
	if err := os.WriteFile(filepath.Join(tools, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	bin := t.TempDir()
	data := t.TempDir()
	t.Setenv("RIGSMITH_DEV_BIN", bin)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("TMPDIR", t.TempDir())
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RIGSMITH_DEV_SRC", filepath.Clean(filepath.Join(root, "..", "..")))
	if err := run(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rig", "shiprig", "changerig", "clauderig"} {
		path := filepath.Join(data, "rigsmith", "zsh", "site-functions", "_"+name+"-dev")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s-dev completion missing: %v", name, err)
		}
	}
}
