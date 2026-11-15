package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// fakeGitHub serves site-installers.sh's requests from a directory: each URL is
// a file named after it (sanitizeURL), the /releases/latest redirect resolves
// to latestTag, and anything else is a 404.
type fakeGitHub struct {
	t         *testing.T
	tools     string
	files     string
	latestTag string // "" makes the latest release unreadable
}

var notWord = regexp.MustCompile(`[^A-Za-z0-9]+`)

func sanitizeURL(u string) string { return notWord.ReplaceAllString(u, "_") }

func newFakeGitHub(t *testing.T, tag string) *fakeGitHub {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX build script")
	}
	g := &fakeGitHub{t: t, tools: t.TempDir(), files: t.TempDir(), latestTag: tag}
	return g
}

func (g *fakeGitHub) serve(url, body string) {
	g.t.Helper()
	if err := os.WriteFile(filepath.Join(g.files, sanitizeURL(url)), []byte(body), 0o644); err != nil {
		g.t.Fatal(err)
	}
}

func (g *fakeGitHub) run(out string) (string, error) {
	g.t.Helper()
	curl := `#!/bin/sh
head=0; url=""; out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -I|-fsSLI) head=1 ;;
    -o) shift; out="$1" ;;
    http*) url="$1" ;;
  esac
  shift
done
if [ "$head" = 1 ]; then
  [ -n "` + g.latestTag + `" ] || exit 6
  printf 'https://github.com/acme/rigsmith/releases/tag/%s' "` + g.latestTag + `"
  exit 0
fi
key=$(printf '%s' "$url" | sed 's/[^A-Za-z0-9][^A-Za-z0-9]*/_/g')
[ -f "` + g.files + `/$key" ] || exit 22
cp "` + g.files + `/$key" "$out"
`
	if err := os.WriteFile(filepath.Join(g.tools, "curl"), []byte(curl), 0o755); err != nil {
		g.t.Fatal(err)
	}
	cmd := exec.Command("sh", "site-installers.sh", out)
	cmd.Env = append(os.Environ(),
		"PATH="+g.tools+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RIGSMITH_REPO=acme/rigsmith")
	b, err := cmd.CombinedOutput()
	return string(b), err
}

const dl = "https://github.com/acme/rigsmith/releases/download/"

// A release from before installers were attached (v1.28.0 today): its scripts
// come from its tag, so the site serves what that release shipped with.
func TestSiteInstallers_OlderReleaseServesItsTaggedScripts(t *testing.T) {
	g := newFakeGitHub(t, "v1.28.0")
	g.serve("https://raw.githubusercontent.com/acme/rigsmith/v1.28.0/scripts/install.sh", "# install.sh of v1.28.0\n")
	g.serve("https://raw.githubusercontent.com/acme/rigsmith/v1.28.0/scripts/install.ps1", "# install.ps1 of v1.28.0\n")
	out := t.TempDir()
	log, err := g.run(out)
	if err != nil {
		t.Fatalf("site-installers: %v\n%s", err, log)
	}
	for name, want := range map[string]string{"install.sh": "# install.sh of v1.28.0\n", "install.ps1": "# install.ps1 of v1.28.0\n"} {
		got, err := os.ReadFile(filepath.Join(out, name))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q (%v), want the tag's copy", name, got, err)
		}
	}
}

// A release with attached installers serves those, once its checksums.txt
// vouches for them — and refuses one it doesn't.
func TestSiteInstallers_AttachedScriptsMustMatchTheChecksums(t *testing.T) {
	sh, ps1 := "# install.sh of v1.29.0\n", "# install.ps1 of v1.29.0\n"
	sums := sha256Hex(sh) + "  install.sh\n" + sha256Hex(ps1) + "  install.ps1\n"

	g := newFakeGitHub(t, "v1.29.0")
	g.serve(dl+"v1.29.0/checksums.txt", sums)
	g.serve(dl+"v1.29.0/install.sh", sh)
	g.serve(dl+"v1.29.0/install.ps1", ps1)
	out := t.TempDir()
	if log, err := g.run(out); err != nil {
		t.Fatalf("site-installers: %v\n%s", err, log)
	}
	if got, _ := os.ReadFile(filepath.Join(out, "install.sh")); string(got) != sh {
		t.Errorf("install.sh = %q, want the release asset", got)
	}

	tampered := newFakeGitHub(t, "v1.29.0")
	tampered.serve(dl+"v1.29.0/checksums.txt", sums)
	tampered.serve(dl+"v1.29.0/install.sh", "# something else\n")
	tampered.serve(dl+"v1.29.0/install.ps1", ps1)
	out = t.TempDir()
	log, err := tampered.run(out)
	if err == nil || !strings.Contains(log, "does not match its checksums.txt") {
		t.Fatalf("a tampered asset was not refused: %v\n%s", err, log)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("wrote %d file(s) despite the failure", len(entries))
	}
}

// No latest release to read fails the build: never a silent fall back to main.
func TestSiteInstallers_UnreadableLatestReleaseFailsTheBuild(t *testing.T) {
	g := newFakeGitHub(t, "")
	out := t.TempDir()
	log, err := g.run(out)
	if err == nil || !strings.Contains(log, "latest release") {
		t.Fatalf("an unreadable latest release didn't fail the build: %v\n%s", err, log)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("wrote %d file(s) despite the failure", len(entries))
	}

	// A latest release whose scripts can't be read fails too.
	missing := newFakeGitHub(t, "v1.28.0")
	log, err = missing.run(t.TempDir())
	if err == nil || !strings.Contains(log, "could not read install.sh") {
		t.Fatalf("an unreadable script didn't fail the build: %v\n%s", err, log)
	}
}
