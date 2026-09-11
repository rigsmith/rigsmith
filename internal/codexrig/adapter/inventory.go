package adapter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/internal/agentrig/allowlist"
)

type Root struct {
	Kind RootKind
	Path string
}

type Inventory struct {
	Root       RootKind    `json:"root"`
	Path       string      `json:"path"`
	Present    bool        `json:"present"`
	Candidates []Candidate `json:"candidates"`
}

// Roots resolves explicitly supplied locations without reading configuration or
// creating state. CODEX_HOME overrides only the Codex source, not shared skills.
// An empty override means the standard user location. Require absolute roots so
// environment mistakes cannot redirect inspection into the current repository.
func Roots(home, codexHome, skillsDir string) ([]Root, error) {
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("user home must be absolute")
	}
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	if skillsDir == "" {
		skillsDir = filepath.Join(home, ".agents", "skills")
	}
	roots := []Root{{CodexHome, codexHome}, {UserSkills, skillsDir}}
	for i := range roots {
		if !filepath.IsAbs(roots[i].Path) {
			return nil, fmt.Errorf("%s must be an absolute directory", roots[i].Kind)
		}
		roots[i].Path = filepath.Clean(roots[i].Path)
	}
	return roots, nil
}

// Inspect reads names and filesystem metadata only. It does not open file
// contents, follow file/directory links into capture, execute Codex or write
// state. Missing roots are reported explicitly; other errors fail the report.
// The walk is a point-in-time inventory, not a sealed snapshot or security scan.
func Inspect(ctx context.Context, root Root) (Inventory, error) {
	result := Inventory{Root: root.Kind, Path: root.Path, Candidates: []Candidate{}}
	if root.Kind != CodexHome && root.Kind != UserSkills {
		return result, fmt.Errorf("unsupported Codex root kind")
	}
	if !filepath.IsAbs(root.Path) {
		return result, fmt.Errorf("inspection root must be absolute")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	info, err := os.Lstat(root.Path)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !info.IsDir() {
		return result, fmt.Errorf("%s inspection root must be a directory, not a link or file", root.Kind)
	}
	result.Present = true
	paths, _, err := allowlist.WalkContext(ctx, root.Path, Selection(root.Kind))
	if err != nil {
		return result, err
	}
	for _, rel := range paths {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		candidate, ok := Classify(root.Kind, rel)
		if !ok {
			continue
		}
		info, err := os.Lstat(filepath.Join(root.Path, filepath.FromSlash(rel)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return result, err
		}
		if info.Mode().IsRegular() {
			result.Candidates = append(result.Candidates, candidate)
		}
	}
	return result, ctx.Err()
}
