package pipeline

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ReleasePackage is one package in the release, carrying the values that back
// the built-in ${version.*} / ${changelog.*} / ${tag.*} / ${releaseUrl.*}
// interpolation variables.
type ReleasePackage struct {
	// Name is the full manifest name, e.g. "@acme/web". Used in the ${versions}
	// aggregate and accepted as an exact address alias.
	Name string
	// Key is the short address used in ${version.<key>} (e.g. "web").
	Key string
	// Ecosystem is the ecosystem id ("node" | "dotnet" | "go" | "rust").
	Ecosystem string
	// Version is the package's new version for this release.
	Version string
	// Tag is the package's git tag (e.g. "@acme/web@2.1.0").
	Tag string
	// Changelog is the release-notes body for this version, if any.
	Changelog string
}

// IssueRef is an issue the release resolves, with the branch name shiprig's
// issue automation would use (the ${issueBranch} value).
type IssueRef struct {
	Number int
	Branch string
}

// ReleaseContext supplies the built-in interpolation values the engine cannot
// compute itself (versions, changelog, tags, forge URLs, issues). The host
// implements it; methods are called lazily during a run. ReleaseURL returns ""
// until the forge release step has created the release, so a value that depends
// on it should be referenced from a later step's command.
type ReleaseContext interface {
	// Packages returns the released packages. Order is not significant — the
	// engine sorts by Name for aggregates and error messages.
	Packages() []ReleasePackage
	// ReleaseURL returns the forge release URL for the package addressed by key
	// (its Key or full Name), or "" when not yet created / forge disabled.
	ReleaseURL(key string) string
	// Issues returns the issues this release resolves (empty when issue
	// automation is disabled).
	Issues() []IssueRef
}

// releaseVars resolves the built-in ${...} release variables from a
// ReleaseContext. It holds no cache: the host implementation owns any caching
// of expensive lookups, and forge URLs legitimately change during a run (empty
// before the release step, populated after), so re-reading per reference is
// correct.
type releaseVars struct {
	ctx ReleaseContext
}

func newReleaseVars(ctx ReleaseContext) *releaseVars {
	if ctx == nil {
		return nil
	}
	return &releaseVars{ctx: ctx}
}

// refPattern finds every ${...} placeholder so its inner key can be inspected.
var refPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// extractRefs returns the distinct inner keys of a command's ${...} placeholders
// in first-appearance order (e.g. "version.web", "versions", "env.TOKEN").
func extractRefs(command CommandSpec) []string {
	seen := map[string]bool{}
	var keys []string
	collect := func(text string) {
		for _, m := range refPattern.FindAllStringSubmatch(text, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				keys = append(keys, m[1])
			}
		}
	}
	if command.IsShell() {
		collect(command.Shell())
	} else {
		for _, token := range command.Argv() {
			collect(token)
		}
	}
	return keys
}

// resolve returns the value for a release-variable key. The second result is
// false when key is not a release variable (so the caller lets it fall through
// to ${vars.*}/${env.*}/${tool}). A non-nil error is a usage error (an ambiguous
// bare form in a multi-package release, or an unknown package/issue address) and
// should fail the command.
func (rv *releaseVars) resolve(key string) (string, bool, error) {
	switch {
	case key == "versions":
		return rv.aggregate(func(p ReleasePackage) string { return p.Name + "@" + p.Version }), true, nil
	case key == "tags":
		return rv.aggregate(func(p ReleasePackage) string { return p.Tag }), true, nil
	case key == "releaseUrls":
		return rv.aggregate(func(p ReleasePackage) string { return rv.ctx.ReleaseURL(p.Key) }), true, nil
	case key == "issues":
		return rv.issues(), true, nil

	case key == "version" || strings.HasPrefix(key, "version."):
		return rv.scalar(key, "version", "versions", func(p ReleasePackage) string { return p.Version })
	case key == "changelog" || strings.HasPrefix(key, "changelog."):
		return rv.scalar(key, "changelog", "", func(p ReleasePackage) string { return p.Changelog })
	case key == "tag" || strings.HasPrefix(key, "tag."):
		return rv.scalar(key, "tag", "tags", func(p ReleasePackage) string { return p.Tag })
	case key == "releaseUrl" || strings.HasPrefix(key, "releaseUrl."):
		return rv.scalar(key, "releaseUrl", "releaseUrls", func(p ReleasePackage) string { return rv.ctx.ReleaseURL(p.Key) })
	case key == "issueBranch" || strings.HasPrefix(key, "issueBranch."):
		return rv.issueBranch(key)

	default:
		return "", false, nil
	}
}

