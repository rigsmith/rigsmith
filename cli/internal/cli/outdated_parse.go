package cli

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/rigsmith/cli/internal/detect"
)

// outdatedDep is one dependency with an available update, parsed from an
// ecosystem's outdated report. project is set only for .NET (the csproj/sln the
// package belongs to), where upgrades are applied per project.
type outdatedDep struct {
	name    string
	current string // installed/resolved version ("—" when not installed)
	latest  string // the newest available version (what `outdated -i` upgrades to)
	wanted  string // the highest version inside the manifest's range, "" when the
	// ecosystem has no ranges/this datum (go/.NET); `upgrade` targets this.
	project string // .NET only: owning project path
	dev     bool   // node: a devDependency (so the upgrade keeps it there)
}

// parseGoListUpdates parses the concatenated JSON objects from
// `go list -m -u -json all` into the modules that have an available update,
// skipping the main module(s). Pure.
func parseGoListUpdates(stream string) []outdatedDep {
	type mod struct {
		Path    string
		Version string
		Main    bool
		Update  *struct {
			Version string
		}
	}
	dec := json.NewDecoder(strings.NewReader(stream))
	var out []outdatedDep
	for {
		var m mod
		if err := dec.Decode(&m); err != nil {
			break // EOF or malformed tail — return what parsed
		}
		if m.Main || m.Update == nil || m.Update.Version == "" {
			continue
		}
		out = append(out, outdatedDep{name: m.Path, current: m.Version, latest: m.Update.Version})
	}
	sortDeps(out)
	return out
}

// parseNpmOutdated parses `npm/pnpm outdated --json` — an object keyed by
// package name with current/wanted/latest — into deps whose latest differs from
// what's installed. Handles both the npm and pnpm shapes (same fields). Pure.
func parseNpmOutdated(jsonText string) []outdatedDep {
	text := strings.TrimSpace(jsonText)
	if text == "" || text == "{}" {
		return nil
	}
	var doc map[string]struct {
		Current string `json:"current"`
		Wanted  string `json:"wanted"`
		Latest  string `json:"latest"`
	}
	if json.Unmarshal([]byte(text), &doc) != nil {
		return nil
	}
	var out []outdatedDep
	for name, v := range doc {
		if v.Latest == "" || v.Latest == v.Current {
			continue
		}
		current := v.Current
		if current == "" {
			current = "—"
		}
		out = append(out, outdatedDep{name: name, current: current, latest: v.Latest, wanted: v.Wanted})
	}
	sortDeps(out)
	return out
}

// parseYarnClassicOutdated parses yarn v1's `yarn outdated --json` — NDJSON
// where the row of interest is `{"type":"table","data":{"head":[…],"body":[[…]]}}`
// with columns Package/Current/Wanted/Latest/Package Type/URL. Columns are
// located by header name (defensive against ordering). Pure.
func parseYarnClassicOutdated(text string) []outdatedDep {
	var out []outdatedDep
	dec := json.NewDecoder(strings.NewReader(text))
	for {
		var msg struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := dec.Decode(&msg); err != nil {
			break // EOF or malformed tail
		}
		if msg.Type != "table" {
			continue
		}
		var table struct {
			Head []string   `json:"head"`
			Body [][]string `json:"body"`
		}
		if json.Unmarshal(msg.Data, &table) != nil {
			continue
		}
		col := map[string]int{}
		for i, h := range table.Head {
			col[strings.ToLower(strings.TrimSpace(h))] = i
		}
		nameI, okN := col["package"]
		latI, okL := col["latest"]
		curI, okC := col["current"]
		wantI, okW := col["wanted"]
		typeI, okT := col["package type"]
		if !okN || !okL {
			continue
		}
		for _, row := range table.Body {
			if nameI >= len(row) || latI >= len(row) {
				continue
			}
			current := ""
			if okC && curI < len(row) {
				current = row[curI]
			}
			wanted := ""
			if okW && wantI < len(row) {
				wanted = row[wantI]
			}
			name, latest := row[nameI], row[latI]
			if name == "" || latest == "" || latest == current {
				continue
			}
			dev := okT && typeI < len(row) && strings.Contains(strings.ToLower(row[typeI]), "dev")
			out = append(out, outdatedDep{name: name, current: current, latest: latest, wanted: wanted, dev: dev})
		}
	}
	sortDeps(out)
	return out
}

