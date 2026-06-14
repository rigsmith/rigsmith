// Package gomod implements the Go ecosystem adapter: it discovers go.mod modules
// and their intra-repo require edges.
//
// Go modules have NO version field in go.mod — the version source is git tags of
// the form `module/vX.Y.Z` (or `vX.Y.Z` for a root module). Per decision Q2,
// Discover reads the latest matching git tag as authoritative (option a), with an
// optional `// rigsmith:version X.Y.Z` comment as a secondary fallback (option b)
// for un-tagged repos. SetVersion still writes that comment as a record; the real
// release operation (creating + pushing the tag) belongs to `tag`/`publish`,
// which are still stubbed here — see Publish's TODO.
package gomod

import (
	"bufio"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rigsmith/core/gitutil"
	"github.com/rigsmith/core/plugin"
	"github.com/rigsmith/core/walkutil"
)

// Adapter is the in-process Go ecosystem adapter.
type Adapter struct{}

// New returns a Go adapter.
func New() *Adapter { return &Adapter{} }

var _ plugin.Ecosystem = (*Adapter)(nil)

// defaultVersion is used when no `// rigsmith:version` annotation is present.
const defaultVersion = "0.0.0"

var (
	// moduleRe captures the module path and any trailing `// rigsmith:version X`.
	// A foreign trailing comment (e.g. `// Deprecated: use bar`, a valid go.mod
	// directive) is matched and ignored via the final `(?://.*)?` so the module is
	// still discovered — earlier the line had to end right after the (optional)
	// rigsmith annotation, so any other comment made the whole line fail to match
	// and the module was silently skipped. `.` does not cross the newline under
	// (?m), so the match stays on the module line for SetVersion's in-place rewrite.
	moduleRe         = regexp.MustCompile(`(?m)^module[ \t]+(\S+)(?:[ \t]+//[ \t]*rigsmith:version[ \t]+(\S+))?[ \t]*(?://.*)?$`)
	versionCommentRe = regexp.MustCompile(`//\s*rigsmith:version\s+\S+`)
)

// Info returns the Go adapter's identity and capabilities.
func (a *Adapter) Info() plugin.EcosystemInfo {
	return plugin.EcosystemInfo{
		APIVersion:       plugin.APIVersion,
		ID:               "go",
		DisplayName:      "Go",
		Capabilities:     []string{plugin.MethodDiscover, plugin.MethodSetVersion, plugin.MethodPublish},
		ManifestPatterns: []string{"go.mod"},
		DevCommands: map[string][]string{
			plugin.VerbBuild:  {"go", "build", "./..."},
			plugin.VerbTest:   {"go", "test", "./..."},
			plugin.VerbRun:    {"go", "run", "."},
			plugin.VerbFormat: {"gofmt", "-l", "-w", "."},
			// Go folds linting and type-checking into the compiler + `go vet`,
			// which type-checks the program as part of its analysis. There is no
			// separate type-only pass, so both lint and typecheck surface the
			// canonical `go vet ./...` — giving `rig lint`/`rig check` a sensible
			// Go behavior for cross-ecosystem muscle memory.
			plugin.VerbLint:      {"go", "vet", "./..."},
			plugin.VerbTypecheck: {"go", "vet", "./..."},
			plugin.VerbCoverage:  {"go", "test", "-cover", "./..."},
			plugin.VerbInstall:   {"go", "mod", "download"},
			// Go has no distinct frozen-install: module downloads are checksum-
			// verified against go.sum, so `ci` mirrors `install`.
			plugin.VerbCI:       {"go", "mod", "download"},
			plugin.VerbAdd:      {"go", "get"},
			plugin.VerbUpgrade:  {"go", "get", "-u", "./..."},
			plugin.VerbOutdated: {"go", "list", "-m", "-u", "all"},
			plugin.VerbClean:    {"go", "clean"},
			plugin.VerbGlobal:   {"go", "install"},
			// `go run pkg@latest` runs a tool once without installing it — Go's
			// equivalent of npx/dnx. The caller appends the package@version.
			plugin.VerbDlx: {"go", "run"},
		},
	}
}

// Detect reports whether a go.mod exists at the repo root.
func (a *Adapter) Detect(ctx context.Context, root string) (bool, error) {
	// Recursive: any go.mod under root counts, so a go.work monorepo (no root
	// go.mod) and a polyglot repo with Go in a subdir are both detected.
	resp, err := a.Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: "."})
	if err != nil {
		return false, err
	}
	return len(resp.Packages) > 0, nil
}

