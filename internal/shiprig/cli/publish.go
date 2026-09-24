package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/rigsmith/rigsmith/core/auth"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/prestate"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newPublishCmd publishes each discovered package to its ecosystem's registry
// (idempotently — already-published versions are skipped), then creates and
// pushes a git tag per package. Go modules have no registry push; they are
// published purely by the tag (module/vX.Y.Z), which the module proxy serves.
type publishResult struct {
	pkg     plugin.Package
	resp    plugin.PublishResponse
	err     error
	skipped bool // not ours to publish: ignored, or no ecosystem
}

// report renders one package's outcome. Both the concurrent and sequential
// paths call it, so a dry run and the publish it previews cannot drift into
// different wordings.
func report(out io.Writer, r publishResult) {
	if r.skipped {
		return
	}
	switch {
	case r.resp.Published:
		fmt.Fprintf(out, "%s %s@%s  %s\n", commands.PatchStyle.Render("published"), r.pkg.Name, r.pkg.Version, commands.DimStyle.Render(r.resp.Message))
	case r.resp.Skipped:
		fmt.Fprintf(out, "%s %s@%s  %s\n", commands.DimStyle.Render("skipped  "), r.pkg.Name, r.pkg.Version, commands.DimStyle.Render(r.resp.Message))
	default:
		fmt.Fprintf(out, "%s %s@%s  %s\n", commands.DimStyle.Render("·        "), r.pkg.Name, r.pkg.Version, commands.DimStyle.Render(r.resp.Message))
	}
}

// dryRunProbeLimit bounds how many packages a dry run probes at once. Each
// probe is one registry round trip; the point is to stop waiting for them one
// at a time, not to open a connection per package at a registry that has its
// own opinion about that.
const dryRunProbeLimit = 8

