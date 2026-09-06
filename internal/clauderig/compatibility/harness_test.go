// Package compatibility compares the shipped command workflow with its v2
// replacement. Fixtures and observations deliberately use no clauderig packages:
// changing a production default or serializer must not change both test inputs.
package compatibility

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// The v1 byte-preservation fix (#293). Advance only as an explicit compatibility
// decision, never automatically to main (which would compare a change to itself).
const baselineRef = "d39a4462427f1956d310abc306f010f85704d264"

var fixtureTime = time.Now().UTC().Truncate(time.Second)

func binaries(t *testing.T) (string, string) {
	t.Helper()
	if os.Getenv("CLAUDERIG_COMPAT") != "1" {
		t.Skip("set CLAUDERIG_COMPAT=1; requires git history containing the pinned baseline")
	}
	repo := strings.TrimSpace(command(t, "", nil, "git", "rev-parse", "--show-toplevel"))
	src := t.TempDir()
	// Export the baseline without changing this checkout or creating a worktree.
	archive := command(t, repo, nil, "git", "archive", baselineRef)
	r := tar.NewReader(strings.NewReader(archive))
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !filepath.IsLocal(h.Name) {
			t.Fatalf("non-local archive member %q", h.Name)
		}
		p := filepath.Join(src, filepath.FromSlash(h.Name))
		switch h.Typeflag {
		case tar.TypeXGlobalHeader:
			continue // git archive records the source commit in a PAX header
		case tar.TypeDir:
			must(t, os.MkdirAll(p, 0o755))
		case tar.TypeReg:
			must(t, os.MkdirAll(filepath.Dir(p), 0o755))
			b, err := io.ReadAll(r)
			must(t, err)
			must(t, os.WriteFile(p, b, os.FileMode(h.Mode)&0o777))
		default:
			t.Fatalf("unsupported archive member %q (type %d)", h.Name, h.Typeflag)
		}
	}
	binDir := t.TempDir()
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	base, next := filepath.Join(binDir, "baseline"+ext), filepath.Join(binDir, "candidate"+ext)
	command(t, src, nil, "go", "build", "-o", base, "./cmd/clauderig")
	command(t, repo, nil, "go", "build", "-o", next, "./cmd/clauderig")
	t.Logf("baseline %s; candidate working tree", baselineRef)
	return base, next
}

type sandbox struct {
	t                              *testing.T
	root, home, stage, remote, bin string
	env                            []string
	cfg                            map[string]any
	observations                   map[string]string
	writes                         int
}

func newSandbox(t *testing.T, root, bin string) *sandbox {
	t.Helper()
	s := &sandbox{t: t, root: root, home: filepath.Join(root, "home"), remote: filepath.Join(root, "remote.git"), bin: bin, observations: map[string]string{}}
	s.stage = filepath.Join(s.home, ".clauderig", "repo")
	must(t, os.MkdirAll(s.home, 0o755))
	// Scrub inherited vendor and Git configuration before invoking either binary.
	// Only file transport is allowed, even if a regression chooses another URL.
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		k = strings.ToUpper(k)
		if strings.HasPrefix(k, "GIT_") || strings.HasPrefix(k, "CLAUDE") || strings.HasPrefix(k, "RIGSMITH_") || strings.HasPrefix(k, "XDG_") {
			continue
		}
		switch k {
		case "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "NO_COLOR", "TERM", "CLICOLOR", "FORCE_COLOR", "CI", "TZ":
			continue
		}
		s.env = append(s.env, e)
	}
	s.env = append(s.env, "HOME="+s.home, "USERPROFILE="+s.home,
		"APPDATA="+filepath.Join(s.home, "AppData", "Roaming"), "LOCALAPPDATA="+filepath.Join(s.home, "AppData", "Local"),
		"XDG_CONFIG_HOME="+filepath.Join(s.home, ".config"), "XDG_DATA_HOME="+filepath.Join(s.home, ".local", "share"),
		"XDG_STATE_HOME="+filepath.Join(s.home, ".local", "state"), "XDG_CACHE_HOME="+filepath.Join(s.home, ".cache"),
		"NO_COLOR=1", "TERM=dumb", "CLICOLOR=0", "CI=1", "TZ=UTC", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+filepath.Join(s.home, "empty-gitconfig"), "GIT_ALLOW_PROTOCOL=file", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Compatibility", "GIT_AUTHOR_EMAIL=compat@example.com", "GIT_COMMITTER_NAME=Compatibility", "GIT_COMMITTER_EMAIL=compat@example.com",
		"GIT_AUTHOR_DATE="+fixtureTime.Format(time.RFC3339), "GIT_COMMITTER_DATE="+fixtureTime.Format(time.RFC3339),
		"GIT_CONFIG_COUNT=4", "GIT_CONFIG_KEY_0=core.autocrlf", "GIT_CONFIG_VALUE_0=true",
		"GIT_CONFIG_KEY_1=commit.gpgsign", "GIT_CONFIG_VALUE_1=false", "GIT_CONFIG_KEY_2=init.defaultBranch", "GIT_CONFIG_VALUE_2=main",
		"GIT_CONFIG_KEY_3=core.hooksPath", "GIT_CONFIG_VALUE_3="+filepath.Join(s.home, "empty-hooks"))
	ost := runtime.GOOS
	if ost == "darwin" {
		ost = "macos"
	}
	s.cfg = map[string]any{
		"schema": 1, "remote": s.remote, "chunkTranscripts": true, "hookIntervalMinutes": 0,
		"machines": map[string]any{"fixture": map[string]any{"name": "fixture", "os": ost, "home": s.home}},
		"roots": []any{
			map[string]any{"id": "cli", "enabled": true, "location": map[string]any{"portable": "$HOME/.claude"}},
			map[string]any{"id": "desktop", "enabled": true, "location": map[string]any{"portable": "$HOME/desktop"}},
		},
		"retention": map[string]any{"historyDays": 0, "maxFileBytes": 50 << 20, "largeFileBytes": 8 << 20, "floorBytes": 1 << 30, "squashFactor": 2},
	}
	s.saveConfig()
	s.git(s.root, "init", "--bare", "-b", "main", s.remote)
	return s
}

