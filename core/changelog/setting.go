// Package changelog ports net-changesets' changelog enrichment: interpreting
// the @changesets `changelog` config (default / changelog-git /
// changelog-github), resolving the commit (and PR/author) that introduced each
// changeset, and decorating release lines accordingly.
//
// Ported from net-changesets' Shared/ChangelogSetting.cs,
// Commands/Version/Helpers/ChangelogCommitResolver.cs and
// Commands/Version/Helpers/ChangelogReleaseLine.cs.
package changelog

import (
	"encoding/json"

	"github.com/rigsmith/rigsmith/core/config"
)

// Kind is the changelog generator to use, mirroring the @changesets
// `changelog` key.
type Kind int

const (
	// KindDefault is the default format: the changeset summary as the entry.
	KindDefault Kind = iota
	// KindGit prepends the short commit hash, e.g. `- a1b2c3d: summary`
	// (@changesets/changelog-git).
	KindGit
	// KindGitHub adds PR links, commit links, and author thanks
	// (@changesets/changelog-github).
	KindGitHub
)

// Setting is the resolved changelog setting: which generator and, for GitHub,
// the `owner/repo` used to build links (empty when not configured).
type Setting struct {
	Kind Kind
	Repo string
}

// ParseSetting interprets the polymorphic `changelog` config value:
// false/null/absent (or any unrecognized shape) → default; a generator name
// string → its kind; a [name, options] tuple → the name's kind plus the
// options' "repo" (for changelog-github's { "repo": "owner/repo" }).
//
// As in the C# converter, an unrecognized name (including the stock
// "@changesets/cli/changelog") maps to the default kind, and a tuple's repo is
// extracted whenever the options object carries a string "repo", with a
// missing or non-string repo left empty.
func ParseSetting(cfg *config.Config) Setting {
	raw := trimSpace(cfg.Changelog)
	if len(raw) == 0 {
		return Setting{}
	}

	// String form: a generator name, no options.
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return Setting{Kind: kindFromName(name)}
	}

	// [name, options] tuple form.
	var tuple []json.RawMessage
	if err := json.Unmarshal(raw, &tuple); err == nil {
		setting := Setting{}
		if len(tuple) > 0 {
			var first string
			if json.Unmarshal(tuple[0], &first) == nil {
				setting.Kind = kindFromName(first)
			}
		}
		if len(tuple) > 1 {
			var options struct {
				Repo *string `json:"repo"`
			}
			if json.Unmarshal(tuple[1], &options) == nil && options.Repo != nil {
				setting.Repo = *options.Repo
			}
		}
		return setting
	}

	// false, null, or any other shape: the default format.
	return Setting{}
}

func kindFromName(name string) Kind {
	switch name {
	case "@changesets/changelog-git":
		return KindGit
	case "@changesets/changelog-github":
		return KindGitHub
	default:
		return KindDefault
	}
}

func trimSpace(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\n' || b[j-1] == '\r') {
		j--
	}
	return b[i:j]
}
