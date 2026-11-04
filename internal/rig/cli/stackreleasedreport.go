package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/ecosystem/dotnet"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/spf13/cobra"
)

type stackReleasedOptions struct {
	Package, Version, Feed, Via, ViaVersion string
	Warnings                                []string
}
type stackReleasedFile struct {
	Path, Content, Mode string
}
type stackReleasedReport struct {
	Schema                                                    int
	Member, Sibling, SeedBase                                 string
	Options                                                   stackReleasedOptions
	Bases, Branches                                           map[string]string
	Status, Version, ReleaseCommit, Pin, PinVersion, Delivery string
	Edits                                                     []stackReleasedFile
	Notes, Removed                                            []string
}

var stackReleasedReportRename = os.Rename

// Report and publisher share only data-reading code: no adapter registry,
// checkout filters, MSBuild, hooks, or commands from the manifest are executed.
func stackReleasedLinks(ctx context.Context, root string, m *stackManifest) ([]stackLink, error) {
	eco := dotnet.New()
	resp, err := eco.Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: ".", IncludeUnversioned: true, IncludeRegistrySiblings: true})
	if err != nil {
		return nil, err
	}
	byEco, _, _, failed := stackRedirectsFromScan([]stackScanResult{{Eco: eco, Packages: resp.Packages}}, m.names(), m.publishing())
	if len(failed) > 0 {
		return nil, fmt.Errorf("can't check NuGet ownership")
	}
	return byEco["dotnet"], nil
}

func stackReleasedCompute(ctx context.Context, api stackDepsAPI, root string, m *stackManifest, r stackReleasedReport) (stackReleasedReport, error) {
	if r.Member == r.Sibling || m.Repos[r.Member] == nil || m.Repos[r.Sibling] == nil || r.Options.Package == "" {
		return r, fmt.Errorf("name distinct manifest members and --package")
	}
	r.Schema = 1
	o := r.Options
	v, err := stackReleaseVersionProof(ctx, api, m, r.Sibling, o.Package, o.Version, o.Feed, &r.ReleaseCommit)
	if err != nil {
		if strings.HasPrefix(err.Error(), "not yet released") {
			r.Status = "not yet"
			r.Notes = []string{err.Error()}
			return r, nil
		}
		return r, err
	}
	r.Status = "released"
	r.Version = v
	links, err := stackReleasedLinks(ctx, root, m)
	if err != nil {
		return r, err
	}
	r.Pin, r.PinVersion, r.Delivery, err = stackReleaseDelivery(ctx, api, links, r.Member, r.Sibling, o.Package, v, o.Via, o.ViaVersion, o.Feed)
	if err != nil {
		if strings.HasPrefix(err.Error(), "guards unchanged: no checked") {
			r.Status = "not yet"
			r.Notes = []string{err.Error()}
			return r, nil
		}
		return r, err
	}
	dir := filepath.Join(root, r.Member)
	already, err := stackReleasedAlready(dir, r.Pin, r.PinVersion)
	if err != nil {
		return r, err
	}
	if already {
		r.Status = "already migrated"
		return r, nil
	}
	symbol := "STACK_" + strings.ToUpper(dotnet.StackSourceName(o.Package))
	edits, notes, err := stackReleaseMigrationEdits(dir, o.Package, r.Pin, r.PinVersion, symbol, o.Warnings...)
	if err != nil {
		return r, err
	}
	for _, n := range notes {
		r.Notes = append(r.Notes, strings.ReplaceAll(n, dir+string(filepath.Separator), ""))
	}
	for _, e := range edits {
		rel, err := filepath.Rel(dir, e.path)
		if err != nil {
			return r, err
		}
		rel = filepath.ToSlash(rel)
		mode := "100644"
		if e.mode&0111 != 0 {
			mode = "100755"
		}
		r.Edits = append(r.Edits, stackReleasedFile{rel, string(e.after), mode})
		for i, line := range strings.Split(string(e.before), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "#if" && fields[1] == symbol && filepath.Ext(rel) == ".cs" {
				r.Removed = append(r.Removed, fmt.Sprintf("%s:%d: removed #if %s (kept guarded branch)", rel, i+1, symbol))
			}
		}
		for _, warning := range o.Warnings {
			// The XML rewriter removes only the exact attributed declaration.
			declaration := func(b []byte) int { return stackReleasedWarningCount(b, warning) }
			if count := declaration(e.before) - declaration(e.after); count > 0 {
				r.Removed = append(r.Removed, fmt.Sprintf("%s: removed %d StackOnlyWarning %s declaration(s)", rel, count, warning))
			}
		}
	}
	return r, nil
}

