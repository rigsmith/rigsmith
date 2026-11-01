package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	dir := t.TempDir()
	archive := filepath.Join(dir, "fixture.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	data := []byte("#!/bin/sh\nexit 0\n")
	if err = tw.WriteHeader(&tar.Header{Name: "rig", Mode: 0755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err = tw.Write(data); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	f.Close()
	blob, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(blob))
	t.Setenv("FIXTURE_ARCHIVE", archive)
	t.Setenv("FIXTURE_SUM", strings.Repeat("0", 64))
	curl := `#!/bin/bash
while [[ $# -gt 0 ]]; do if [[ $1 == -o ]]; then dest=$2; shift; fi; shift; done
if [[ $dest == *checksums.txt ]]; then printf '%s  rig_1.23.1_linux_amd64.tar.gz\n' "$FIXTURE_SUM" > "$dest"; else cp "$FIXTURE_ARCHIVE" "$dest"; fi
`
	os.WriteFile(filepath.Join(dir, "curl"), []byte(curl), 0755)
	os.WriteFile(filepath.Join(dir, "uname"), []byte("#!/bin/sh\ncase $1 in -s) echo Linux;; *) echo x86_64;; esac\n"), 0755)
	c := exec.Command("bash", "../../../.github/actions/stack/install-rig.sh")
	c.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "INPUT_RIG_VERSION=v1.23.1", "RUNNER_TEMP="+dir, "GITHUB_PATH="+filepath.Join(dir, "path"))
	out, err := c.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "checksum mismatch") {
		t.Fatalf("tampered download must fail verification: %v %s", err, out)
	}
	c = exec.Command("bash", "../../../.github/actions/stack/install-rig.sh")
	c.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "INPUT_RIG_VERSION=v1.23.1", "RUNNER_TEMP="+dir, "GITHUB_PATH="+filepath.Join(dir, "path"), "FIXTURE_SUM="+sum)
	if out, err = c.CombinedOutput(); err != nil {
		t.Fatalf("valid fixture: %v %s", err, out)
	}
	c = exec.Command("bash", "../../../.github/actions/stack/install-rig.sh")
	c.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "INPUT_RIG_VERSION=v1.23.1", "RUNNER_TEMP="+dir, "GITHUB_PATH="+filepath.Join(dir, "path"), "FIXTURE_SUM="+sum, "INPUT_RIG_SHA256="+strings.Repeat("f", 64))
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
