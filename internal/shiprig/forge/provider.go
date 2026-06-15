package forge

import "strings"

// Release is the forge-agnostic description of a release to create.
type Release struct {
	Tag   string
	Title string
	Notes string
}

// Provider is one forge backend (GitHub / GitLab / Gitea). It follows the
// "orchestrate, don't reimplement" stance: every method drives the forge's
// official CLI.
//
// The split of responsibilities is deliberate:
//   - Probes (Matches/Ready/ReleaseExists) execute through the Runner and
//     interpret the result, so each forge owns its own detection quirks (e.g.
//     Gitea has no `release view`, so it lists + matches).
//   - Mutations are expressed as argv (CreateReleaseCmd/UploadAssetsCmd) and
//     handed back unexecuted, so forge.Run stays the single place that runs and
//     reports them — preserving one progress/idempotency loop across all forges.
type Provider interface {
	// Name is the config spelling: "github" | "gitlab" | "gitea".
	Name() string
	// Matches reports whether origin's URL belongs to this forge's SaaS host, for
	// `forge: auto`. Self-hosted hosts are unsniffable, so a self-hosted provider
	// returns false and must be selected explicitly.
	Matches(remoteURL string) bool
	// Ready reports whether the forge's CLI is installed and authenticated. A
	// missing/unauthenticated CLI is "not ready" (degrade to tags-only), never an
	// error.
	Ready(repoRoot string, run Runner) bool
	// ReleaseExists reports whether a release for tag already exists (idempotency).
	ReleaseExists(tag, repoRoot string, run Runner) bool
	// CreateReleaseCmd is the argv that creates the release.
	CreateReleaseCmd(r Release) []string
	// UploadAssetsCmd is the argv that attaches files to an existing release, or
	// nil when this forge can't upload to an already-created release (the caller
	// reports a skip rather than failing the run).
	UploadAssetsCmd(tag string, files []string) []string
	// ReleaseURLCmd is the argv that prints just the release's web URL for tag,
	// for the ${releaseUrl} variable. nil when the forge's CLI has no stable way
	// to print it (the caller resolves the URL to "").
	ReleaseURLCmd(tag string) []string
}

// defaultProviders is the auto-detection order: GitHub, then GitLab, then Gitea
// (Gitea never auto-matches — it's explicit-only — so its position is moot).
func defaultProviders() []Provider {
	return []Provider{githubProvider{}, gitlabProvider{}, giteaProvider{}}
}

// providerByName returns the provider with the given config name, or nil.
func providerByName(name string) Provider {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, p := range defaultProviders() {
		if p.Name() == want {
			return p
		}
	}
	return nil
}

// --- GitHub (gh) -------------------------------------------------------------

type githubProvider struct{}

func (githubProvider) Name() string { return "github" }
func (githubProvider) Matches(u string) bool {
	return strings.Contains(strings.ToLower(u), "github.com")
}
func (githubProvider) Ready(root string, run Runner) bool {
	_, err := run(root, "gh", "auth", "status")
	return err == nil
}
func (githubProvider) ReleaseExists(tag, root string, run Runner) bool {
	_, err := run(root, "gh", "release", "view", tag)
	return err == nil
}
func (githubProvider) CreateReleaseCmd(r Release) []string {
	return []string{"gh", "release", "create", r.Tag, "--title", r.Title, "--notes", r.Notes}
}
func (githubProvider) UploadAssetsCmd(tag string, files []string) []string {
	argv := append([]string{"gh", "release", "upload", tag}, files...)
	return append(argv, "--clobber") // idempotent: re-runs replace assets
}
func (githubProvider) ReleaseURLCmd(tag string) []string {
	return []string{"gh", "release", "view", tag, "--json", "url", "--jq", ".url"}
}

// --- GitLab (glab) -----------------------------------------------------------

type gitlabProvider struct{}

func (gitlabProvider) Name() string { return "gitlab" }
func (gitlabProvider) Matches(u string) bool {
	return strings.Contains(strings.ToLower(u), "gitlab.com")
}
func (gitlabProvider) Ready(root string, run Runner) bool {
	_, err := run(root, "glab", "auth", "status")
	return err == nil
}
func (gitlabProvider) ReleaseExists(tag, root string, run Runner) bool {
	_, err := run(root, "glab", "release", "view", tag)
	return err == nil
}
func (gitlabProvider) CreateReleaseCmd(r Release) []string {
	return []string{"glab", "release", "create", r.Tag, "--name", r.Title, "--notes", r.Notes}
}
func (gitlabProvider) UploadAssetsCmd(tag string, files []string) []string {
	return append([]string{"glab", "release", "upload", tag}, files...)
}

// ReleaseURLCmd: glab has no stable single-value URL output, so ${releaseUrl}
// resolves to "" on GitLab (a follow-up if needed).
func (gitlabProvider) ReleaseURLCmd(string) []string { return nil }

// --- Gitea (tea) -------------------------------------------------------------

type giteaProvider struct{}

func (giteaProvider) Name() string { return "gitea" }

// Gitea is typically self-hosted on an arbitrary host, which can't be sniffed
// from origin — so it never auto-matches and must be selected explicitly with
// `forge: gitea` (+ forgeURL).
func (giteaProvider) Matches(string) bool { return false }
func (giteaProvider) Ready(root string, run Runner) bool {
	_, err := run(root, "tea", "login", "list")
	return err == nil
}

// ReleaseExists: tea has no `release view <tag>`, so list releases and match the
// tag as a whitespace-delimited field in the output.
func (giteaProvider) ReleaseExists(tag, root string, run Runner) bool {
	out, err := run(root, "tea", "release", "list")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		for _, field := range strings.Fields(line) {
			if field == tag {
				return true
			}
		}
	}
	return false
}
func (giteaProvider) CreateReleaseCmd(r Release) []string {
	return []string{"tea", "release", "create", "--tag", r.Tag, "--title", r.Title, "--note", r.Notes}
}

// UploadAssetsCmd: tea attaches assets only at create time (`--asset`), with no
// stable "upload to an existing release by tag" — so attaching to an
// already-created release is unsupported. nil ⇒ the caller reports a skip.
func (giteaProvider) UploadAssetsCmd(string, []string) []string { return nil }

// ReleaseURLCmd: tea has no stable single-value URL output, so ${releaseUrl}
// resolves to "" on Gitea (a follow-up if needed).
func (giteaProvider) ReleaseURLCmd(string) []string { return nil }