func (s *sandbox) saveConfig() { s.json("home/.clauderig/config.json", s.cfg) }

func (s *sandbox) put(rel, body string) {
	s.t.Helper()
	p := filepath.Join(s.root, filepath.FromSlash(rel))
	must(s.t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(s.t, os.WriteFile(p, []byte(body), 0o644))
	must(s.t, os.Chmod(p, 0o644))
	s.writes++
	stamp := fixtureTime.Add(time.Duration(s.writes) * time.Second)
	must(s.t, os.Chtimes(p, stamp, stamp))
}

func (s *sandbox) json(rel string, v any) {
	s.t.Helper()
	b, err := json.Marshal(v)
	must(s.t, err)
	s.put(rel, string(b)+"\n")
}

func (s *sandbox) read(rel string) string {
	s.t.Helper()
	b, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	must(s.t, err)
	return string(b)
}

func (s *sandbox) absent(rel string) {
	s.t.Helper()
	_, err := os.Lstat(filepath.Join(s.root, filepath.FromSlash(rel)))
	if !os.IsNotExist(err) {
		s.t.Fatalf("%s should be absent: %v", rel, err)
	}
}

func (s *sandbox) git(dir string, args ...string) string {
	s.t.Helper()
	return strings.TrimSpace(command(s.t, dir, s.env, "git", args...))
}

func (s *sandbox) run(label, input string, code int, message string, args ...string) {
	s.t.Helper()
	cmd := exec.CommandContext(s.t.Context(), s.bin, args...)
	cmd.Dir, cmd.Env, cmd.Stdin = s.home, s.env, strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	got := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			got = e.ExitCode()
		} else {
			s.t.Fatal(err)
		}
	}
	// Fang wraps error paragraphs to terminal width. A longer temporary path
	// can put adjacent diagnostic words on different lines on another OS.
	diagnostic := strings.Join(strings.Fields(stdout.String()+" "+stderr.String()), " ")
	if got != code || !strings.Contains(diagnostic, message) {
		s.t.Fatalf("%s: exit %d (want %d), expected %q\n%s\n%s", label, got, code, message, &stdout, &stderr)
	}
}