// Discover walks for go.mod files and returns one Package each, with intra-repo
// require edges (those whose module path matches another discovered module).
func (a *Adapter) Discover(ctx context.Context, req plugin.DiscoverRequest) (plugin.DiscoverResponse, error) {
	root := req.RepoRoot
	source := req.SourcePath
	if source == "" {
		source = "."
	}
	scanRoot := filepath.Join(root, source)

	// First pass: parse every go.mod and collect the set of module paths.
	type parsed struct {
		path    string
		module  string
		version string
		require map[string]string // module path -> required version
	}
	var all []parsed
	modules := map[string]bool{}
	err := walkutil.Walk(scanRoot, func(path string, d fs.DirEntry) error {
		if filepath.Base(path) != "go.mod" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mod, ver := parseModule(string(content))
		if mod == "" {
			return nil
		}
		all = append(all, parsed{path: path, module: mod, version: ver, require: parseRequires(string(content))})
		modules[mod] = true
		return nil
	})
	if err != nil {
		return plugin.DiscoverResponse{}, err
	}

	// Second pass: build packages with only intra-repo require edges.
	var resp plugin.DiscoverResponse
	for _, pr := range all {
		var deps []plugin.Dependency
		names := make([]string, 0, len(pr.require))
		for name := range pr.require {
			names = append(names, name)
		}
		sortStrings(names)
		for _, name := range names {
			if modules[name] && name != pr.module {
				deps = append(deps, plugin.Dependency{Name: name, Kind: plugin.DepNormal, Range: pr.require[name]})
			}
		}
		dir := relTo(root, filepath.Dir(pr.path))
		// Version resolution (decision Q2 → option a, git-tag native): the latest
		// matching git tag is authoritative. The `// rigsmith:version` comment is a
		// secondary fallback (option b) for repos without tags yet / cross-checking.
		version := pr.version
		if tagVer, ok := gitutil.LatestModuleVersion(ctx, root, dir); ok {
			version = tagVer
		}
		resp.Packages = append(resp.Packages, plugin.Package{
			Name:         pr.module,
			Version:      version,
			Dir:          dir,
			ManifestPath: relTo(root, pr.path),
			Dependencies: deps,
		})
	}
	return resp, nil
}

// SetVersion updates (or creates) the `// rigsmith:version` comment on the module
// line as a placeholder.
//
// TODO: the real Go release operation is creating + pushing a git tag
// (module/vX.Y.Z), not editing go.mod. See the package doc.
func (a *Adapter) SetVersion(ctx context.Context, req plugin.SetVersionRequest) error {
	path := filepath.Join(req.RepoRoot, req.Package.ManifestPath)
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated := setVersionComment(string(content), req.NewVersion)
	return os.WriteFile(path, []byte(updated), 0o644)
}

// Publish for a Go module is a no-op at the registry level: there is no registry
// push — a Go module is "published" by creating and pushing a git tag
// (module/vX.Y.Z), which the module proxy then serves. The shiprig `publish`
// command performs that tag + push in its tagging phase (shared with `tag`), so
// this adapter reports the version as handled-by-tag rather than pushing here.
func (a *Adapter) Publish(ctx context.Context, req plugin.PublishRequest) (plugin.PublishResponse, error) {
	return plugin.PublishResponse{Skipped: true, Message: "published via git tag (publish tagging phase)"}, nil
}

// parseModule returns the module path and the optional `// rigsmith:version`
// value (defaultVersion when the annotation is absent).
func parseModule(text string) (module, version string) {
	m := moduleRe.FindStringSubmatch(text)
	if m == nil {
		return "", ""
	}
	version = defaultVersion
	if m[2] != "" {
		version = m[2]
	}
	return m[1], version
}

// parseRequires extracts the require edges (module path -> version) from both the
// single-line `require x v` form and the `require ( ... )` block form.
func parseRequires(text string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	inBlock := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		switch {
		case inBlock:
			if line == ")" {
				inBlock = false
				continue
			}
			addRequire(out, line)
		case line == "require (":
			inBlock = true
		case strings.HasPrefix(line, "require ("):
			inBlock = true
		case strings.HasPrefix(line, "require "):
			addRequire(out, strings.TrimSpace(strings.TrimPrefix(line, "require")))
		}
	}
	return out
}

// addRequire parses a `<module> <version>` pair into the map.
func addRequire(out map[string]string, line string) {
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		out[fields[0]] = fields[1]
	}
}

// setVersionComment replaces the `// rigsmith:version` annotation on the module
// line, inserting one when absent.
func setVersionComment(text, newVersion string) string {
	loc := moduleRe.FindStringSubmatchIndex(text)
	if loc == nil {
		return text
	}
	line := text[loc[0]:loc[1]]
	annotation := "// rigsmith:version " + newVersion
	var newLine string
	switch {
	case versionCommentRe.MatchString(line):
		// Update the existing rigsmith annotation in place.
		newLine = versionCommentRe.ReplaceAllString(line, annotation)
	case strings.Contains(line, "//"):
		// The module line already carries a foreign comment (e.g. a
		// `// Deprecated:` directive). go.mod allows only one comment per line and
		// the authoritative version source is the git tag, so leave the line intact
		// rather than clobber the existing comment (or bury the annotation behind it
		// where parseModule could no longer read it back).
		newLine = line
	default:
		newLine = strings.TrimRight(line, " \t") + " " + annotation
	}
	return text[:loc[0]] + newLine + text[loc[1]:]
}

func relTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
