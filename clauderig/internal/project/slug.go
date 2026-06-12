// Package project translates Claude Code project directories (the
// ~/.claude/projects/<slug> session stores) between machines: encoding the slug
// the way Claude Code does, and rewriting it for a target machine's path layout.
package project

import (
	"strings"

	"github.com/rigsmith/core/pathmap"
)

// Flatten encodes an absolute path the way Claude Code names project dirs under
// ~/.claude/projects: every ASCII-non-alphanumeric character becomes '-'.
//
// Confirmed against real slugs: dots collapse (Maryland.Avalonia →
// Maryland-Avalonia), underscores too (gitninja_worktrees → gitninja-worktrees),
// and a separator immediately followed by a dot yields a double dash
// (/nuxt-roost/.dmux → -nuxt-roost--dmux). A drive path flattens the colon and
// backslashes alike (C:\Users\John → C--Users-John).
//
// The encoding is lossy and NOT reversible — a '-' may have been '/', '.', '_',
// or a literal '-'. Recover a project's cwd from its transcript, never by trying
// to un-flatten the slug.
func Flatten(abs string) string {
	var b strings.Builder
	b.Grow(len(abs))
	for _, r := range abs {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// Rewrite translates a project whose original working directory is oldCwd from
// the source machine's layout into the target machine's, returning the target
// slug and cwd.
//
// src maps the source machine's known folders (at minimum HOME) in its native
// form; srcOS is the source OS token; target is a pathmap.Resolver configured for
// the destination machine (its OS + home). The path is portablized against the
// source, then resolved for the target, then re-flattened into a slug.
//
// When oldCwd is not under any known source folder (nothing to translate) or the
// target can't resolve it, Rewrite falls back to the original slug unchanged and
// the returned Status says why (StatusUnconfigured / the resolver's status) — the
// "restore anyway" rule, so an untranslatable session still lands on disk.
func Rewrite(oldCwd string, src map[string]string, srcOS string, target *pathmap.Resolver) (newSlug, newCwd string, status pathmap.Status) {
	tmpl, ok := pathmap.Portablize(oldCwd, src, srcOS)
	if !ok {
		return Flatten(oldCwd), oldCwd, pathmap.StatusUnconfigured
	}
	res := target.Resolve(tmpl)
	if !res.IsResolved() {
		return Flatten(oldCwd), oldCwd, res.Status
	}
	return Flatten(res.Path), res.Path, pathmap.StatusResolved
}
