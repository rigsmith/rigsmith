package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// `<tool> self-update` points the installer at the directory the running tool
// lives in and at the repo it checked. RIGSMITH_BIN_DIR has to win over the
// prefix, and RIGSMITH_REPO has to reach the download URL — otherwise a fork's
// self-update would install upstream's release.
func TestReleaseInstallerHonoursBinDirAndRepo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX release installer")
	}
	tools, work := t.TempDir(), t.TempDir()
	urls := filepath.Join(work, "urls")
	fakeCurl := `#!/bin/sh
url=""
out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) shift; out="$1" ;;
    http*) url="$1" ;;
  esac
  shift
done
echo "$url" >> "` + urls + `"
[ -n "$out" ] && : > "$out"
exit 0
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
printf '#!/bin/sh\nexit 0\n' > "$dir/$name"
chmod +x "$dir/$name"
`
	for name, body := range map[string]string{"curl": fakeCurl, "tar": fakeTar} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	prefix := t.TempDir()
	binDir := filepath.Join(t.TempDir(), "custom tools")
	cmd := exec.Command("sh", "install.sh", "all")
	cmd.Env = append(os.Environ(),
		"PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RIGSMITH_VERSION=v0.0.1", "RIGSMITH_INSTALL="+prefix, "RIGSMITH_BIN_DIR="+binDir,
		"RIGSMITH_REPO=acme/fork", "XDG_DATA_HOME="+work)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release installer: %v\n%s", err, out)
	}
	for _, name := range []string{"rig", "shiprig", "clauderig", "codexrig", "changerig", "brewrig"} {
		if _, err := os.Stat(filepath.Join(binDir, name)); err != nil {
			t.Errorf("%s not installed into RIGSMITH_BIN_DIR: %v", name, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(prefix, "bin")); len(entries) > 0 {
		t.Errorf("RIGSMITH_BIN_DIR set, yet the prefix's bin got %d file(s)", len(entries))
	}
	raw, err := os.ReadFile(urls)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://github.com/acme/fork/releases/download/v0.0.1/rig_0.0.1_"
	if !strings.Contains(string(raw), want) {
		t.Errorf("downloads did not use RIGSMITH_REPO; want a URL starting %q in:\n%s", want, raw)
	}
}
