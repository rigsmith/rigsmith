package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

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
			pkgs, ecoOf, err := ws.Discover(cmd.Context())
			if err != nil {
				return err
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
				for _, p := range pkgs {
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
			for _, p := range pkgs {
				if ws.Config.IsIgnored(p.Name) {
					continue
				}
				eco, ok := ws.EcosystemFor(ecoOf[p.Name])
				if !ok {
					continue
				}
				ecoID := ecoOf[p.Name]
				// Resolve auth only for real publishes; --dry-run must stay free of
				// side effects (no secret fetch / prompt / token mint).
				var cred *plugin.AuthCredential
				var oidc bool
				if !dryRun {
					cred, oidc, err = resolvePublishCreds(cmd.Context(), ws.Config, ecoID, npmAuth, authCache, redactor)
					if err != nil {
						return fmt.Errorf("auth for %s: %s", p.Name, redactor.Redact(err.Error()))
					}
				}
				resp, err := eco.Publish(cmd.Context(), plugin.PublishRequest{
					RepoRoot:      ws.Root,
					Package:       p,
					PackageSource: packageSourceFor(ws.Config, ecoID),
					Access:        acc,
					DryRun:        dryRun,
					Auth:          cred,
					OIDC:          oidc,
					OIDCUser:      ws.Config.EcoConfig(ecoID).User,
				})
				if err != nil {
					return fmt.Errorf("publish %s: %s", p.Name, redactor.Redact(err.Error()))
				}
				switch {
				case resp.Published:
					fmt.Fprintf(out, "%s %s@%s  %s\n", commands.PatchStyle.Render("published"), p.Name, p.Version, commands.DimStyle.Render(resp.Message))
				case resp.Skipped:
					fmt.Fprintf(out, "%s %s@%s  %s\n", commands.DimStyle.Render("skipped  "), p.Name, p.Version, commands.DimStyle.Render(resp.Message))
				default:
					fmt.Fprintf(out, "%s %s@%s  %s\n", commands.DimStyle.Render("·        "), p.Name, p.Version, commands.DimStyle.Render(resp.Message))
				}
			}

			// 2. Tagging phase (this is what actually publishes Go modules).
			if noGitTag {
				return nil
			}
			remote := ""
			if !noPush {
				remote = gitutil.DefaultRemote(cmd.Context(), ws.Root)
			}
			fmt.Fprintln(out)
			soloApp := singleApp(pkgs)
			for _, p := range pkgs {
				if ws.Config.IsIgnored(p.Name) {
					continue
				}
				tag := gitutil.RenderTag(ws.Config.TagTemplate, ecoOf[p.Name], p.Dir, p.Name, p.Version, soloApp)
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
