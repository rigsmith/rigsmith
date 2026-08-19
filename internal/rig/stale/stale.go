// Package stale answers the one question `rig verify` exists for: were the
// artifacts I am about to trust built from the code I have?
//
// build, test and run each produce or consume artifacts, and nothing checks
// that the ones in play were produced together — so every verb can answer its
// own question honestly while the answers are collectively wrong (a test binary
// two hours older than the resources it loads; an app bundle with a fresh dylib
// beside a stale .pak). The checks here compare modification times rather than
// rebuilding, so the answer costs a second even on a repo whose build costs
// hours — which is exactly where stale artifacts survive longest.
//
// Two kinds of check live here:
//
//   - the generic one, free for every repo rig understands: is anything under
//     the source tree newer than the newest build output?
//   - the declared one, for the artifacts rig cannot infer (generated
//     resources, multi-artifact builds, an out/ tree beside the repo): each
//     `artifacts` entry in .rig.json names a path and the globs it is built
//     from.
//
// Nothing here runs a command or touches a file — it reads mtimes and reports.
package stale

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/walkutil"
)

// Status is one check's verdict.
type Status int

const (
	// OK — the artifact is at least as new as everything it is built from.
	OK Status = iota
	// Stale — something the artifact is built from is newer than the artifact.
	Stale
	// Skipped — the check could not run (nothing built yet, no inputs matched,
	// no output directory for this ecosystem). Reported, never silent: silence
	// about a check that did not run is how a green result becomes misleading.
	Skipped
)

// Artifact is one entry from `.rig.json`'s `artifacts` block: something built,
// plus the globs it is built from. Anything under Path older than the newest
// file matching Inputs is stale.
type Artifact struct {
	// Name labels the artifact in the report (the block's key).
	Name string
	// Path is the file or directory produced, relative to the repo root or
	// absolute. It may sit outside the repo (`../out/Release/App.app`).
	Path string
	// Inputs are globs relative to the repo root. `*` and `?` stay inside a
	// path segment; `**` spans directories, so `**/*.cc` matches at any depth.
	Inputs []string
}

// Finding is one check's result, structured so the caller renders it and tests
// assert on it. Newest/Oldest are display paths (repo-relative where possible).
type Finding struct {
	// Name is the check's label: an artifact name, or a built-in check's name.
	Name string
	// Status is the verdict.
	Status Status
	// Target is the display path of what was checked.
	Target string
	// Reason says why the check could not run (Status == Skipped).
	Reason string

	// Newest is the newest input — what the artifact should have been built from.
	Newest   string
	NewestAt time.Time
	// Oldest is the out-of-date file: the artifact itself, or (for a directory
	// artifact) the oldest file under it.
	Oldest   string
	OldestAt time.Time
	// AlsoOld counts further out-of-date files under a directory artifact,
	// beyond the one named in Oldest.
	AlsoOld int
	// Unreadable names a directory the scan could not descend into. A clean
	// verdict requires a complete read, so an unreadable directory turns an
	// otherwise-OK check into a Skipped one (see vouch).
	Unreadable string
}

// Detail renders the human explanation for a finding: why it is stale, or why
// the check did not run. Empty for an OK finding, which needs no explanation
// beyond its name.
func (f Finding) Detail() string {
	switch f.Status {
	case Skipped:
		return f.Reason
	case Stale:
		d := fmt.Sprintf("%s is %s older than %s", f.Oldest, roughly(f.NewestAt.Sub(f.OldestAt)), f.Newest)
		if f.AlsoOld == 1 {
			d += " (and 1 more file)"
		} else if f.AlsoOld > 1 {
			d += fmt.Sprintf(" (and %d more files)", f.AlsoOld)
		}
		if f.Unreadable != "" {
			// The staleness is proven either way; say what wasn't read so the
			// count isn't mistaken for the whole story.
			d += fmt.Sprintf("; could not read %s", f.Unreadable)
		}
		return d
	case OK:
		if f.Newest == "" {
			return ""
		}
		return "up to date with " + f.Newest
	default:
		return ""
	}
}