func stackReleasedReportCommand(cmd *cobra.Command, args []string, o stackReleasedOptions, seed, report, publish string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
	defer cancel()
	r := stackReleasedReport{Options: o}
	if publish != "" {
		f, err := os.Open(publish)
		if err != nil {
			return err
		}
		defer f.Close()
		decoder := json.NewDecoder(io.LimitReader(f, 8<<20))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&r); err != nil {
			return err
		}
		var trailing any
		if err = decoder.Decode(&trailing); err != io.EOF {
			return fmt.Errorf("invalid trailing report data")
		}
		if r.Schema != 1 || !reflect.DeepEqual(r.Options, o) {
			return fmt.Errorf("report selections differ from trusted command flags")
		}
		if len(args) > 0 && (args[0] != r.Member || args[1] != r.Sibling) {
			return fmt.Errorf("report members differ")
		}
		if seed == "" {
			return fmt.Errorf("--publish requires --seed")
		}
	} else {
		r.Member = args[0]
		r.Sibling = args[1]
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root := cwd
	var m *stackManifest
	fresh := stackReleasedReport{Member: r.Member, Sibling: r.Sibling, Options: o}
	if seed != "" {
		tmp, err := os.MkdirTemp("", "rig-released-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		home := filepath.Join(tmp, "home")
		data, base, err := stackCISeed(ctx, cwd, home, seed, "github.com")
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(home, "rig.stack.jsonc"), data, 0600); err != nil {
			return err
		}
		m, _, err = loadStackManifest(home)
		if err != nil {
			return err
		}
		if m == nil {
			return fmt.Errorf("missing manifest")
		}
		if err = stackCheckHome(ctx, home, m); err != nil {
			return err
		}
		if err = stackCIValidate(m, "github.com"); err != nil {
			return err
		}
		fresh.SeedBase = base
		fresh.Bases = map[string]string{}
		fresh.Branches = map[string]string{}
		root = filepath.Join(tmp, "members")
		if err = os.MkdirAll(root, 0700); err != nil {
			return err
		}
		for _, n := range m.names() {
			repo, branch, sha, err := stackReleasedSnapshot(ctx, stackDepsClient(), cwd, tmp, root, n, m.Repos[n].Upstream)
			_ = repo
			fresh.Branches[n] = branch
			if err != nil {
				return err
			}
			fresh.Bases[n] = sha
		}
	} else {
		root, err = stackRoot(ctx)
		if err != nil {
			return err
		}
		m, _, err = loadStackManifest(root)
		if err != nil {
			return err
		}
		if m == nil {
			return fmt.Errorf("no stack manifest")
		}
	}
	fresh, err = stackReleasedCompute(ctx, stackDepsClient(), root, m, fresh)
	if err != nil {
		return err
	}
	if publish != "" {
		// A current hand migration needs no proposal, even if the report predates it.
		// No artifact content is used on this no-write path.
		if fresh.Status == "already migrated" {
			return stackCanarySummary(cmd.OutOrStdout(), "Member already has the delivery version; no PR opened.")
		}
		// Always re-verify evidence before any write, including a not-yet artifact.
		if err = stackReleasedValidate(r, fresh); err != nil {
			return err
		}
		if fresh.Status == "not yet" {
			return stackCanarySummary(cmd.OutOrStdout(), "Release not yet carrying the recorded work; no PR.")
		}
		return stackReleasedPublishCurrent(ctx, cmd, stackDepsClient(), root, m, fresh)
	}
	if dryRun {
		return stackCanarySummary(cmd.ErrOrStderr(), "Would write release report; no tree edits.")
	}
	data, err := json.MarshalIndent(fresh, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(report), ".rig-released-report-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = stackReleasedReportRename(f.Name(), report); err != nil {
		return err
	}
	return stackCanarySummary(cmd.ErrOrStderr(), "Release report: "+fresh.Status+"; no tree edits.")
}

func stackReleasedValidate(reported, verified stackReleasedReport) error {
	if !reflect.DeepEqual(reported, verified) {
		return fmt.Errorf("release report differs from independently verified upstream state; rerun report")
	}
	return nil
}

func stackReleasedSnapshot(ctx context.Context, a stackDepsAPI, cwd, tmp, root, name, upstream string) (string, string, string, error) {
	parts := strings.Split(upstream, "/")
	if len(parts) != 3 || parts[0] != "github.com" {
		return "", "", "", fmt.Errorf("publication requires GitHub upstreams")
	}
	repo := parts[1] + "/" + parts[2]
	var info struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := a.request(ctx, "GET", "/repos/"+repo, nil, &info); err != nil {
		return "", "", "", err
	}
	if info.DefaultBranch == "" || strings.HasPrefix(info.DefaultBranch, "-") {
		return "", "", "", fmt.Errorf("missing default branch")
	}
	bare := filepath.Join(tmp, "bare-"+name)
	if _, err := stackCIGit(ctx, cwd, "clone", "--bare", "--depth=1", "--single-branch", "--branch", info.DefaultBranch, "--", stackRemoteURL(upstream), bare); err != nil {
		return "", "", "", fmt.Errorf("cannot read member default branch %s", name)
	}
	sha, err := stackCIGit(ctx, bare, "rev-parse", "HEAD")
	if err != nil {
		return "", "", "", err
	}
	dest := filepath.Join(root, name)
	if err = os.MkdirAll(dest, 0700); err != nil {
		return "", "", "", err
	}
	// Reject symlinks entirely in this privileged data-only reader.
	entries, err := stackCIGit(ctx, bare, "ls-tree", "-r", "HEAD")
	if err != nil {
		return "", "", "", err
	}
	for _, line := range strings.Split(string(entries), "\n") {
		if strings.HasPrefix(line, "120000 ") {
			return "", "", "", fmt.Errorf("release publisher refuses member symlinks")
		}
	}
	if err = stackCIMaterialize(ctx, bare, dest); err != nil {
		return "", "", "", err
	}
	return repo, info.DefaultBranch, strings.TrimSpace(string(sha)), nil
}

func stackReleasedWarningCount(data []byte, warning string) int {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	count := 0
	for {
		tok, err := decoder.Token()
		if err != nil {
			return count
		}
		el, ok := tok.(xml.StartElement)
		if !ok || el.Name.Local != "StackOnlyWarning" {
			continue
		}
		for _, attr := range el.Attr {
			if attr.Name.Local == "Include" && attr.Value == warning {
				count++
			}
		}
	}
}