// yarnUpgradeCommands builds `yarn upgrade --latest name…` for the chosen deps
// (yarn v1; --latest ignores the semver range and keeps each package in its
// existing dependencies/devDependencies section). Pure.
func yarnUpgradeCommands(deps []outdatedDep) [][]string {
	if len(deps) == 0 {
		return nil
	}
	args := []string{"yarn", "upgrade", "--latest"}
	for _, d := range deps {
		args = append(args, d.name)
	}
	return [][]string{args}
}

// parseDotnetOutdated parses `dotnet list package --outdated --format json` into
// per-package deps (deduped across target frameworks, keeping the owning
// project path so upgrades can be applied per project). Pure.
func parseDotnetOutdated(jsonText string) []outdatedDep {
	var doc struct {
		Projects []struct {
			Path       string `json:"path"`
			Frameworks []struct {
				TopLevelPackages []struct {
					ID              string `json:"id"`
					ResolvedVersion string `json:"resolvedVersion"`
					LatestVersion   string `json:"latestVersion"`
				} `json:"topLevelPackages"`
			} `json:"frameworks"`
		} `json:"projects"`
	}
	if json.Unmarshal([]byte(jsonText), &doc) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []outdatedDep
	for _, p := range doc.Projects {
		for _, f := range p.Frameworks {
			for _, pkg := range f.TopLevelPackages {
				if pkg.LatestVersion == "" || pkg.LatestVersion == pkg.ResolvedVersion {
					continue
				}
				key := p.Path + "\x00" + pkg.ID
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, outdatedDep{
					name:    pkg.ID,
					current: pkg.ResolvedVersion,
					latest:  pkg.LatestVersion,
					project: p.Path,
				})
			}
		}
	}
	sortDeps(out)
	return out
}

// parseBunOutdated parses `bun outdated`'s pipe-delimited ASCII table (bun has
// no --json) into deps. Columns are Package | Current | Update | Latest; a
// package name carries a " (dev)"/" (peer)"/… suffix marking its section, which
// is stripped (and recorded for dev). Border/separator/header rows and the
// banner are skipped; rows are deduped by name (workspaces can repeat). Pure.
func parseBunOutdated(text string) []outdatedDep {
	var out []outdatedDep
	seen := map[string]bool{}
	for _, raw := range strings.Split(stripANSI(text), "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "|") {
			continue // banner / blank — not a table row
		}
		cells := splitTableRow(line)
		if len(cells) < 4 {
			continue // top/bottom border (one cell of dashes)
		}
		// Columns: Package | Current | Update (in-range) | Latest.
		name, current, wanted, latest := cells[0], cells[1], cells[2], cells[3]
		if name == "" || name == "Package" || strings.Trim(name, "- ") == "" {
			continue // header or separator row
		}
		dev := false
		if i := strings.Index(name, " ("); i >= 0 {
			dev = strings.Contains(name[i:], "dev")
			name = strings.TrimSpace(name[:i])
		}
		if latest == "" || latest == current || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, outdatedDep{name: name, current: current, latest: latest, wanted: wanted, dev: dev})
	}
	sortDeps(out)
	return out
}