func newPublishCmd() *cobra.Command {
	var (
		dryRun     bool
		noGitTag   bool
		noPush     bool
		outputPath string
		access     string
		yes        bool
		npmAuth    string
		packDir    string
		distTag    string
	)
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish packages to their registries and tag the release",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := commands.Open()
			if err != nil {
				return err
			}
			// The event sink is resolved once, here, from the flag or the
			// process environment: canon never reads .env, so a
			// CHANGESETS_OUTPUT that only .env sets (loaded below) is not one.
			// It is opened before any registry is touched, and with
			// --no-git-tag too, as `changeset publish` does (see
			// tagEvents.ready). A dry run writes nothing, the file included.
			events := openTagEvents(outputPath)
			if events != nil && !dryRun {
				if err := events.ready(); err != nil {
					return err
				}
			}
			// Layer .env/.env.local under the ambient environment and export it
			// (skipped by --no-env) before anything resolves a credential: the
			// registry push reads its key from the process environment
			// (NUGET_API_KEY, `env:NAME` auth refs, the OIDC probe) and the
			// adapters spawn their package manager with an inherited one. Without
			// this, a key that lives only in .env is invisible here — while the
			// same publish run from `shiprig release` sees it — and the push goes
			// out with no credential.
			if _, err := applyReleaseEnv(ws.Root, noEnv); err != nil {
				return err
			}
			pkgs, ecoOf, err := ws.Discover(cmd.Context())
			if err != nil {
				return err
			}
			// Packages a build generated rather than a person checked in — npm
			// binary wrappers built from release artifacts, say. Discovery walks
			// the tree and cannot see them; `publishDirs` names where they land.
			//
			// They stay OUT of `pkgs`, which is the workspace's own list and the
			// one the tagging phase below iterates. A git tag marks a commit as a
			// released version of something in this repository; a wrapper built
			// from an archive is not that, and tagging them would push 41 refs
			// like `@rigsmith/rig@1.19.0` on every release. Only the publish loop
			// sees them, via toPublish.
			known := make(map[string]bool, len(pkgs))
			for _, p := range pkgs {
				known[p.Name] = true
			}
			gen, err := generatedPackages(ws.Root, ws.Config, known)
			if err != nil {
				return err
			}
			for name, eco := range gen.Eco {
				ecoOf[name] = eco
			}
			// Said out loud rather than inferred from a short list: a publish
			// that shipped none of the generated packages otherwise looks
			// exactly like one that had none to ship.
			for _, note := range gen.Notes {
				fmt.Fprintln(cmd.OutOrStdout(), commands.DimStyle.Render(note))
			}
			toPublish := make([]plugin.Package, 0, len(pkgs)+len(gen.Packages))
			toPublish = append(toPublish, pkgs...)
			toPublish = append(toPublish, gen.Packages...)
			// The npm dist-tag, as `changeset publish` picks it. Settled
			// before any registry is touched.
			pre, err := prestate.Read(ws.ChangesetDir)
			if err != nil {
				return err
			}
			// Only npm has dist-tags, so the tag is only checked when an npm
			// package is going out: a Go- or .NET-only prerelease can be
			// tagged anything.
			npm := false
			for _, p := range toPublish {
				if ecoOf[p.Name] == "node" && !p.Private && !ws.Config.IsIgnored(p.Name) {
					npm = true
				}
			}
			tag, err := publishDistTag(distTag, packDir != "", pre, npm)
			if err != nil {
				return err
			}
			// From a pack directory, exactly the files pack built go out, in
			// the plan's order, and nothing is built.
			packed := map[string]packedRelease{}
			if packDir != "" {
				releases, err := readPackDir(packDir, toPublish, ecoOf)
				if err != nil {
					return err
				}
				toPublish = toPublish[:0]
				for _, r := range releases {
					toPublish = append(toPublish, r.pkg)
					packed[r.pkg.Name] = r
				}
			}
			out := cmd.OutOrStdout()
			acc := access
			if acc == "" {
				acc = ws.Config.Access
			}

			// Confirm before the first real network side effect (registry
			// pushes, tag pushes) when a human is at the terminal. --yes and
			// non-TTY runs (CI) skip the gate; --dry-run never needs it.
			if !dryRun && !yes &&
				term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
				n := 0
				for _, p := range toPublish {
					if !ws.Config.IsIgnored(p.Name) {
						n++
					}
				}
				if !(ttyPrompter{}).Confirm(fmt.Sprintf("Publish %d package(s) to their registries (and push tags)?", n)) {
					fmt.Fprintln(out, commands.DimStyle.Render("Publish cancelled."))
					return nil
				}
			}

			// Registry credentials are resolved just-in-time and redacted from any
			// surfaced output. Cache per ecosystem so a secret-manager command
			// (e.g. `op read`) runs at most once per run.
			redactor := auth.NewRedactor()
			authCache := map[string]*plugin.AuthCredential{}

			// 1. Registry publish per package (ignored packages are never published).
			//
			// A real publish stays strictly sequential: each one is a side effect
			// on a registry, they are reported as they happen, and the first
			// failure stops the rest rather than racing more uploads out.
			//
			// A dry run is the opposite. Every adapter's dry-run path is
			// read-only by contract — no credential is even resolved — and its
			// cost is almost entirely waiting: the npm adapter asks the registry
			// whether each version already exists, which is what lets a dry run
			// say "already published" rather than "would publish". Serially that
			// is one round trip per package, and a repo whose release generates
			// 41 wrapper packages waited 29 seconds at 27% CPU to be told what it
			// would do. Probing concurrently keeps the answer and drops the wait.
			results := make([]publishResult, len(toPublish))
			var firstErr error

			// publishOne runs one package through its ecosystem. Credentials are
			// resolved only for real publishes, which is also what keeps the
			// concurrent path below safe: the auth cache is never touched there.
			publishOne := func(i int, p plugin.Package) {
				results[i] = publishResult{pkg: p}
				if ws.Config.IsIgnored(p.Name) {
					results[i].skipped = true
					return
				}
				eco, ok := ws.EcosystemFor(ecoOf[p.Name])
				if !ok {
					results[i].skipped = true
					return
				}
				ecoID := ecoOf[p.Name]
				var cred *plugin.AuthCredential
				var oidc bool
				if !dryRun {
					var credErr error
					cred, oidc, credErr = resolvePublishCreds(cmd.Context(), ws.Config, ecoID, npmAuth, authCache, redactor)
					if credErr != nil {
						results[i].err = fmt.Errorf("auth for %s: %s", p.Name, redactor.Redact(credErr.Error()))
						return
					}
				}
				// The workspace `access` describes the packages in the tree. A
				// generated wrapper carries its own: these are scoped npm
				// packages that must go out public, from a repo whose config
				// says "restricted".
				pkgAccess := acc
				if a, ok := gen.Access[p.Name]; ok && a != "" {
					pkgAccess = a
				}
				// A packed release carries the access and dist-tag its plan was
				// made with.
				pr, fromPack := packed[p.Name]
				if !fromPack {
					pr.tag = tag
				}
				if fromPack && pr.access != "" {
					pkgAccess = pr.access
				}
				resp, pubErr := eco.Publish(cmd.Context(), plugin.PublishRequest{
					RepoRoot:      ws.Root,
					Package:       p,
					PackageSource: packageSourceFor(ws.Config, ecoID),
					Access:        pkgAccess,
					DryRun:        dryRun,
					Auth:          cred,
					OIDC:          oidc,
					OIDCUser:      ws.Config.EcoConfig(ecoID).User,
					ArtifactPath:  pr.file,
					Tag:           pr.tag,
				})
				if pubErr != nil {
					results[i].err = fmt.Errorf("publish %s: %s", p.Name, redactor.Redact(pubErr.Error()))
					return
				}
				results[i].resp = resp
			}

			if dryRun {
				// Bounded, so a large workspace cannot open a connection per
				// package at a registry that would rather it did not.
				sem := make(chan struct{}, dryRunProbeLimit)
				var wg sync.WaitGroup
				for i, p := range toPublish {
					wg.Add(1)
					go func(i int, p plugin.Package) {
						defer wg.Done()
						sem <- struct{}{}
						defer func() { <-sem }()
						publishOne(i, p)
					}(i, p)
				}
				wg.Wait()
			} else {
				for i, p := range toPublish {
					publishOne(i, p)
					// A failed publish stops the run where it happened, rather
					// than pushing the rest out behind it.
					if results[i].err != nil {
						firstErr = results[i].err
						break
					}
					report(out, results[i])
				}
			}

			// A real publish reports each package as it happens. It is a series
			// of uploads over a slow link, and the operator watching it needs to
			// see the one that is taking a while, or where it stopped — buffering
			// until the end would hold every line behind the next package, and
			// lose them entirely if that one hangs.
			//
			// A dry run has nothing to watch: the probes finish out of order and
			// take seconds in total, so its lines are printed after the wait, in
			// workspace order. Both read the same in the end, which is the point
			// — a preview whose order differs from the publish is worse than a
			// slow one.
			if dryRun {
				for _, r := range results {
					if r.err != nil {
						return r.err
					}
					report(out, r)
				}
			} else if firstErr != nil {
				return firstErr
			}

			// 2. Tagging phase (this is what actually publishes Go modules).
			if noGitTag {
				return nil
			}
			// A stackspace's fused history is neither tagged nor pushed: the
			// registry push above is the whole publish there.
			if ws.Stackspace != nil {
				fmt.Fprintln(out, commands.DimStyle.Render("\nstackspace: a fused history is not tagged — registry push only"))
				return nil
			}
			// With tag events on, the caller owns the push (see tagEvents):
			// tags are created locally only, and the remote is consulted just
			// to skip a tag that is already there, as `changeset publish` does.
			remote := ""
			if !noPush && events == nil {
				remote = gitutil.DefaultRemote(cmd.Context(), ws.Root)
			}
			eventRemote := ""
			if events != nil {
				eventRemote = gitutil.DefaultRemote(cmd.Context(), ws.Root)
			}
			fmt.Fprintln(out)
			soloApp := singleApp(pkgs)
			// A git tag is one ref, but several packages can render the same one —
			// a `tagTemplate` like "v${version}" collapses every package in the repo
			// onto a single tag. Iterate distinct tags, not packages, or a 12-package
			// repo reports the one tag 12 times ("would tag v0.2.0" twelve over, or
			// one "tagged+pushed" then eleven "tag exists" as each iteration
			// re-reads the tag the previous one just created).
			done := map[string]bool{}
			for _, p := range pkgs {
				if ws.Config.SkipsTag(p.Name) {
					continue
				}
				tag := gitutil.RenderTag(ws.Config.TagTemplate, ecoOf[p.Name], p.Dir, p.Name, p.Version, soloApp)
				if done[tag] {
					continue
				}
				done[tag] = true
				localExists := gitutil.TagExists(cmd.Context(), ws.Root, tag)
				if events != nil {
					if localExists || (eventRemote != "" && gitutil.RemoteTagExists(cmd.Context(), ws.Root, eventRemote, tag)) {
						fmt.Fprintf(out, "%s %s\n", commands.DimStyle.Render("tag exists"), tag)
						continue
					}
					if dryRun {
						fmt.Fprintf(out, "%s %s\n", commands.DimStyle.Render("would tag"), tag)
						continue
					}
					created, err := events.create(cmd.Context(), ws.Root, tag, p.Name)
					if err != nil {
						return fmt.Errorf("tagging %s: %w", p.Name, err)
					}
					if !created {
						fmt.Fprintf(out, "%s %s\n", commands.DimStyle.Render("tag exists"), tag)
						continue
					}
					fmt.Fprintf(out, "%s %s %s\n", commands.PatchStyle.Render("tagged"), tag, commands.DimStyle.Render("(local; the caller pushes it)"))
					continue
				}
				// Without a remote, a local tag is the terminal state. With one, the
				// tag is only "done" once it's actually on the remote — a previous run
				// could have created the tag locally and then failed to push it.
				onRemote := remote == "" || gitutil.RemoteTagExists(cmd.Context(), ws.Root, remote, tag)
				if localExists && onRemote {
					fmt.Fprintf(out, "%s %s\n", commands.DimStyle.Render("tag exists"), tag)
					continue
				}
				if dryRun {
					push := ""
					if remote != "" {
						push = commands.DimStyle.Render(" → push " + remote)
					}
					action := "would tag"
					if localExists {
						action = "would push" // recover a tag created but never pushed
					}
					fmt.Fprintf(out, "%s %s%s\n", commands.DimStyle.Render(action), tag, push)
					continue
				}
				if !localExists {
					if err := gitutil.CreateTag(cmd.Context(), ws.Root, tag, tag); err != nil {
						return fmt.Errorf("tagging %s: %w", p.Name, err)
					}
				}
				if remote != "" {
					if err := gitutil.PushTag(cmd.Context(), ws.Root, remote, tag); err != nil {
						return fmt.Errorf("pushing tag %s: %w", tag, err)
					}
					label := "tagged+pushed"
					if localExists {
						label = "pushed" // tag already existed locally from a prior run
					}
					fmt.Fprintf(out, "%s %s %s\n", commands.PatchStyle.Render(label), tag, commands.DimStyle.Render("→ "+remote))
				} else {
					fmt.Fprintf(out, "%s %s\n", commands.PatchStyle.Render("tagged"), tag)
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&yes, "yes", "y", false, "skip the confirm prompt (CI / scripted runs)")
	f.BoolVarP(&dryRun, "dry-run", "n", false, "show what would be published/tagged without doing it")
	f.BoolVar(&noGitTag, "no-git-tag", false, "skip creating git tags")
	f.BoolVar(&noPush, "no-push", false, "create tags locally but do not push them")
	f.StringVarP(&outputPath, "output", "o", "", "append a git-tag event per tag created to this file (default $CHANGESETS_OUTPUT); tags are then created locally only, for the caller to push")
	f.StringVar(&access, "access", "", "npm access (public|restricted); defaults to config")
	f.StringVar(&npmAuth, "npm-auth", "", "npm auth secret ref (op://… | env:NAME | cmd:…); overrides node config")
	f.StringVar(&distTag, "tag", "", "the npm dist-tag to publish under (default: the prerelease tag in pre mode; not allowed in pre mode or with --from-pack-dir)")
	f.StringVar(&packDir, "from-pack-dir", "", "publish the files `shiprig pack` built into this directory, building nothing (as `changeset publish --from-pack-dir`)")
	return cmd
}

