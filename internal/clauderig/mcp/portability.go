package mcp

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/settings"
)

// What happens to an MCP server on another machine, which `claude mcp` cannot
// say and clauderig never did either.
//
// The dominant answer turned out not to be about secrets. It is about SCOPE:
// user- and local-scope servers live in ~/.claude.json, which sits beside the
// sync root rather than inside it, so clauderig does not carry them at all. A
// person who set up their servers once and assumed a backup covered them has
// been wrong the whole time, and nothing told them.

// NoteKind is a stable token for one thing a person has to know. Scripts branch
// on these; the prose beside them can be reworded freely.
type NoteKind string

const (
	// NoteNotBackedUp: clauderig's backup does not carry this definition.
	NoteNotBackedUp NoteKind = "not-backed-up"
	// NoteInYourRepository: it travels, but through the user's own git repo.
	NoteInYourRepository NoteKind = "in-your-repository"
	// NoteSecretsNotCarried: env/header values are redacted out of the backup.
	NoteSecretsNotCarried NoteKind = "secrets-not-carried"
	// NoteSecretsCommitted: env/header values sit in a file the user's own repo
	// commits — clauderig does not redact what it does not sync.
	NoteSecretsCommitted NoteKind = "secrets-committed"
	// NoteMachinePath: a value is an absolute path that cannot be portablized.
	NoteMachinePath NoteKind = "machine-path"
	// NoteNeedsApproval: a project server must be approved again on arrival.
	NoteNeedsApproval NoteKind = "needs-approval"
)

// Note is one thing to know, as a token and a sentence.
type Note struct {
	Kind NoteKind `json:"kind"`
	Text string   `json:"text"`
	// Fields names what the note is about — env keys, or which value held the
	// path — so a caller can act without parsing the sentence.
	Fields []string `json:"fields,omitempty"`
}

// Portability is the verdict for one server.
type Portability struct {
	// BackedUp is whether clauderig's own backup carries this definition.
	BackedUp bool `json:"backedUp"`
	// Carrier names what does carry it, when anything does.
	Carrier string `json:"carrier,omitempty"`
	Notes   []Note `json:"notes,omitempty"`
}

// Clean reports whether a server needs nothing said about it.
func (p Portability) Clean() bool { return p.BackedUp && len(p.Notes) == 0 }

// Judge works out what will happen to a server on another machine.
//
// folders and osToken describe THIS machine, and are what decide whether an
// absolute path can be expressed portably.
func Judge(e Entry, folders pathmap.MapFolders, osToken string) Portability {
	var p Portability

	switch e.Scope {
	case settings.User, settings.Local:
		// ~/.claude.json is a sibling of ~/.claude, and the sync root is
		// ~/.claude. No allowlist rule reaches it, so nothing here travels.
		p.BackedUp = false
		where := "every project on this machine"
		if e.Scope == settings.Local {
			where = "this project on this machine"
		}
		p.Notes = append(p.Notes, Note{
			Kind: NoteNotBackedUp,
			Text: "defined in ~/.claude.json, which clauderig does not sync — it sits beside ~/.claude rather than inside it. " +
				"Servers here work for " + where + " and will not appear on another one; add them again there.",
		})
		return p

	case settings.Project:
		// It travels, but through the user's own repository rather than
		// through the backup — which is usually what they want, and is worth
		// saying plainly so they do not go looking for it in the wrong place.
		p.BackedUp = false
		p.Carrier = "your repository"
		p.Notes = append(p.Notes, Note{
			Kind: NoteInYourRepository,
			Text: "defined in this repo's .mcp.json, so it travels when the repo does. clauderig is not involved.",
		})
		// Approval is recorded in .claude/settings.local.json, which is
		// gitignored by convention — so the definition arrives and the
		// permission does not.
		p.Notes = append(p.Notes, Note{
			Kind: NoteNeedsApproval,
			Text: "approval is recorded in .claude/settings.local.json, which is gitignored, so a fresh clone asks again.",
		})
	}

	if fields := secretFields(e.Server); len(fields) > 0 {
		switch e.Scope {
		case settings.Project:
			p.Notes = append(p.Notes, Note{
				Kind: NoteSecretsCommitted, Fields: fields,
				Text: "these values are committed to your repository in plain text — clauderig redacts what it syncs, and it does not sync this file.",
			})
		default:
			p.Notes = append(p.Notes, Note{
				Kind: NoteSecretsNotCarried, Fields: fields,
				Text: "these values are treated as secrets and replaced in the backup; set them again on each machine.",
			})
		}
	}

	if fields := machinePaths(e.Server, folders, osToken); len(fields) > 0 {
		p.Notes = append(p.Notes, Note{
			Kind: NoteMachinePath, Fields: fields,
			Text: "an absolute path here is outside any folder clauderig knows how to translate, so it will arrive spelled for this machine.",
		})
	}
	return p
}

// secretFields names the env and header keys that will not travel in the clear.
//
// EVERY key under them, not the ones that look like secrets: the redactor treats
// env and headers as secret CONTAINERS, which is the right default and means
// this answer does not depend on guessing.
func secretFields(s Server) []string {
	var out []string
	for k := range s.Env {
		out = append(out, "env."+k)
	}
	for k := range s.Headers {
		out = append(out, "headers."+k)
	}
	sort.Strings(out)
	return out
}

// machinePaths names values holding an absolute path that cannot be portablized.
func machinePaths(s Server, folders pathmap.MapFolders, osToken string) []string {
	var out []string
	check := func(label, v string) {
		if !isAbsPath(v) {
			return
		}
		if _, ok := pathmap.Portablize(v, folders, osToken); !ok {
			out = append(out, label)
		}
	}
	check("command", s.Command)
	for _, a := range s.Args {
		check("args", a)
	}
	sort.Strings(out)
	return dedup(out)
}

// isAbsPath is deliberately shape-based rather than a filesystem check: a
// command that does not exist on this machine is still an absolute path, and
// still will not resolve on another one.
func isAbsPath(v string) bool {
	if v == "" {
		return false
	}
	if strings.HasPrefix(v, "/") || filepath.IsAbs(v) {
		return true
	}
	// C:\… and C:/…, which filepath.IsAbs does not recognise off Windows.
	return len(v) > 2 && v[1] == ':' && (v[2] == '\\' || v[2] == '/')
}

func dedup(in []string) []string {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}