// roughly renders a duration the way a person would say it: "3s", "12m", "2h",
// "4d". Exactness is not the point — the order of magnitude is.
func roughly(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Round(time.Second).Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// AnyStale reports whether any finding came back stale — the gate `rig verify`
// exits non-zero on.
func AnyStale(findings []Finding) bool {
	for _, f := range findings {
		if f.Status == Stale {
			return true
		}
	}
	return false
}

// Check runs every agreement check for the repo at root: the generic
// source-newer-than-output check for the ecosystem, the ecosystem's extra check
// (Node's lockfile vs node_modules), then one check per declared artifact
// (sorted by name, so the report is stable).
//
// A repo with no `artifacts` block is not an error — it gets the generic checks
// and, where one could not run, the reason.
func Check(root, eco string, artifacts []Artifact) []Finding {
	out := []Finding{CheckOutput(root, eco)}
	if f, ok := CheckDeps(root, eco); ok {
		out = append(out, f)
	}
	return append(out, CheckArtifacts(root, artifacts)...)
}

// CheckArtifacts checks every declared artifact, sorted by name so the report
// is stable run to run.
func CheckArtifacts(root string, artifacts []Artifact) []Finding {
	sorted := append([]Artifact(nil), artifacts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	out := make([]Finding, 0, len(sorted))
	for _, a := range sorted {
		out = append(out, CheckArtifact(root, a))
	}
	return out
}

// CheckArtifact checks one declared artifact: the newest file matching its
// input globs against the OLDEST file under its path.
//
// Oldest, not newest, is the point for a directory: an app bundle whose newest
// file is minutes old can still hold a resource from two hours ago that the
// build never refreshed — the bundle looks fresh and loads stale data. Naming
// the specific out-of-date file (and counting the rest) is what turns "some
// test crashed in code nobody touched" into a diagnosis.
func CheckArtifact(root string, a Artifact) Finding {
	f := Finding{Name: a.Name}
	if strings.TrimSpace(a.Path) == "" {
		f.Status, f.Reason = Skipped, `no "path" declared`
		return f
	}
	abs := a.Path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, filepath.FromSlash(a.Path))
	}
	f.Target = display(root, abs)
	if _, err := os.Stat(abs); err != nil {
		f.Status, f.Reason = Skipped, fmt.Sprintf("%s does not exist — never built?", f.Target)
		return f
	}
	if len(a.Inputs) == 0 {
		f.Status, f.Reason = Skipped, `no "inputs" declared — nothing to compare against`
		return f
	}

	// The artifact's own tree is never one of its inputs: a bundle full of
	// generated files would otherwise be compared against itself and always
	// look stale.
	in := scanInputs(root, a.Inputs, []string{abs})
	if in.matched == 0 {
		f.Status, f.Reason = Skipped, fmt.Sprintf("no files match %s", strings.Join(a.Inputs, ", "))
		return f
	}
	f.Newest, f.NewestAt = display(root, in.newest), in.newestAt

	art, err := scanArtifact(abs, in.newestAt)
	switch {
	case err != nil:
		f.Status, f.Reason = Skipped, fmt.Sprintf("could not read %s: %v", f.Target, err)
	case art.total == 0:
		f.Status, f.Reason = Skipped, fmt.Sprintf("%s holds no files", f.Target)
	case art.older > 0:
		f.Status = Stale
		f.Oldest, f.OldestAt, f.AlsoOld = display(root, art.oldest), art.oldestAt, art.older-1
	default:
		f.Status = OK
	}
	return vouch(root, f, in.unreadable, art.unreadable)
}

// vouch downgrades an otherwise-clean verdict to Skipped when part of what the
// comparison needed could not be read. Evidence of staleness stands either way
// — a file that IS older than its input is older whatever else was hidden — but
// "everything is up to date" is a claim about files we never saw, and this verb
// exists precisely to stop such claims from exiting zero.
func vouch(root string, f Finding, unreadable ...[]string) Finding {
	var missed string
	for _, list := range unreadable {
		if len(list) > 0 {
			missed = display(root, list[0])
			break
		}
	}
	if missed == "" {
		return f
	}
	f.Unreadable = missed
	if f.Status == OK {
		f.Status = Skipped
		f.Reason = fmt.Sprintf("could not read %s — cannot vouch for the comparison", missed)
	}
	return f
}

// OutputCheckName is the generic check's label in the report — exported so a
// caller that cannot resolve an ecosystem can report the same check as skipped
// under the same name.
const OutputCheckName = "build output"

