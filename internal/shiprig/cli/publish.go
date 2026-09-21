package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/rigsmith/rigsmith/core/auth"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/plugin"
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
		dryRun   bool
		noGitTag bool
		noPush   bool
		access   string
		yes      bool
		npmAuth  string
	)
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish packages to their registries and tag the release",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := commands.Open()
			if err != nil {
				return err
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
			toPublish := make([]plugin.Package, 0, len(pkgs)+len(gen.Packages))
			toPublish = append(toPublish, pkgs...)
			toPublish = append(toPublish, gen.Packages...)
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
				resp, pubErr := eco.Publish(cmd.Context(), plugin.PublishRequest{
					RepoRoot:      ws.Root,
					Package:       p,
					PackageSource: packageSourceFor(ws.Config, ecoID),
					Access:        pkgAccess,
					DryRun:        dryRun,
					Auth:          cred,
					OIDC:          oidc,
					OIDCUser:      ws.Config.EcoConfig(ecoID).User,
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
			remote := ""
			if !noPush {
				remote = gitutil.DefaultRemote(cmd.Context(), ws.Root)
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
				if ws.Config.IsIgnored(p.Name) {
					continue
				}
				tag := gitutil.RenderTag(ws.Config.TagTemplate, ecoOf[p.Name], p.Dir, p.Name, p.Version, soloApp)
				if done[tag] {
					continue
				}
				done[tag] = true
				localExists := gitutil.TagExists(cmd.Context(), ws.Root, tag)
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
	f.StringVar(&access, "access", "", "npm access (public|restricted); defaults to config")
	f.StringVar(&npmAuth, "npm-auth", "", "npm auth secret ref (op://… | env:NAME | cmd:…); overrides node config")
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
