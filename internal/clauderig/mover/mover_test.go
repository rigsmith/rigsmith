package mover

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/project"
)

// scene builds a fake $HOME with a project directory and the Claude history that
// references it: a CLI project slug (plus a deeper sub-session), a settings.json
// additionalDirectories entry, and a Desktop session file.
type scene struct {
	home       string
	src        string
	dst        string
	claudeHome string
	desktop    string
}

func setup(t *testing.T) scene {
	t.Helper()
	home := t.TempDir()
	s := scene{
		home:       home,
		src:        filepath.Join(home, "Git", "proj"),
		dst:        filepath.Join(home, "Git", "renamed"),
		claudeHome: filepath.Join(home, ".claude"),
		desktop:    filepath.Join(home, "Desktop"),
	}
	mustMkdir(t, s.src)

	// CLI project: root session + a deeper sub-session under src.
	rootSlug := project.Flatten(s.src)
	writeTranscript(t, s.claudeHome, rootSlug, "a.jsonl", s.src)
	subCwd := filepath.Join(s.src, "site")
	subSlug := project.Flatten(subCwd)
	writeTranscript(t, s.claudeHome, subSlug, "b.jsonl", subCwd)

	// An unrelated project that must be left alone.
	other := filepath.Join(home, "Git", "other")
	writeTranscript(t, s.claudeHome, project.Flatten(other), "c.jsonl", other)

	// settings.json with an additionalDirectories entry under src.
	writeJSON(t, filepath.Join(s.claudeHome, "settings.json"), map[string]any{
		"additionalDirectories": []any{s.src, filepath.Join(home, "elsewhere")},
	})

	// Desktop session metadata referencing src.
	writeJSON(t, filepath.Join(s.desktop, "claude-code-sessions", "uuid", "local_1.json"), map[string]any{
		"cwd":       s.src,
		"originCwd": filepath.Dir(s.src),
		"unrelated": "/tmp/x",
	})
	return s
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeTranscript(t *testing.T, claudeHome, slug, name, cwd string) {
	t.Helper()
	dir := filepath.Join(claudeHome, "projects", slug)
	mustMkdir(t, dir)
	// Two records: a header line with cwd, and a body line where the same path
	// appears inside tool output (must NOT be rewritten — only the cwd field).
	header, _ := json.Marshal(map[string]any{"type": "user", "cwd": cwd, "isSidechain": false})
	body, _ := json.Marshal(map[string]any{"type": "assistant", "text": "I read " + cwd + "/main.go"})
	content := string(header) + "\n" + string(body) + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func planFor(t *testing.T, s scene, live []string) *Plan {
	t.Helper()
	absSrc, absDst, moveDir, err := Resolve(s.src, s.dst)
	if err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(absSrc, absDst, moveDir, s.claudeHome, s.desktop, live)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApply_MovesDirAndRelinksHistory(t *testing.T) {
	s := setup(t)
	p := planFor(t, s, nil)

	if !p.MoveDir {
		t.Fatal("expected MoveDir for a present source")
	}
	if len(p.Projects) != 2 {
		t.Fatalf("want 2 project slug moves (root + sub), got %d: %+v", len(p.Projects), p.Projects)
	}
	if len(p.Desktop) != 1 {
		t.Fatalf("want 1 desktop file, got %d", len(p.Desktop))
	}
	if p.Settings == "" {
		t.Fatal("want settings.json flagged")
	}

	projectsDir := filepath.Join(s.claudeHome, "projects")
	rep, err := p.Apply(projectsDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.MovedDir || rep.SlugsRenamed != 2 {
		t.Fatalf("unexpected report: %+v", rep)
	}

	// Directory moved.
	if _, err := os.Stat(s.dst); err != nil {
		t.Fatalf("dst missing: %v", err)
	}
	if _, err := os.Stat(s.src); !os.IsNotExist(err) {
		t.Fatalf("src should be gone, got %v", err)
	}

	// New slug dirs exist, old ones gone.
	newRoot := project.Flatten(s.dst)
	if _, err := os.Stat(filepath.Join(projectsDir, newRoot)); err != nil {
		t.Fatalf("new root slug missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectsDir, project.Flatten(s.src))); !os.IsNotExist(err) {
		t.Fatal("old root slug should be gone")
	}

	// Transcript cwd rebased, but the path inside tool output preserved.
	data, _ := os.ReadFile(filepath.Join(projectsDir, newRoot, "a.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var header map[string]any
	json.Unmarshal([]byte(lines[0]), &header)
	if header["cwd"] != s.dst {
		t.Fatalf("cwd not rebased: %v", header["cwd"])
	}
	if !strings.Contains(lines[1], s.src+"/main.go") {
		t.Fatalf("tool-output path should be untouched, body: %s", lines[1])
	}

	// Settings additionalDirectories rebased.
	var settings map[string]any
	sb, _ := os.ReadFile(p.Settings)
	json.Unmarshal(sb, &settings)
	dirs := settings["additionalDirectories"].([]any)
	if dirs[0] != s.dst {
		t.Fatalf("settings dir not rebased: %v", dirs[0])
	}

	// Desktop cwd rebased.
	var desk map[string]any
	db, _ := os.ReadFile(p.Desktop[0])
	json.Unmarshal(db, &desk)
	if desk["cwd"] != s.dst {
		t.Fatalf("desktop cwd not rebased: %v", desk["cwd"])
	}
}

func TestApply_DryRunChangesNothing(t *testing.T) {
	s := setup(t)
	p := planFor(t, s, nil)
	before, _ := os.ReadFile(filepath.Join(s.claudeHome, "projects", project.Flatten(s.src), "a.jsonl"))

	if _, err := p.Apply(filepath.Join(s.claudeHome, "projects"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.src); err != nil {
		t.Fatal("dry run must not move the directory")
	}
	after, _ := os.ReadFile(filepath.Join(s.claudeHome, "projects", project.Flatten(s.src), "a.jsonl"))
	if string(before) != string(after) {
		t.Fatal("dry run must not rewrite transcripts")
	}
}

func TestApply_RefusesLiveSession(t *testing.T) {
	s := setup(t)
	p := planFor(t, s, []string{filepath.Join(s.src, "site")})
	if len(p.LiveBlockers) != 1 {
		t.Fatalf("want 1 live blocker, got %v", p.LiveBlockers)
	}
	if _, err := p.Apply(filepath.Join(s.claudeHome, "projects"), false); err == nil {
		t.Fatal("expected refusal with a live session under src")
	}
}

func TestApply_RefusesCollision(t *testing.T) {
	s := setup(t)
	// Pre-create the destination root slug dir to force a collision.
	mustMkdir(t, filepath.Join(s.claudeHome, "projects", project.Flatten(s.dst)))
	p := planFor(t, s, nil)
	if !p.HasCollision() {
		t.Fatal("expected a collision")
	}
	if _, err := p.Apply(filepath.Join(s.claudeHome, "projects"), false); err == nil {
		t.Fatal("expected refusal on collision")
	}
}

func TestResolve_RelinkOnlyWhenSrcGone(t *testing.T) {
	home := t.TempDir()
	dst := filepath.Join(home, "moved")
	mustMkdir(t, dst)
	_, _, moveDir, err := Resolve(filepath.Join(home, "gone"), dst)
	if err != nil {
		t.Fatal(err)
	}
	if moveDir {
		t.Fatal("want relink-only (moveDir=false) when src is already gone")
	}
}

func TestResolve_Errors(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, "a")
	mustMkdir(t, src)
	dst := filepath.Join(home, "b")
	mustMkdir(t, dst)
	if _, _, _, err := Resolve(src, dst); err == nil {
		t.Fatal("want error when both src and dst exist")
	}
	if _, _, _, err := Resolve(src, src); err == nil {
		t.Fatal("want error when src == dst")
	}
	if _, _, _, err := Resolve(src, filepath.Join(home, "nope", "deep")); err == nil {
		t.Fatal("want error when dst parent is missing")
	}
}