// CheckOutput runs the generic check every rig ecosystem gets for free: is
// anything under the source tree newer than the newest build output? It is the
// "you edited and didn't rebuild" case — the check people most often skip —
// and it needs no configuration at all.
//
// Newest output, not oldest, here: the generic check has no per-artifact
// dependency knowledge, so it asks the weaker, no-false-positive question.
// Declared artifacts are where the strict version lives.
func CheckOutput(root, eco string) Finding {
	f := Finding{Name: OutputCheckName}
	globs := sourceGlobs(eco)
	if len(globs) == 0 {
		f.Status, f.Reason = Skipped, fmt.Sprintf("no source convention for ecosystem %q", eco)
		return f
	}
	dirs := outputDirs(root, eco)
	if len(dirs) == 0 {
		f.Status, f.Reason = Skipped, fmt.Sprintf("no build output found (looked for %s) — nothing built yet?", strings.Join(outputNames(eco), ", "))
		return f
	}
	f.Target = strings.Join(displayAll(root, dirs), ", ")

	built := newestUnder(dirs)
	if built.count == 0 {
		f.Status, f.Reason = Skipped, fmt.Sprintf("%s holds no files — nothing built yet?", f.Target)
		return f
	}
	// Build output is never source. walkutil prunes some output directories by
	// default but not all of them (`build`, `out`, `.output`, `.svelte-kit` are
	// not in its set), so a bundled .js or .css would otherwise count as a
	// source newer than the output it came from — a permanent false "stale".
	in := scanInputs(root, globs, dirs)
	if in.matched == 0 {
		f.Status, f.Reason = Skipped, "no source files found to compare against"
		return f
	}
	f.Newest, f.NewestAt = display(root, in.newest), in.newestAt
	f.Oldest, f.OldestAt = display(root, built.newest), built.newestAt
	if in.newestAt.After(built.newestAt) {
		f.Status = Stale
	} else {
		f.Status = OK
	}
	return vouch(root, f, in.unreadable, built.unreadable)
}

// DepsCheckName is the dependency check's label in the report.
const DepsCheckName = "dependencies"

// CheckDeps compares an installed dependency tree against its lockfile — the
// second half of the Node row in the design's table (`node_modules` vs
// lockfile). ok=false for ecosystems with no such split (Go, .NET and Cargo
// resolve from the manifest at build time, so their build output check already
// covers it).
func CheckDeps(root, eco string) (Finding, bool) {
	if eco != "node" {
		return Finding{}, false
	}
	f := Finding{Name: DepsCheckName}
	lock, lockAt := newestOf(root, "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "bun.lock")
	if lock == "" {
		return Finding{}, false // no lockfile — nothing this check can say
	}
	f.Newest, f.NewestAt = display(root, lock), lockAt

	mods := filepath.Join(root, "node_modules")
	f.Target = "node_modules"
	if _, err := os.Stat(mods); err != nil {
		f.Status, f.Reason = Skipped, "node_modules is missing — run `rig install`"
		return f, true
	}
	// The package managers each stamp a state file when they finish installing;
	// it is a far better "when was this tree installed" signal than the
	// directory mtime, which any stray write bumps.
	stamp, stampAt := newestOf(mods, ".package-lock.json", ".modules.yaml", ".yarn-state.yml")
	if stamp == "" {
		stamp = mods
		info, err := os.Stat(mods)
		if err != nil {
			f.Status, f.Reason = Skipped, fmt.Sprintf("could not read node_modules: %v", err)
			return f, true
		}
		stampAt = info.ModTime()
	}
	f.Oldest, f.OldestAt = display(root, stamp), stampAt
	if lockAt.After(stampAt) {
		f.Status = Stale
	} else {
		f.Status = OK
	}
	return f, true
}

// inputScan is what a pass over the input globs found.
type inputScan struct {
	newest     string
	newestAt   time.Time
	matched    int
	unreadable []string
}

// scanInputs finds the newest file under root matching any glob. The walk is
// gitignore-aware and prunes the usual noise (node_modules, bin/obj/target/dist,
// .git) plus every directory in skip, and it reports what it could not read so
// the caller never draws a clean verdict from a partial scan.
func scanInputs(root string, globs, skip []string) inputScan {
	scan := inputScan{}
	m := newMatcher(globs)
	unreadable, _ := walkutil.WalkReport(root, skip, func(p string, d fs.DirEntry) error {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		if !m.match(filepath.ToSlash(rel)) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			scan.unreadable = append(scan.unreadable, p)
			return nil
		}
		scan.matched++
		if info.ModTime().After(scan.newestAt) {
			scan.newest, scan.newestAt = p, info.ModTime()
		}
		return nil
	})
	scan.unreadable = append(scan.unreadable, unreadable...)
	return scan
}

// outputScan is what a pass over the build-output directories found.
type outputScan struct {
	newest     string
	newestAt   time.Time
	count      int
	unreadable []string
}

