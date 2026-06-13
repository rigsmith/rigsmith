// `rig self-update [--check]` — update rig itself, ported from the .NET rig's
// UpdateVerb: check the publish channel for the latest stable version, compare
// it to the running build, and (unless --check) re-run the distribution's own
// updater. The .NET tool ships on NuGet so its updater is `dotnet tool update
// --global rig`; rigsmith ships GoReleaser tarballs on GitHub Releases behind
// scripts/install.sh, so the Go updater re-runs the curl|sh installer pinned
// to the discovered tag. Version comparison (latestStable/isNewer) lives in
// dotnetverbs.go with the other UpdateVerb ports.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// version is the build version, stamped at release time via
//
//	-ldflags "-X github.com/rigsmith/cli/internal/cli.version={{.Version}}"
//
// (see .goreleaser.yaml). Source builds stay "dev", which self-update treats
// as "not an installed release" and degrades gracefully.
var version = "dev"

// defaultUpdateRepo is the GitHub repo self-update checks and installs from,
// matching scripts/install.sh. Overridable at build time via
// -X …cli.defaultUpdateRepo=…, or at run time with $RIG_SELFUPDATE_REPO.
var defaultUpdateRepo = "rigsmith/rigsmith"

func newSelfUpdateCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update rig itself to the latest release",
		Long: "Check GitHub Releases for a newer rig and install it by re-running the\n" +
			"rigsmith installer (scripts/install.sh) pinned to that release.\n\n" +
			"With --check it only reports whether an update is available.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSelfUpdate(cmd, check)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only check whether a newer release exists")
	return cmd
}

func runSelfUpdate(cmd *cobra.Command, check bool) error {
	out := cmd.OutOrStdout()
	repo := resolveUpdateRepo(os.Getenv("RIG_SELFUPDATE_REPO"))

	// A source ("dev") build isn't managed by the installer — nothing to update.
	current := strings.TrimPrefix(version, "v")
	if current == "" || current == "dev" {
		fmt.Fprintln(out, "this rig is a dev build (not an installed release); nothing to self-update.")
		fmt.Fprintln(out, dimStyle.Render("install a release with: curl -fsSL https://raw.githubusercontent.com/"+repo+"/main/scripts/install.sh | sh -s rig"))
		return nil
	}

	tag, err := fetchLatestReleaseTag(latestReleaseURL(repo))
	if err != nil {
		// Offline / rate-limited is a degraded check, not a failure of rig.
		fmt.Fprintln(out, dimStyle.Render("couldn't reach GitHub to check for updates: "+err.Error()))
		return nil
	}
	if tag == "" {
		fmt.Fprintln(out, dimStyle.Render("no published releases found for "+repo+"."))
		return nil
	}

	latest := strings.TrimPrefix(tag, "v")
	if !isNewer(current, latest) {
		fmt.Fprintf(out, "rig is up to date (v%s; latest is v%s).\n", current, latest)
		return nil
	}

	fmt.Fprintf(out, "a newer rig is available: v%s → v%s.\n", current, latest)
	if check {
		fmt.Fprintln(out, dimStyle.Render("run `rig self-update` to install it."))
		return nil
	}

	if runtime.GOOS == "windows" {
		// The installer is POSIX sh; on Windows the running rig.exe is locked
		// anyway. Point at the release instead of half-failing.
		fmt.Fprintln(out, "self-update can't replace a running rig.exe on Windows.")
		fmt.Fprintln(out, dimStyle.Render("download "+tag+" from https://github.com/"+repo+"/releases/latest"))
		return nil
	}

	// Mirror the .NET rig: hand the actual install to the distribution's own
	// updater (there: `dotnet tool update`; here: the curl|sh installer, pinned
	// to the tag we just resolved). Replacing a running binary is fine on Unix.
	argv := selfUpdateInstallerArgs(repo, tag)
	cwd, _ := os.Getwd()
	return runCommand(cmd, cwd, argv)
}

// resolveUpdateRepo picks the GitHub repo slug: the $RIG_SELFUPDATE_REPO
// override when set, else the build-time default. Pure.
func resolveUpdateRepo(env string) string {
	if s := strings.TrimSpace(env); s != "" {
		return s
	}
	return defaultUpdateRepo
}

// latestReleaseURL is the GitHub API endpoint for a repo's latest release. Pure.
func latestReleaseURL(repo string) string {
	return "https://api.github.com/repos/" + repo + "/releases/latest"
}

// parseReleaseTag extracts tag_name from a GitHub release JSON payload.
// "" when the payload is malformed or carries no tag. Pure.
func parseReleaseTag(data []byte) string {
	var doc struct {
		TagName string `json:"tag_name"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	return doc.TagName
}

// releaseAssetName is the GoReleaser archive name for a binary at a tag,
// matching .goreleaser.yaml's name_template and scripts/install.sh:
// <bin>_<version-without-v>_<os>_<arch>.tar.gz (zip on windows). Pure.
func releaseAssetName(bin, tag, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return bin + "_" + strings.TrimPrefix(tag, "v") + "_" + goos + "_" + goarch + ext
}

// selfUpdateInstallerArgs is the argv that re-runs the rigsmith installer for
// just the rig binary, pinned to the given release tag. Pure.
func selfUpdateInstallerArgs(repo, tag string) []string {
	script := "curl -fsSL https://raw.githubusercontent.com/" + repo + "/main/scripts/install.sh" +
		" | RIGSMITH_VERSION=" + tag + " sh -s rig"
	return []string{"sh", "-c", script}
}

// fetchLatestReleaseTag GETs the release endpoint and returns its tag_name.
func fetchLatestReleaseTag(url string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub responded %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return parseReleaseTag(body), nil
}
