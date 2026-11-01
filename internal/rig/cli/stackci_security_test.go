package cli

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStackCIAllowedHosts(t *testing.T) {
	m := &stackManifest{Repos: map[string]*stackRepo{"app": {Upstream: "github.com/acme/app", Fork: "other.example/acme/app"}}}
	if stackCIValidate(m, "github.com") == nil {
		t.Fatal("untrusted fork accepted")
	}
	if err := stackCIValidate(m, "github.com,other.example"); err != nil {
		t.Fatal(err)
	}
}
func TestStackCILFS(t *testing.T) {
	dir := t.TempDir()
	mustGitStack(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.bin filter=lfs\n"), 0600)
	mustGitStack(t, dir, "add", ".")
	mustGitStack(t, dir, "-c", "user.name=test", "-c", "user.email=t@t", "commit", "-qm", "fixture")
	err := stackCIMaterialize(context.Background(), dir, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "Git LFS") {
		t.Fatalf("expected LFS refusal: %v", err)
	}
}
func TestStackCIFilteredCache(t *testing.T) {
	dir := t.TempDir()
	mustGitStack(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "file"), []byte("source"), 0600)
	mustGitStack(t, dir, "add", ".")
	mustGitStack(t, dir, "-c", "user.name=test", "-c", "user.email=t@t", "commit", "-qm", "fixture")
	sha := strings.TrimSpace(mustGitStack(t, dir, "rev-parse", "HEAD"))
	t.Setenv("RIG_STACK_CACHE", t.TempDir())
	calls := 0
	fetch := func() (string, error) { calls++; return sha, nil }
	for i := 0; i < 2; i++ {
		got, err := stackCIFilteredCache(context.Background(), dir, "github.com/acme/app", sha, "app", stackJoshVersion, fetch)
		if err != nil || got != sha {
			t.Fatalf("%s %v", got, err)
		}
	}
	if calls != 1 {
		t.Fatalf("warm cache fetched %d times", calls)
	}
}
func TestStackCIInstallerTampered(t *testing.T) {
	platform := "linux"
	if runtime.GOOS == "windows" {
		platform = "windows"
	}
	t.Run(platform, func(t *testing.T) { stackCIInstallerFixture(t, platform) })
	if platform != "windows" {
		t.Run("windows-zip", func(t *testing.T) { stackCIInstallerFixture(t, "windows") })
	}
}

