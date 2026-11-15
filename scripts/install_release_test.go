package scripts

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// family is what `install.sh all` installs on darwin/linux.
var family = []string{"rig", "shiprig", "clauderig", "codexrig", "changerig", "brewrig"}

// fakeRelease is a release served to install.sh by a fake curl: every asset is a
// file in assets, keyed by its name; anything else is a 404. A fake tar turns an
// archive into a stub binary named after it, and a fake cosign, when present,
// accepts only the bundle the release was signed with.
type fakeRelease struct {
	t      *testing.T
	tools  string // fake curl/tar/cosign, first on PATH
	assets string // the release's files
	urls   string // every URL curl was asked for
	prefix string
	data   string
	cosign bool
	// cosignVersion is what the fake cosign reports (default v3.1.3, the
	// first v3 without GHSA-fx35-mq7g-6g98).
	cosignVersion string
	extraEnv      []string
}

func newFakeRelease(t *testing.T) *fakeRelease {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX release installer")
	}
	r := &fakeRelease{t: t, tools: t.TempDir(), assets: t.TempDir(), prefix: t.TempDir(), data: t.TempDir(), cosign: true}
	r.urls = filepath.Join(r.data, "urls")
	r.write(r.tools, "curl", `#!/bin/sh
url=""
out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) shift; out="$1" ;;
    http*) url="$1" ;;
  esac
  shift
done
echo "$url" >> "`+r.urls+`"
src="`+r.assets+`/${url##*/}"
[ -f "$src" ] || exit 22
cp "$src" "$out"
`)
	r.write(r.tools, "tar", `#!/bin/sh
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
`)
	// The release: an archive per tool, a checksums file over them, and a
	// signature bundle the fake cosign accepts.
	var sums strings.Builder
	for _, bin := range family {
		name := bin + "_0.0.1_" + runtime.GOOS + "_" + goarch() + ".tar.gz"
		body := "archive of " + bin
		r.write(r.assets, name, body)
		sums.WriteString(sha256Hex(body) + "  " + name + "\n")
	}
	r.write(r.assets, "checksums.txt", sums.String())
	r.write(r.assets, "checksums.txt.sigstore.json", `{"signed":"by the release workflow"}`)
	return r
}

func (r *fakeRelease) write(dir, name, body string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		r.t.Fatal(err)
	}
}