// scalar resolves a per-package variable in either addressed (ns.<addr>) or bare
// form. The bare form resolves only when the release is single-package; in a
// multi-package release it errors with guidance toward the addressed form and,
// when one exists, the aggregate form (aggName, "" when there is none).
func (rv *releaseVars) scalar(key, ns, aggName string, field func(ReleasePackage) string) (string, bool, error) {
	pkgs := rv.ctx.Packages()

	if addr, ok := strings.CutPrefix(key, ns+"."); ok {
		p, found := pkgByAddress(pkgs, addr)
		if !found {
			return "", true, fmt.Errorf("${%s} refers to unknown package %q; released packages: %s",
				key, addr, strings.Join(packageNames(pkgs), ", "))
		}
		return field(p), true, nil
	}

	if len(pkgs) == 1 {
		return field(pkgs[0]), true, nil
	}

	suggestion := fmt.Sprintf("use ${%s.<package>}", ns)
	if aggName != "" {
		suggestion += fmt.Sprintf(" for a specific one, or ${%s} for all", aggName)
	}
	return "", true, fmt.Errorf("${%s} is ambiguous: this release includes %d packages (%s); %s",
		ns, len(pkgs), strings.Join(packageNames(pkgs), ", "), suggestion)
}

// aggregate joins field(p) over the released packages, sorted by package name,
// skipping empty values (e.g. a forge URL not yet known).
func (rv *releaseVars) aggregate(field func(ReleasePackage) string) string {
	pkgs := sortedByName(rv.ctx.Packages())
	out := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		if v := field(p); v != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, ", ")
}

// issues renders the resolved issues as "#1, #2", sorted and deduped.
func (rv *releaseVars) issues() string {
	seen := map[int]bool{}
	var nums []int
	for _, iss := range rv.ctx.Issues() {
		if !seen[iss.Number] {
			seen[iss.Number] = true
			nums = append(nums, iss.Number)
		}
	}
	sort.Ints(nums)
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = "#" + strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

// issueBranch resolves ${issueBranch} (the single resolved issue's branch) or
// ${issueBranch.<number>} (a specific one). Zero issues yields "" rather than an
// error, so referencing it never forces the issues feature on; more than one
// issue makes the bare form ambiguous.
func (rv *releaseVars) issueBranch(key string) (string, bool, error) {
	issues := rv.ctx.Issues()

	if addr, ok := strings.CutPrefix(key, "issueBranch."); ok {
		n, err := strconv.Atoi(addr)
		if err != nil {
			return "", true, fmt.Errorf("${%s}: issue address %q is not a number", key, addr)
		}
		for _, iss := range issues {
			if iss.Number == n {
				return iss.Branch, true, nil
			}
		}
		return "", true, fmt.Errorf("${%s} refers to issue #%d, which this release does not resolve", key, n)
	}

	switch len(issues) {
	case 0:
		return "", true, nil
	case 1:
		return issues[0].Branch, true, nil
	default:
		return "", true, fmt.Errorf("${issueBranch} is ambiguous: this release resolves %d issues; use ${issueBranch.<number>}", len(issues))
	}
}

// pkgByAddress finds a package by its short Key or, as an exact alias, its full
// Name.
func pkgByAddress(pkgs []ReleasePackage, addr string) (ReleasePackage, bool) {
	for _, p := range pkgs {
		if p.Key == addr {
			return p, true
		}
	}
	for _, p := range pkgs {
		if p.Name == addr {
			return p, true
		}
	}
	return ReleasePackage{}, false
}

// isReleaseURLKey reports whether key addresses a forge release URL, which is
// unknowable until the release step runs — so the dry-run preview shows it as a
// placeholder rather than resolving it.
func isReleaseURLKey(key string) bool {
	return key == "releaseUrl" || key == "releaseUrls" || strings.HasPrefix(key, "releaseUrl.")
}

func packageNames(pkgs []ReleasePackage) []string {
	names := make([]string, len(pkgs))
	for i, p := range pkgs {
		names[i] = p.Name
	}
	sort.Strings(names)
	return names
}

func sortedByName(pkgs []ReleasePackage) []ReleasePackage {
	out := append([]ReleasePackage(nil), pkgs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