func (s *sandbox) snapshot(label string) {
	s.t.Helper()
	for _, tree := range []string{"home/.claude", "home/desktop", "home/.clauderig/desktop", "home/.claude.bak", "home/.clauderig/repo"} {
		root := filepath.Join(s.root, filepath.FromSlash(tree))
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		must(s.t, filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			var b []byte
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(p)
				if err != nil {
					return err
				}
				b = []byte(target)
			} else {
				b, err = os.ReadFile(p)
				if err != nil {
					return err
				}
			}
			key := tree + "/" + filepath.ToSlash(rel)
			s.observations[label+"/files/"+key] = fmt.Sprintf("%s %s", info.Mode(), s.normalized(key, b))
			return nil
		}))
	}
	// Compare ref names, commit counts and committed payloads, not commit IDs.
	// This catches local-only success and changes to config-history selection.
	for _, repo := range []struct{ name, path string }{{"stage", s.stage}, {"remote", s.remote}} {
		if _, err := os.Stat(repo.path); os.IsNotExist(err) {
			continue
		}
		if repo.name == "stage" {
			if _, err := os.Stat(filepath.Join(repo.path, ".git")); os.IsNotExist(err) {
				continue
			}
		}
		refs := s.git(repo.path, "for-each-ref", "--format=%(refname)", "refs/heads")
		s.observations[label+"/"+repo.name+"/refs"] = refs
		for _, ref := range strings.Fields(refs) {
			prefix := label + "/" + repo.name + "/" + ref
			s.observations[prefix+"/commits"] = s.git(repo.path, "rev-list", "--count", ref)
			entries := s.git(repo.path, "ls-tree", "-rz", ref)
			for _, entry := range strings.Split(entries, "\x00") {
				if entry == "" {
					continue
				}
				meta, path, ok := strings.Cut(entry, "\t")
				fields := strings.Fields(meta)
				if !ok || len(fields) != 3 {
					s.t.Fatalf("unexpected tree entry %q", entry)
				}
				blob := command(s.t, repo.path, s.env, "git", "cat-file", "blob", fields[2])
				s.observations[prefix+"/"+path] = fields[0] + " " + s.normalized("home/.clauderig/repo/"+path, []byte(blob))
			}
		}
	}
}

func (s *sandbox) normalized(path string, b []byte) string {
	text := string(b)
	// Normalize only known generated timestamp fields. Session timestamps and
	// arbitrary user JSON remain byte-for-byte significant.
	keys := []string{}
	if strings.Contains(path, "/repo/index/") {
		keys = []string{"seen", "accountSince"}
	}
	if strings.Contains(path, "/repo/journal/") {
		keys = []string{"at"}
	}
	if strings.HasSuffix(path, "/repo/clauderig-devices.json") {
		keys = []string{"lastSync"}
	}
	if len(keys) != 0 {
		lines := strings.Split(strings.TrimSpace(text), "\n")
		if strings.HasSuffix(path, ".json") {
			lines = []string{text}
		}
		for i, line := range lines {
			var v map[string]any
			must(s.t, json.Unmarshal([]byte(line), &v))
			if strings.HasSuffix(path, "/repo/clauderig-devices.json") {
				if devices, ok := v["devices"].(map[string]any); ok {
					for _, device := range devices {
						if fields, ok := device.(map[string]any); ok {
							normalizeTimes(fields, keys)
						}
					}
				}
			} else {
				normalizeTimes(v, keys)
			}
			out, err := json.Marshal(v)
			must(s.t, err)
			lines[i] = string(out)
		}
		text = strings.Join(lines, "\n")
	}
	escaped, _ := json.Marshal(s.root)
	text = strings.ReplaceAll(text, string(escaped[1:len(escaped)-1]), "<sandbox>")
	text = strings.ReplaceAll(text, s.root, "<sandbox>")
	text = strings.ReplaceAll(text, filepath.ToSlash(s.root), "<sandbox>")
	if len(text) > 4096 {
		return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(text)))
	}
	return text
}

func normalizeTimes(v map[string]any, keys []string) {
	for _, key := range keys {
		if str, ok := v[key].(string); ok {
			if tm, err := time.Parse(time.RFC3339Nano, str); err == nil && !tm.IsZero() {
				v[key] = "<generated-time>"
			}
		}
	}
}

func compare(t *testing.T, a, b map[string]string) {
	t.Helper()
	if reflect.DeepEqual(a, b) {
		return
	}
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	for _, k := range ordered {
		av, aok := a[k]
		bv, bok := b[k]
		if av != bv || aok != bok {
			t.Fatalf("compatibility changed at %s\nbaseline (present=%t): %s\ncandidate (present=%t): %s", k, aok, av, bok, bv)
		}
	}
}

func command(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Dir, cmd.Env = dir, env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s\n%s", name, args, err, out, &stderr)
	}
	return string(out)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