func stackCIInstallerFixture(t *testing.T, platform string) {
	dir := t.TempDir()
	runnerTemp := filepath.Join(dir, `back\slash`)
	if err := os.MkdirAll(runnerTemp, 0700); err != nil {
		t.Fatal(err)
	}
	// On Unix this is a literal backslash in one name; on Windows it is a
	// separator, and RUNNER_TEMP retains its native drive/backslash spelling.
	if !strings.Contains(runnerTemp, `\`) {
		t.Fatal("fixture must exercise backslashes")
	}
	extension, binary, uname := "tar.gz", "rig", "Linux"
	if platform == "windows" {
		extension, binary, uname = "zip", "rig.exe", "MINGW64_NT"
	}
	archive := filepath.Join(dir, "fixture."+extension)
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("#!/bin/sh\nexit 0\n")
	if extension == "zip" {
		zw := zip.NewWriter(f)
		entry, e := zw.Create(binary)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = entry.Write(data); e != nil {
			t.Fatal(e)
		}
		if e = zw.Close(); e != nil {
			t.Fatal(e)
		}
	} else {
		gz := gzip.NewWriter(f)
		tw := tar.NewWriter(gz)
		if err = tw.WriteHeader(&tar.Header{Name: binary, Mode: 0755, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err = tw.Write(data); err != nil {
			t.Fatal(err)
		}
		if err = tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err = gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(blob))
	t.Setenv("FIXTURE_ARCHIVE", archive)
	t.Setenv("FIXTURE_SUM", strings.Repeat("0", 64))
	t.Setenv("FIXTURE_ASSET", "rig_1.23.1_"+platform+"_amd64."+extension)
	t.Setenv("FIXTURE_UNAME", uname)
	curl := `#!/bin/bash
[[ "$*" == *"--retry 3 --connect-timeout 30"* ]] || { echo "missing bounded download retries" >&2; exit 98; }
while [[ $# -gt 0 ]]; do if [[ $1 == -o ]]; then dest=$2; shift; fi; shift; done
if [[ $dest == *checksums.txt ]]; then printf '%s  %s\n' "$FIXTURE_SUM" "$FIXTURE_ASSET" > "$dest"; else cp "$FIXTURE_ARCHIVE" "$dest"; fi
`
	os.WriteFile(filepath.Join(dir, "curl"), []byte(curl), 0755)
	// macOS's native sha256sum does not escape names; exercise the GNU-style
	// behavior using real shasum on Unix, rather than hide the bug. Windows
	// uses Git Bash sha256sum; a native shasum.bat (e.g. Strawberry Perl)
	// cannot be executed by this POSIX wrapper.
	if tool, err := exec.LookPath("shasum"); runtime.GOOS != "windows" && err == nil {
		t.Setenv("REAL_SHASUM", tool)
		if err = os.WriteFile(filepath.Join(dir, "sha256sum"), []byte("#!/bin/sh\nexec \"$REAL_SHASUM\" -a 256 \"$@\"\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}

	os.WriteFile(filepath.Join(dir, "uname"), []byte("#!/bin/sh\ncase $1 in -s) echo \"$FIXTURE_UNAME\";; *) echo x86_64;; esac\n"), 0755)
	var bash string
	if runtime.GOOS == "windows" {
		git, err := exec.LookPath("git")
		if err != nil {
			t.Fatal(err)
		}
		bash = ""
		for _, relative := range []string{"../bin/bash.exe", "../usr/bin/bash.exe", "../../bin/bash.exe"} {
			candidate := filepath.Clean(filepath.Join(filepath.Dir(git), relative))
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				bash = candidate
				break
			}
		}
		if bash == "" {
			t.Fatal("Git Bash is required to exercise the Windows installer; WSL bash cannot use this fixture")
		}
	}
	if runtime.GOOS != "windows" {
		bash, err = exec.LookPath("bash")
		if err != nil {
			t.Fatal(err)
		}
	}
	installer, err := filepath.Abs("../../../.github/actions/stack/install-rig.sh")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FIXTURE_BIN", dir)
	t.Setenv("FIXTURE_INSTALLER", installer)
	// Convert only shell lookup paths. Keep native RUNNER_TEMP backslashes to
	// exercise the installer exactly as a Windows Actions runner invokes it.
	launch := `if [[ -x /usr/bin/cygpath ]]; then
  export FIXTURE_BIN="$(/usr/bin/cygpath -u "$FIXTURE_BIN")"
  export FIXTURE_ARCHIVE="$(/usr/bin/cygpath -u "$FIXTURE_ARCHIVE")"
  export FIXTURE_INSTALLER="$(/usr/bin/cygpath -u "$FIXTURE_INSTALLER")"
  if [[ -n "${REAL_SHASUM:-}" ]]; then export REAL_SHASUM="$(/usr/bin/cygpath -u "$REAL_SHASUM")"; fi
fi
export PATH="$FIXTURE_BIN:/usr/bin:/bin:$PATH"
exec /bin/bash "$FIXTURE_INSTALLER"`
	command := func() *exec.Cmd { return exec.Command(bash, "--noprofile", "--norc", "-c", launch) }
	c := command()
	c.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "INPUT_RIG_VERSION=v1.23.1", "RUNNER_TEMP="+runnerTemp, "GITHUB_PATH="+filepath.Join(dir, "path"))
	out, err := c.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "checksum mismatch") {
		t.Fatalf("tampered download must fail verification: %v %s", err, out)
	}
	c = command()
	c.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "INPUT_RIG_VERSION=v1.23.1", "RUNNER_TEMP="+runnerTemp, "GITHUB_PATH="+filepath.Join(dir, "path"), "FIXTURE_SUM="+sum)
	if out, err = c.CombinedOutput(); err != nil {
		t.Fatalf("valid fixture: %v %s", err, out)
	}
	c = command()
	c.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "INPUT_RIG_VERSION=v1.23.1", "RUNNER_TEMP="+runnerTemp, "GITHUB_PATH="+filepath.Join(dir, "path"), "FIXTURE_SUM="+sum, "INPUT_RIG_SHA256="+strings.Repeat("f", 64))
	if out, err = c.CombinedOutput(); err == nil || !strings.Contains(string(out), "pinned checksum mismatch") {
		t.Fatalf("expected independent pin refusal: %v %s", err, out)
	}
}

func BenchmarkStackCIMaterialize(b *testing.B) {
	dir := b.TempDir()
	c := exec.Command("git", "init", "-q", dir)
	if err := c.Run(); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprint(i)), []byte("fixture"), 0600)
	}
	for _, args := range [][]string{{"add", "."}, {"-c", "user.name=test", "-c", "user.email=t@t", "commit", "-qm", "fixture"}} {
		c = exec.Command("git", args...)
		c.Dir = dir
		if err := c.Run(); err != nil {
			b.Fatal(err)
		}
	}
	for _, mode := range []string{"batch", "per-file"} {
		b.Run(mode, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				dest := b.TempDir()
				if mode == "batch" {
					if err := stackCIMaterialize(context.Background(), dir, dest); err != nil {
						b.Fatal(err)
					}
				} else {
					entries, err := stackCIGit(context.Background(), dir, "ls-tree", "-r", "HEAD")
					if err != nil {
						b.Fatal(err)
					}
					for _, line := range strings.Split(strings.TrimSpace(string(entries)), "\n") {
						fields := strings.Fields(line)
						data, err := stackCIGit(context.Background(), dir, "cat-file", "blob", fields[2])
						if err != nil {
							b.Fatal(err)
						}
						if err = os.WriteFile(filepath.Join(dest, fields[3]), data, 0644); err != nil {
							b.Fatal(err)
						}
					}
				}
			}
		})
	}
}

func TestStackCIPushGuard(t *testing.T) {
	dir := t.TempDir()
	dest := t.TempDir()
	mustGitStack(t, dir, "init", "-q")
	mustGitStack(t, dest, "init", "--bare", "-q")
	mustGitStack(t, dir, "-c", "user.name=test", "-c", "user.email=t@t", "commit", "--allow-empty", "-qm", "fixture")
	if err := stackCIGuardPush(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if out, err := stackCIGit(context.Background(), dir, "push", dest, "HEAD:refs/heads/main"); err == nil || !strings.Contains(err.Error(), "Fused history must never be pushed") {
		t.Fatalf("push guard failed: %s %v", out, err)
	}
	mustGitStack(t, dir, "remote", "add", "origin", dest)
	if err := stackCIGuardPush(context.Background(), dir); err == nil {
		t.Fatal("accepted fused push destination")
	}
}
func TestStackCICustomEngine(t *testing.T) {
	if err := stackCIValidate(&stackManifest{Josh: "custom"}, "github.com"); err == nil {
		t.Fatal("accepted unverified engine version")
	}
}

// A bundle key is a lookup hint, not a signature over its relation to a cursor.
func TestStackCISubstitutedBundleIsTrusted(t *testing.T) {
	dir := t.TempDir()
	mustGitStack(t, dir, "init", "-q")
	mustGitStack(t, dir, "-c", "user.name=test", "-c", "user.email=t@t", "commit", "--allow-empty", "-qm", "expected")
	cursor := strings.TrimSpace(mustGitStack(t, dir, "rev-parse", "HEAD"))
	cache := t.TempDir()
	t.Setenv("RIG_STACK_CACHE", cache)
	calls := 0
	fetch := func() (string, error) { calls++; return cursor, nil }
	if _, err := stackCIFilteredCache(context.Background(), dir, "github.com/acme/app", cursor, "app", stackJoshVersion, fetch); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, dir, "-c", "user.name=test", "-c", "user.email=t@t", "commit", "--allow-empty", "-qm", "substituted")
	substituted := strings.TrimSpace(mustGitStack(t, dir, "rev-parse", "HEAD"))
	paths, err := filepath.Glob(filepath.Join(cache, "*.bundle"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("bundle fixture: %v %v", paths, err)
	}
	mustGitStack(t, dir, "update-ref", "refs/rigsmith/cache", substituted)
	mustGitStack(t, dir, "bundle", "create", paths[0], "refs/rigsmith/cache")
	got, err := stackCIFilteredCache(context.Background(), dir, "github.com/acme/app", cursor, "app", stackJoshVersion, fetch)
	if err != nil || got != substituted || calls != 1 {
		t.Fatalf("expected trusted substituted history, got %s calls=%d err=%v", got, calls, err)
	}
	t.Setenv("RIG_STACK_CACHE", "")
	got, err = stackCIFilteredCache(context.Background(), dir, "github.com/acme/app", cursor, "app", stackJoshVersion, fetch)
	if err != nil || got != cursor || calls != 2 {
		t.Fatalf("disabled cache did not fetch: %s calls=%d err=%v", got, calls, err)
	}
}
