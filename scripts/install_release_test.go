package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReleaseInstallerRefreshesFamilyZshCompletions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX release installer")
	}
	tools := t.TempDir()
	fakeCurl := `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then shift; : > "$1"; exit 0; fi
  shift
done
exit 1
`
	fakeTar := `#!/bin/sh
while [ "$#" -gt 0 ]; do
  case "$1" in
    -xzf) shift; archive="$1" ;;
    -C) shift; dir="$1" ;;
  esac
  shift
done
name=${archive##*/}
name=${name%%_*}
cat > "$dir/$name" <<'EOF'
#!/bin/sh
name=${0##*/}
dir="${XDG_DATA_HOME:-$HOME/.local/share}/rigsmith/zsh/site-functions"
mkdir -p "$dir"
printf '#compdef %s\n' "$name" > "$dir/_$name"
EOF
chmod +x "$dir/$name"
`
	for name, body := range map[string]string{"curl": fakeCurl, "tar": fakeTar} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	prefix, data := t.TempDir(), t.TempDir()
	cmd := exec.Command("sh", "install.sh", "all")
	cmd.Env = append(os.Environ(),
		"PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RIGSMITH_VERSION=v0.0.1", "RIGSMITH_INSTALL="+prefix, "XDG_DATA_HOME="+data)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release installer: %v\n%s", err, out)
	}
	for _, name := range []string{"rig", "shiprig", "changerig", "clauderig"} {
		path := filepath.Join(data, "rigsmith", "zsh", "site-functions", "_"+name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s completion missing: %v", name, err)
		}
	}
}
