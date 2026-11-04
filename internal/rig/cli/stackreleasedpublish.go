package cli

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Pin proof runs first; this only asks whether every literal pin already meets
// the delivery version. A human migration must not reopen a guard-only PR.
func stackReleasedAlready(root, pkg, version string) (bool, error) {
	if err := stackReleaseProvePins(root, pkg); err != nil {
		return false, err
	}
	target, ok := stackParseNugetVersion(version)
	if !ok {
		return false, fmt.Errorf("invalid delivery version")
	}
	found, older := false, false
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch strings.ToLower(d.Name()) {
			case ".git", "bin", "obj", "vendor", "node_modules", "packages", "generated", ".vs":
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".csproj" && ext != ".props" && ext != ".targets" {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		decoder := xml.NewDecoder(f)
		for {
			tok, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			el, ok := tok.(xml.StartElement)
			if !ok || (!strings.EqualFold(el.Name.Local, "PackageReference") && !strings.EqualFold(el.Name.Local, "PackageVersion")) {
				continue
			}
			id, v := "", ""
			for _, a := range el.Attr {
				if strings.EqualFold(a.Name.Local, "Include") {
					id = a.Value
				}
				if strings.EqualFold(a.Name.Local, "Version") {
					v = a.Value
				}
			}
			if !strings.EqualFold(id, pkg) || v == "" {
				continue
			}
			current, ok := stackParseNugetVersion(v)
			if !ok {
				return fmt.Errorf("unresolved pin")
			}
			found = true
			older = older || stackCompareNuget(current, target) < 0
		}
		return nil
	})
	return found && !older, err
}

func stackReleasedPublishCurrent(ctx context.Context, cmd *cobra.Command, a stackDepsAPI, root string, m *stackManifest, r stackReleasedReport) error {
	if r.Status == "already migrated" {
		return stackCanarySummary(cmd.OutOrStdout(), "Member already has the delivery version; no PR opened.")
	}
	if dryRun {
		return stackCanarySummary(cmd.OutOrStdout(), "Would reconcile verified back-to-packages PR.")
	}
	if a.token == "" {
		return stackCanarySummary(cmd.OutOrStdout(), "Release report available; publication skipped (no GITHUB_TOKEN).")
	}
	upstream := m.Repos[r.Member].Upstream
	repo := strings.TrimPrefix(upstream, "github.com/")
	if err := stackReleasedPublish(ctx, a, repo, r.Branches[r.Member], r.Bases[r.Member], r); err != nil {
		var writeError *stackReleasedWriteError
		var responseError *stackDepsHTTPError
		if errors.As(err, &writeError) && errors.As(err, &responseError) && (responseError.status == http.StatusForbidden || responseError.status == http.StatusNotFound) {
			return stackCanarySummary(cmd.OutOrStdout(), fmt.Sprintf("Release publication denied (%v); check member contents/PR permissions and enable \"Allow GitHub Actions to create and approve pull requests\" or use a token with PR write access. A rig-released/ branch may remain; the next run reuses it.", err))
		}
		return fmt.Errorf("release publication failed: %w", err)
	}
	return stackCanarySummary(cmd.OutOrStdout(), "Verified back-to-packages PR reconciled.")
}

type stackReleasedWriteError struct{ error }

func (e *stackReleasedWriteError) Unwrap() error { return e.error }

func stackReleasedPublish(ctx context.Context, a stackDepsAPI, repo, baseBranch, base string, r stackReleasedReport) error {
	branch := "rig-released/" + r.Sibling
	refPath := url.PathEscape(branch)
	write := func(method, endpoint string, payload, result any) error {
		if err := a.request(ctx, method, endpoint, payload, result); err != nil {
			return &stackReleasedWriteError{err}
		}
		return nil
	}
	marker := "<!-- rig-released:" + r.Member + ":" + r.Sibling + " -->"
	title := "Stack: return to " + r.Pin + " " + r.PinVersion
	var body strings.Builder
	fmt.Fprintf(&body, "%s\nVerified NuGet %s %s, repository commit `%s`: all recorded proposals and the upstream cursor are included.\n\nDelivery: %s\n\n", marker, stackCanaryText(r.Options.Package), stackCanaryText(r.Version), r.ReleaseCommit, stackCanaryText(r.Delivery))
	for _, line := range r.Removed {
		fmt.Fprintf(&body, "- %s\n", stackCanaryText(line))
	}
	for _, line := range r.Notes {
		fmt.Fprintf(&body, "- Left for review: %s\n", stackCanaryText(line))
	}
	body.WriteString("\nA human must review this PR and run package-mode checks before merging.\n")
	path := "/repos/" + repo
	var prs []struct {
		Number      int
		Title, Body string
		Base        struct{ Ref string }
	}
	if err := a.request(ctx, "GET", path+"/pulls?state=open&head="+url.QueryEscape(strings.Split(repo, "/")[0]+":"+branch), nil, &prs); err != nil {
		return err
	}
	for _, pr := range prs {
		if pr.Base.Ref != baseBranch {
			return fmt.Errorf("existing release PR targets %q instead of %q; refusing to change its branch", pr.Base.Ref, baseBranch)
		}
	}
	var commit struct{ Tree struct{ SHA string } }
	if err := a.request(ctx, "GET", path+"/git/commits/"+base, nil, &commit); err != nil {
		return err
	}
	var refs []struct {
		Ref    string
		Object struct{ SHA string }
	}
	// A list distinguishes an absent branch from denied ref access.
	if err := a.request(ctx, "GET", path+"/git/matching-refs/heads/"+refPath, nil, &refs); err != nil {
		return err
	}
	exists := false
	var have struct {
		Tree    struct{ SHA string }
		Parents []struct{ SHA string }
	}
	for _, ref := range refs {
		if ref.Ref == "refs/heads/"+branch {
			exists = true
			if err := a.request(ctx, "GET", path+"/git/commits/"+url.PathEscape(ref.Object.SHA), nil, &have); err != nil {
				return err
			}
			break
		}
	}
	var entries []map[string]string
	for _, edit := range r.Edits {
		entries = append(entries, map[string]string{"path": edit.Path, "mode": edit.Mode, "type": "blob", "content": edit.Content})
	}
	var tree struct{ SHA string }
	if err := write("POST", path+"/git/trees", map[string]any{"base_tree": commit.Tree.SHA, "tree": entries}, &tree); err != nil {
		return err
	}
	current := exists && have.Tree.SHA == tree.SHA && len(have.Parents) == 1 && have.Parents[0].SHA == base
	if !current {
		var created struct{ SHA string }
		if err := write("POST", path+"/git/commits", map[string]any{"message": title, "tree": tree.SHA, "parents": []string{base}}, &created); err != nil {
			return err
		}
		if exists {
			if err := write("PATCH", path+"/git/refs/heads/"+refPath, map[string]any{"sha": created.SHA, "force": true}, nil); err != nil {
				return err
			}
		} else if err := write("POST", path+"/git/refs", map[string]string{"ref": "refs/heads/" + branch, "sha": created.SHA}, nil); err != nil {
			return err
		}
	}
	if len(prs) > 0 {
		if prs[0].Title == title && prs[0].Body == body.String() {
			return nil
		}
		return write("PATCH", fmt.Sprintf("%s/pulls/%d", path, prs[0].Number), map[string]string{"title": title, "body": body.String()}, nil)
	}
	return write("POST", path+"/pulls", map[string]string{"title": title, "body": body.String(), "head": branch, "base": baseBranch}, nil)
}