// splitTableRow splits a "| a | b | c |" row into its trimmed inner cells,
// dropping the empty fragments before the first and after the last pipe. Pure.
func splitTableRow(line string) []string {
	parts := strings.Split(line, "|")
	if len(parts) >= 2 {
		parts = parts[1 : len(parts)-1] // drop the empties outside the outer pipes
	}
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

// stripANSI removes ANSI SGR escape sequences (bun colorizes on a TTY). Pure.
func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// sortDeps orders deps by project then name for a stable picker. Pure.
func sortDeps(deps []outdatedDep) {
	sort.SliceStable(deps, func(i, j int) bool {
		if deps[i].project != deps[j].project {
			return deps[i].project < deps[j].project
		}
		return deps[i].name < deps[j].name
	})
}

// goUpgradeCommands builds the commands to upgrade the chosen go modules: a
// single `go get path@version …` followed by `go mod tidy`. Pure.
func goUpgradeCommands(deps []outdatedDep) [][]string {
	if len(deps) == 0 {
		return nil
	}
	get := []string{"go", "get"}
	for _, d := range deps {
		get = append(get, d.name+"@"+d.latest)
	}
	return [][]string{get, {"go", "mod", "tidy"}}
}

// nodeUpgradeCommands builds the single package-manager command to upgrade the
// chosen node deps to their latest, using the manager's add/install verb
// (npm install · pnpm/yarn/bun add) with explicit name@version specs. Pure.
func nodeUpgradeCommands(pm detect.NodePM, deps []outdatedDep) [][]string {
	if len(deps) == 0 {
		return nil
	}
	base := []string{string(pm), "add"}
	if pm == detect.NPM {
		base = []string{"npm", "install"}
	}
	for _, d := range deps {
		base = append(base, d.name+"@"+d.latest)
	}
	return [][]string{base}
}

// bunUpgradeCommands builds `bun add name@latest …` for the chosen deps,
// splitting dev dependencies into a `bun add --dev …` so they stay in
// devDependencies. At most two commands (prod, dev). Pure.
func bunUpgradeCommands(deps []outdatedDep) [][]string {
	var prod, dev []string
	for _, d := range deps {
		spec := d.name + "@" + d.latest
		if d.dev {
			dev = append(dev, spec)
		} else {
			prod = append(prod, spec)
		}
	}
	var cmds [][]string
	if len(prod) > 0 {
		cmds = append(cmds, append([]string{"bun", "add"}, prod...))
	}
	if len(dev) > 0 {
		cmds = append(cmds, append([]string{"bun", "add", "--dev"}, dev...))
	}
	return cmds
}

// cargoUpdateRe matches a `cargo update --dry-run` upgrade line, e.g.
// "    Updating foo v1.0.0 -> v1.2.0" (also "Upgrading"), capturing name, the
// current version, and the in-range target. Lines without "-> v" (Updating the
// index, Adding/Removing transitive deps) don't match. Anchored to the keyword
// so stray output can't be misread as an upgrade.
var cargoUpdateRe = regexp.MustCompile(`(?m)^\s*(?:Updating|Upgrading)\s+(\S+)\s+v(\S+)\s+->\s+v(\S+)`)

// parseCargoUpdateDryRun parses `cargo update --dry-run` (the ground truth for a
// range-respecting upgrade: it reports exactly the Cargo.lock changes that stay
// within Cargo.toml's ranges) into deps. The target is in-range, so it's stored
// as both wanted and latest. Pure.
func parseCargoUpdateDryRun(text string) []outdatedDep {
	var out []outdatedDep
	for _, m := range cargoUpdateRe.FindAllStringSubmatch(text, -1) {
		out = append(out, outdatedDep{name: m[1], current: m[2], wanted: m[3], latest: m[3]})
	}
	sortDeps(out)
	return out
}

// dotnetUpgradeCommands builds one `dotnet add [project] package id --version`
// per chosen package (upgrades are per project). Pure.
func dotnetUpgradeCommands(deps []outdatedDep) [][]string {
	var cmds [][]string
	for _, d := range deps {
		args := []string{"dotnet", "add"}
		if d.project != "" {
			args = append(args, d.project)
		}
		args = append(args, "package", d.name, "--version", d.latest)
		cmds = append(cmds, args)
	}
	return cmds
}
