package forge

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rigsmith/rigsmith/core/issuerefs"
)

// ResolvedIssueNumbers returns the distinct forge issue numbers referenced by
// the released commit messages (the ${issues} value), in the deduplicated,
// sorted order issuerefs.Collect produces. Jira refs are ignored.
func ResolvedIssueNumbers(messages []string) []int {
	var nums []int
	for _, r := range issuerefs.Collect(messages, nil) {
		if r.Kind != issuerefs.Forge {
			continue
		}
		if n, err := strconv.Atoi(r.ID); err == nil {
			nums = append(nums, n)
		}
	}
	return nums
}

// IssuesConfig is the release `issues` step's behavior, mirrored from
// config.Issues (kept local so the forge package doesn't import core/config).
type IssuesConfig struct {
	Comment string // body template; {{version}} expands to the released versions. Empty ⇒ no comment.
	Close   bool   // close issues referenced with a closing keyword
}

// IssueProvider drives a forge's issue CLI. Like the release Provider, probes
// (HasComment) execute through the Runner; mutations are returned as argv so
// RunIssues stays the single execute+report loop.
type IssueProvider interface {
	Name() string
	// HasComment reports whether the issue already carries a comment containing
	// marker (the idempotency check). A provider that can't read comments returns
	// false (and may therefore re-comment on a re-run).
	HasComment(id, marker, repoRoot string, run Runner) bool
	CommentCmd(id, body string) []string
	CloseCmd(id string) []string
}

// issueProviderFor maps a resolved forge name to its issue provider. Only GitHub
// is wired today; GitLab/Gitea/Jira issue automation are planned follow-ups, so
// other forges return nil (RunIssues reports a clean skip).
func issueProviderFor(forge string) IssueProvider {
	if forge == "github" {
		return githubIssues{}
	}
	return nil
}

// RunIssues comments on / closes the issues referenced by the released commit
// messages. The forge is chosen exactly as the release step chooses it (reusing
// selectProvider), so issues land on the same forge the release did; if the
// release degraded to tags-only, issue automation is skipped too. Comments are
// idempotent via a per-release hidden marker. releasedVersion labels the release
// (it fills the comment's {{version}} and the dedupe marker). Returns ok=false
// only when a forge command exits non-zero.
func RunIssues(messages []string, sel Selection, cfg IssuesConfig, releasedVersion, repoRoot string, run Runner, report func(lines ...string)) (ok bool, message string) {
	if report == nil {
		report = func(...string) {}
	}

	rel, skip := selectProvider(sel, repoRoot, run)
	if rel == nil {
		return true, skip // tags-only ⇒ no issue automation either
	}
	ip := issueProviderFor(rel.Name())
	if ip == nil {
		return true, fmt.Sprintf("Issue automation not yet supported for %s; skipped.", rel.Name())
	}

	refs := issuerefs.Collect(messages, nil) // Jira deferred: forge refs only
	marker := "<!-- shiprig-released: " + releasedVersion + " -->"
	body := strings.ReplaceAll(cfg.Comment, "{{version}}", releasedVersion)

	commented, closed := 0, 0
	for _, r := range refs {
		if r.Kind != issuerefs.Forge {
			continue
		}
		if cfg.Comment != "" {
			if ip.HasComment(r.ID, marker, repoRoot, run) {
				report("issue #" + r.ID + ": already commented for this release, skipped.")
			} else {
				if ok, msg := runForgeCmd(ip.CommentCmd(r.ID, body+"\n\n"+marker), repoRoot, run, report); !ok {
					return false, fmt.Sprintf("issue #%s comment failed: %s", r.ID, msg)
				}
				commented++
			}
		}
		if cfg.Close && r.Closing {
			if ok, msg := runForgeCmd(ip.CloseCmd(r.ID), repoRoot, run, report); !ok {
				return false, fmt.Sprintf("issue #%s close failed: %s", r.ID, msg)
			}
			closed++
		}
	}

	if commented == 0 && closed == 0 {
		return true, "Issues: nothing to comment or close."
	}
	return true, fmt.Sprintf("Issues: %d commented, %d closed.", commented, closed)
}

// --- GitHub issues (gh issue) ------------------------------------------------

type githubIssues struct{}

func (githubIssues) Name() string { return "github" }
func (githubIssues) HasComment(id, marker, root string, run Runner) bool {
	out, err := run(root, "gh", "issue", "view", id, "--json", "comments")
	if err != nil {
		return false
	}
	return strings.Contains(out, marker)
}
func (githubIssues) CommentCmd(id, body string) []string {
	return []string{"gh", "issue", "comment", id, "--body", body}
}
func (githubIssues) CloseCmd(id string) []string {
	return []string{"gh", "issue", "close", id}
}