// newestUnder returns the newest file under any of dirs, the file count, and any
// directory it could not descend into (which could be hiding a newer output).
func newestUnder(dirs []string) outputScan {
	scan := outputScan{}
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				scan.unreadable = append(scan.unreadable, p)
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil {
				scan.unreadable = append(scan.unreadable, p)
				return nil
			}
			scan.count++
			if info.ModTime().After(scan.newestAt) {
				scan.newest, scan.newestAt = p, info.ModTime()
			}
			return nil
		})
	}
	return scan
}

// artifactScan is what a pass over one artifact found.
type artifactScan struct {
	oldest     string
	oldestAt   time.Time
	older      int // files older than the cutoff
	total      int
	unreadable []string
}

// scanArtifact walks the artifact at path in one pass. A regular file is its own
// single entry. Unreadable subdirectories are recorded rather than ignored: an
// unreadable directory inside a bundle could hold exactly the stale resource
// this check exists to find, so a clean verdict must not be drawn over it.
func scanArtifact(path string, cutoff time.Time) (artifactScan, error) {
	scan := artifactScan{}
	info, err := os.Stat(path)
	if err != nil {
		return scan, err
	}
	if !info.IsDir() {
		if cutoff.After(info.ModTime()) {
			scan.older = 1
		}
		scan.oldest, scan.oldestAt, scan.total = path, info.ModTime(), 1
		return scan, nil
	}
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			scan.unreadable = append(scan.unreadable, p)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		fi, ierr := d.Info()
		if ierr != nil {
			scan.unreadable = append(scan.unreadable, p)
			return nil
		}
		scan.total++
		if cutoff.After(fi.ModTime()) {
			scan.older++
		}
		if scan.oldest == "" || fi.ModTime().Before(scan.oldestAt) {
			scan.oldest, scan.oldestAt = p, fi.ModTime()
		}
		return nil
	})
	return scan, err
}

// newestOf returns the newest of the named files that exist directly in dir.
func newestOf(dir string, names ...string) (path string, mod time.Time) {
	for _, name := range names {
		p := filepath.Join(dir, name)
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if path == "" || info.ModTime().After(mod) {
			path, mod = p, info.ModTime()
		}
	}
	return path, mod
}

// display renders p relative to root — including a short climb out of it, so
// the `../out/Release/App.app` a config declared is echoed back as written.
// A path that isn't under root at all (a long climb, or a different volume)
// stays absolute rather than turning into ../../../.. noise.
func display(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil || strings.Count(rel, ".."+string(filepath.Separator)) > maxDisplayClimb {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(rel)
}

// maxDisplayClimb is how many leading `../` segments a display path may have
// before the absolute path reads better.
const maxDisplayClimb = 2

// displayAll maps display over paths.
func displayAll(root string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, display(root, p))
	}
	return out
}

// matcher is a compiled set of input globs.
type matcher struct{ res []*regexp.Regexp }

// newMatcher compiles globs once, so the walk doesn't re-parse them per file.
// An invalid pattern is dropped rather than failing the check.
func newMatcher(globs []string) matcher {
	m := matcher{}
	for _, g := range globs {
		if re, err := regexp.Compile(globRegexp(g)); err == nil {
			m.res = append(m.res, re)
		}
	}
	return m
}

// match reports whether the root-relative slash path matches any glob.
func (m matcher) match(rel string) bool {
	for _, re := range m.res {
		if re.MatchString(rel) {
			return true
		}
	}
	return false
}

// globRegexp translates a glob to an anchored, case-insensitive regexp:
// `*`/`?` stay inside a path segment, `**` spans directories, and `**/`
// matches zero or more directories (so `**/*.cc` matches `a.cc` as well as
// `src/x/a.cc`). Case-insensitive to match rig's other config globs — and the
// filesystems most of these repos live on.
func globRegexp(glob string) string {
	g := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(glob)), "./")
	var b strings.Builder
	b.WriteString("(?i)^")
	for i := 0; i < len(g); {
		switch {
		case strings.HasPrefix(g[i:], "**/"):
			b.WriteString("(?:[^/]*/)*")
			i += 3
		case strings.HasPrefix(g[i:], "**"):
			b.WriteString(".*")
			i += 2
		case g[i] == '*':
			b.WriteString("[^/]*")
			i++
		case g[i] == '?':
			b.WriteString("[^/]")
			i++
		default:
			b.WriteString(regexp.QuoteMeta(string(g[i])))
			i++
		}
	}
	b.WriteString("$")
	return b.String()
}