// run runs install.sh all against the fake release. PATH is the fake tools plus
// the system dirs only, so a real cosign elsewhere can't stand in for the fake.
func (r *fakeRelease) run() (string, error) {
	r.t.Helper()
	if r.cosign {
		version := r.cosignVersion
		if version == "" {
			version = "v3.1.3"
		}
		// Like the real one, a v2 can read the release's bundle only when told
		// it is the new format.
		r.write(r.tools, "cosign", `#!/bin/sh
if [ "$1" = version ]; then printf '  COSIGN\nGitVersion:    %s\n' "`+version+`"; exit 0; fi
bundle=""; newfmt=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --bundle) bundle="$2" ;;
    --new-bundle-format) newfmt=1 ;;
  esac
  shift
done
case "`+version+`" in v2.*) [ "$newfmt" = 1 ] || { echo "bundle: unknown format" >&2; exit 1; } ;; esac
grep -q 'by the release workflow' "$bundle" || { echo "invalid signature" >&2; exit 1; }
`)
	}
	cmd := exec.Command("sh", "install.sh", "all")
	cmd.Env = append(os.Environ(),
		"PATH="+r.tools+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin",
		"RIGSMITH_VERSION=v0.0.1", "RIGSMITH_INSTALL="+r.prefix, "XDG_DATA_HOME="+r.data,
		// A developer's own overrides must not leak in: an inherited
		// RIGSMITH_BIN_DIR would send the fake binaries over their real tools.
		"RIGSMITH_REQUIRE_SIGNATURE=", "RIGSMITH_BIN_DIR=", "RIGSMITH_REPO=")
	cmd.Env = append(cmd.Env, r.extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (r *fakeRelease) installed() []string {
	entries, _ := os.ReadDir(filepath.Join(r.prefix, "bin"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func goarch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}

func TestReleaseInstallerRefreshesFamilyZshCompletions(t *testing.T) {
	r := newFakeRelease(t)
	if out, err := r.run(); err != nil {
		t.Fatalf("release installer: %v\n%s", err, out)
	}
	for _, name := range []string{"rig", "shiprig", "changerig", "clauderig"} {
		path := filepath.Join(r.data, "rigsmith", "zsh", "site-functions", "_"+name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s completion missing: %v", name, err)
		}
	}
}

// A release whose archives match its signed checksums installs the family.
func TestReleaseInstallerInstallsAVerifiedRelease(t *testing.T) {
	r := newFakeRelease(t)
	out, err := r.run()
	if err != nil {
		t.Fatalf("release installer: %v\n%s", err, out)
	}
	if got := strings.Join(r.installed(), " "); len(r.installed()) != len(family) {
		t.Fatalf("installed %q, want the whole family", got)
	}
	for _, want := range []string{"verified the signature on checksums.txt", "every archive matches"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// One tampered archive installs nothing — not even the tools before it.
func TestReleaseInstallerRefusesATamperedArchive(t *testing.T) {
	r := newFakeRelease(t)
	r.write(r.assets, "shiprig_0.0.1_"+runtime.GOOS+"_"+goarch()+".tar.gz", "something else entirely")
	out, err := r.run()
	if err == nil {
		t.Fatalf("a tampered archive installed:\n%s", out)
	}
	if !strings.Contains(out, "does not match checksums.txt") {
		t.Errorf("the failure doesn't say why:\n%s", out)
	}
	if got := r.installed(); len(got) != 0 {
		t.Fatalf("installed %v from a release with a tampered archive", got)
	}
}

func TestReleaseInstallerRefusesAMissingSignature(t *testing.T) {
	r := newFakeRelease(t)
	if err := os.Remove(filepath.Join(r.assets, "checksums.txt.sigstore.json")); err != nil {
		t.Fatal(err)
	}
	out, err := r.run()
	if err == nil || !strings.Contains(out, "has no signature") {
		t.Fatalf("a release without a signature was not refused: %v\n%s", err, out)
	}
	if got := r.installed(); len(got) != 0 {
		t.Fatalf("installed %v from an unsigned release", got)
	}
}

func TestReleaseInstallerRefusesAnInvalidSignature(t *testing.T) {
	r := newFakeRelease(t)
	r.write(r.assets, "checksums.txt.sigstore.json", `{"signed":"by someone else"}`)
	out, err := r.run()
	if err == nil || !strings.Contains(out, "does not verify") {
		t.Fatalf("an invalid signature was not refused: %v\n%s", err, out)
	}
	if got := r.installed(); len(got) != 0 {
		t.Fatalf("installed %v under an invalid signature", got)
	}
}

// Without cosign the checksums are still checked and the script says what it
// could not check; RIGSMITH_REQUIRE_SIGNATURE=1 turns that into a refusal.
func TestReleaseInstallerWithoutCosign(t *testing.T) {
	if _, err := exec.LookPath("/usr/bin/cosign"); err == nil {
		t.Skip("a cosign in /usr/bin would be found on the test's PATH")
	}
	r := newFakeRelease(t)
	r.cosign = false
	out, err := r.run()
	if err != nil {
		t.Fatalf("without cosign the checksums alone should install: %v\n%s", err, out)
	}
	if !strings.Contains(out, "cosign is not installed") || !strings.Contains(out, "RIGSMITH_REQUIRE_SIGNATURE=1") {
		t.Errorf("the fallback isn't said plainly:\n%s", out)
	}

	strict := newFakeRelease(t)
	strict.cosign = false
	strict.extraEnv = []string{"RIGSMITH_REQUIRE_SIGNATURE=1"}
	out, err = strict.run()
	if err == nil || len(strict.installed()) != 0 {
		t.Fatalf("RIGSMITH_REQUIRE_SIGNATURE=1 installed without a signature check:\n%s", out)
	}
}

// `<tool> self-update` points the installer at the directory the running tool
// lives in and at the repo it checked. RIGSMITH_BIN_DIR has to win over the
// prefix, and RIGSMITH_REPO has to reach every download URL — otherwise a
// fork's self-update would install upstream's release.
func TestReleaseInstallerHonoursBinDirAndRepo(t *testing.T) {
	r := newFakeRelease(t)
	binDir := filepath.Join(t.TempDir(), "custom tools")
	r.extraEnv = []string{"RIGSMITH_BIN_DIR=" + binDir, "RIGSMITH_REPO=acme/fork"}
	if out, err := r.run(); err != nil {
		t.Fatalf("release installer: %v\n%s", err, out)
	}
	for _, name := range family {
		if _, err := os.Stat(filepath.Join(binDir, name)); err != nil {
			t.Errorf("%s not installed into RIGSMITH_BIN_DIR: %v", name, err)
		}
	}
	if got := r.installed(); len(got) > 0 {
		t.Errorf("RIGSMITH_BIN_DIR set, yet the prefix's bin got %v", got)
	}
	raw, err := os.ReadFile(r.urls)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Fields(string(raw)) {
		if !strings.HasPrefix(line, "https://github.com/acme/fork/releases/download/v0.0.1/") {
			t.Errorf("a download ignored RIGSMITH_REPO: %s", line)
		}
	}
}

// Only a cosign that can be trusted checks the signature: one with the
// GHSA-fx35-mq7g-6g98 bypass counts as none (said, and refused under
// RIGSMITH_REQUIRE_SIGNATURE=1), and a fixed v2 is told the bundle format.
func TestReleaseInstallerChecksTheCosignVersion(t *testing.T) {
	old := newFakeRelease(t)
	old.cosignVersion = "v3.0.6"
	out, err := old.run()
	if err != nil || len(old.installed()) != len(family) {
		t.Fatalf("an untrusted cosign should fall back to the checksums: %v\n%s", err, out)
	}
	if !strings.Contains(out, "GHSA-fx35-mq7g-6g98") || strings.Contains(out, "verified the signature") {
		t.Errorf("v3.0.6 was trusted, or not said to be untrusted:\n%s", out)
	}

	strict := newFakeRelease(t)
	strict.cosignVersion = "v2.6.4"
	strict.extraEnv = []string{"RIGSMITH_REQUIRE_SIGNATURE=1"}
	if out, err := strict.run(); err == nil || len(strict.installed()) != 0 || !strings.Contains(out, "upgrade to v2.6.5") {
		t.Fatalf("RIGSMITH_REQUIRE_SIGNATURE=1 accepted cosign v2.6.4: %v\n%s", err, out)
	}

	v2 := newFakeRelease(t)
	v2.cosignVersion = "v2.6.5"
	out, err = v2.run()
	if err != nil || !strings.Contains(out, "verified the signature on checksums.txt") {
		t.Fatalf("cosign v2.6.5 should verify with --new-bundle-format: %v\n%s", err, out)
	}
}