// resolvePublishCreds decides how an ecosystem's publish authenticates:
//
//   - An explicit secret ref (the `auth` config block, or --npm-auth for node)
//     wins — resolve it to a credential, cached so a secret-manager command runs
//     at most once per run.
//   - Otherwise, for an ecosystem that supports OIDC trusted publishing, when it
//     is not turned off and a CI OIDC context is present, signal OIDC — the
//     adapter mints and exchanges the token itself.
//   - Otherwise return nothing: the adapter uses its ambient credential
//     (~/.npmrc / NPM_TOKEN), i.e. pre-auth-seam behaviour.
func resolvePublishCreds(ctx context.Context, cfg *config.Config, eco, npmAuthOverride string, cache map[string]*plugin.AuthCredential, redactor auth.Masker) (*plugin.AuthCredential, bool, error) {
	ref := cfg.EcoConfig(eco).Auth
	if eco == "node" && npmAuthOverride != "" {
		ref = npmAuthOverride
	}
	if ref != "" {
		if cached, ok := cache[ref]; ok {
			return cached, false, nil
		}
		cred, err := auth.Resolve(ctx, auth.Request{Ref: ref, Masker: redactor})
		if err != nil {
			return nil, false, err
		}
		var ac *plugin.AuthCredential
		if cred.Resolved() {
			ac = &plugin.AuthCredential{
				Token:      cred.Token,
				Method:     string(cred.Method),
				Provenance: cred.Provenance,
			}
		}
		cache[ref] = ac
		return ac, false, nil
	}

	if ecoSupportsOIDC(eco) &&
		!strings.EqualFold(cfg.EcoConfig(eco).OIDC, "off") &&
		auth.HasOIDCContext() {
		return nil, true, nil
	}
	return nil, false, nil
}

// ecoSupportsOIDC reports whether an ecosystem can publish via OIDC trusted
// publishing. npm, crates.io, and NuGet today.
func ecoSupportsOIDC(eco string) bool {
	return eco == "node" || eco == "cargo" || eco == "dotnet"
}

// packageSourceFor resolves the publish feed for a package's ecosystem: the
// per-ecosystem `packageSource` config block wins, falling back to the built-in
// default. The adapters fall back to their own defaults on "".
func packageSourceFor(cfg *config.Config, eco string) string {
	if src := cfg.EcoConfig(eco).PackageSource; src != "" {
		return src
	}
	return ecosystemSource(eco)
}

// ecosystemSource returns the default package source per ecosystem when config
// doesn't specify one. The adapters fall back to their own defaults on "".
func ecosystemSource(eco string) string {
	switch eco {
	case "dotnet":
		return "nuget"
	default:
		return ""
	}
}

// publishDistTag picks the npm dist-tag a publish goes out under, as
// `changeset publish` does. --tag names it, except in pre mode, where the
// prerelease tag is the only one allowed, and from a pack directory, whose
// plan carries each release's own. In pre mode it's the prerelease tag. Empty
// otherwise, which leaves npm's default (latest). Without this, a prerelease
// version went out as latest.
//
// npm says whether an npm package is being published: only npm has
// dist-tags, so without one the tag isn't checked against npm's rules.
func publishDistTag(flag string, fromPackDir bool, pre *prestate.PreState, npm bool) (string, error) {
	inPre := pre != nil && pre.Mode == prestate.ModePre
	switch {
	case flag != "" && fromPackDir:
		return "", errors.New("--tag can't be used with --from-pack-dir: the pack plan carries each release's dist-tag")
	case flag != "" && inPre:
		return "", errors.New("--tag can't be used in pre mode: prereleases go out under the prerelease tag (run `pre exit` to publish under another)")
	case flag != "":
		if err := checkDistTag(flag); npm && err != nil {
			return "", fmt.Errorf("--tag %q: %w", flag, err)
		}
		return flag, nil
	case fromPackDir:
		return "", nil
	case inPre:
		if strings.TrimSpace(pre.Tag) == "" {
			return "", errors.New(".changeset/pre.json is in pre mode with no tag: set its tag")
		}
		if err := checkDistTag(pre.Tag); npm && err != nil {
			return "", fmt.Errorf(".changeset/pre.json's tag %q: %w", pre.Tag, err)
		}
		return pre.Tag, nil
	}
	return "", nil
}

// distTagChars is what a dist-tag is made of, as npm has it: characters
// encodeURIComponent leaves alone (letters, digits and - _ . ! ~ * ' ( )),
// so no whitespace and nothing a URL would need to escape.
var distTagChars = regexp.MustCompile(`^[A-Za-z0-9\-_.!~*'()]+$`)

// checkDistTag refuses a tag npm would refuse, before anything is published:
// npm checks it per package, so a bad one would fail partway through a
// release. Beyond the characters, npm refuses a tag that reads as a version
// or range ("1", "v2", "1.x"), since `npm install pkg@<tag>` couldn't tell
// the two apart.
func checkDistTag(tag string) error {
	if !distTagChars.MatchString(tag) {
		return errors.New("a dist-tag is letters, digits and - _ . ! ~ * ' ( ) (what a URL doesn't need to escape)")
	}
	if looksLikeVersion.MatchString(tag) {
		return errors.New("npm refuses a dist-tag that reads as a version or range")
	}
	return nil
}

// looksLikeVersion matches what npm's semver would read as a version or
// range, among tags distTagChars allows: an optional ~ (the only range
// operator a URL leaves alone), an optional v, then a number or wildcard and
// more of either ("1", "v2", "1.x", "~1.2", "*").
var looksLikeVersion = regexp.MustCompile(`^~?[vV]?(\d+|[xX*])(\.(\d+|[xX*]))*([-+].*)?$`)
