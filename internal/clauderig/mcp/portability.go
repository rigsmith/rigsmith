package mcp

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/agentrig/redact"
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
	// NoteSecretsCommitted: env/header values sit in a file the user's own repo
	// commits — clauderig does not redact what it does not sync.
	NoteSecretsCommitted NoteKind = "secrets-committed"
	// NoteMachinePath: a value is an absolute path that cannot be portablized.
	NoteMachinePath NoteKind = "machine-path"
	// NoteNeedsApproval: a project server must be approved again on arrival.
	NoteNeedsApproval NoteKind = "needs-approval"
	// NoteNotCommitted: the project file exists but git will not carry it, so
	// "it travels with the repo" is false for this one.
	NoteNotCommitted NoteKind = "not-committed"
	// NoteCarriageUnknown: git could not be asked whether the project file is
	// committed, so the verdict is a guess and says so.
	NoteCarriageUnknown NoteKind = "carriage-unknown"
)

// Carriage is what the caller could learn about the file a project-scope entry
// lives in. Whether .mcp.json is COMMITTED is what decides "your repo" — an
// ignored or never-added file is on this machine and nowhere else, and reading
// the file off disk cannot tell the difference.
type Carriage int

const (
	CarriageUnknown   Carriage = iota // git could not be asked
	CarriageTracked                   // committed: a clone gets it
	CarriageUntracked                 // present, never added
	CarriageIgnored                   // matched by a gitignore rule
)

// Env is what this machine and this checkout contribute to the verdict.
type Env struct {
	Folders pathmap.MapFolders
	OS      string
	// ProjectFile is the carriage of <repo>/.mcp.json. Ignored for other scopes.
	ProjectFile Carriage
	// CommittedServers fingerprints the servers in the COMMITTED .mcp.json, by
	// name. A tracked file can hold a server that exists only in the working
	// tree, and an EDITED one keeps its name while a clone still receives the
	// old definition — so the value matters, not just the key. nil means "not
	// established", which leaves the file-level answer to stand on its own.
	CommittedServers map[string]string
}

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
func Judge(e Entry, env Env) Portability {
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
		//
		// "The repo carries it" is only true if the repo actually carries it.
		// A .mcp.json that is gitignored or was never added is on this machine
		// and nowhere else, and reading the file off disk cannot tell.
		p.BackedUp = false
		switch env.ProjectFile {
		case CarriageIgnored, CarriageUntracked:
			what := "is not committed"
			if env.ProjectFile == CarriageIgnored {
				what = "is matched by a gitignore rule"
			}
			p.Notes = append(p.Notes, Note{
				Kind: NoteNotCommitted,
				Text: "defined in this repo's .mcp.json, which " + what + " — so a clone does not get it and neither does clauderig. This server works in this checkout only.",
			})
			return p
		case CarriageTracked:
			// A committed FILE can still hold a server that only exists in the
			// working tree, and a clone gets the commit — so the file being
			// carried is necessary and not sufficient.
			if env.CommittedServers != nil {
				committed, ok := env.CommittedServers[e.Name]
				if !ok {
					p.Notes = append(p.Notes, Note{
						Kind: NoteNotCommitted,
						Text: "this repo's .mcp.json is committed, but this server is only in your working copy — commit it, or a clone will not have it.",
					})
					return p
				}
				if committed != Fingerprint(e.Server) {
					p.Notes = append(p.Notes, Note{
						Kind: NoteNotCommitted,
						Text: "this server is committed under this name, but your working copy has changed it — a clone still gets the committed definition, not the one shown here.",
					})
					return p
				}
			}
			p.Carrier = "your repository"
			p.Notes = append(p.Notes, Note{
				Kind: NoteInYourRepository,
				Text: "defined in this repo's .mcp.json, so it travels when the repo does. clauderig is not involved.",
			})
		default:
			// CarriageUnknown, and any value a later change adds without
			// deciding what it means. No Carrier: the JSON would otherwise
			// claim the repository carries this while the note beside it says
			// carriage is unconfirmed, and a caller reading one field would be
			// told something nobody checked.
			p.Notes = append(p.Notes, Note{
				Kind: NoteCarriageUnknown,
				Text: "defined in this repo's .mcp.json — but git could not be asked what it carries, so whether this server travels is unconfirmed.",
			})
			// Nothing below this point applies. "A fresh clone asks again",
			// "these values are committed", "a clone will fail to start it" —
			// every one of them describes what a clone receives, and no clone
			// has been established. The fields are still worth knowing, but not
			// under sentences that assert arrival.
			return p
		}
		// Approval is recorded in .claude/settings.local.json, which is
		// gitignored by convention — so the definition arrives and the
		// permission does not.
		p.Notes = append(p.Notes, Note{
			Kind: NoteNeedsApproval,
			Text: "approval is recorded in .claude/settings.local.json, which is gitignored, so a fresh clone asks again.",
		})

	default:
		return p
	}

	// Only project scope reaches here: the other two return above. So the
	// secret note is always the committed-in-your-repo one.
	// Arguments are not a secret CONTAINER the way env and headers are, so they
	// cannot be reported wholesale — but a token passed as --token=… sits in
	// argv, and in a project server argv is committed. Judged by shape, which
	// is the same backstop the publication scan uses.
	if fields := secretArgs(e.Server); len(fields) > 0 {
		p.Notes = append(p.Notes, Note{
			Kind: NoteSecretsCommitted, Fields: fields,
			Text: "this looks like a credential and it is committed to your repository in plain text — clauderig redacts what it syncs, and it does not sync this file.",
		})
	}

	if fields := secretFields(e.Server); len(fields) > 0 {
		p.Notes = append(p.Notes, Note{
			Kind: NoteSecretsCommitted, Fields: fields,
			Text: "these values are committed to your repository in plain text — clauderig redacts what it syncs, and it does not sync this file.",
		})
	}

	// EVERY absolute path, not only the ones Portablize cannot express.
	// Portablizing is what clauderig does to files it carries, and it does not
	// carry this one: .mcp.json travels through git byte for byte, so a path
	// under your own home is exactly as broken on a machine with a different
	// home as one under /opt. Saying only the latter would give the former a
	// clean bill of health it has not earned.
	if fields := absolutePaths(e.Server); len(fields) > 0 {
		p.Notes = append(p.Notes, Note{
			Kind: NoteMachinePath, Fields: fields,
			Text: "an absolute path travels verbatim in .mcp.json — nothing rewrites it for the machine that clones the repo, so the server will be defined there and fail to start.",
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

// secretArgs names the arguments whose VALUE looks like a credential. Unlike
// env and headers, most arguments are ordinary, so this asks the scanner rather
// than treating the whole list as secret.
func secretArgs(s Server) []string {
	var out []string
	for i, a := range s.Args {
		v := a
		// --token=VALUE carries the secret after the '='; the flag name in
		// front of it is enough to stop the value looking like what it is.
		if eq := strings.IndexByte(a, '='); eq > 0 && strings.HasPrefix(a, "-") {
			v = a[eq+1:]
		}
		if _, ok := redact.LooksSecret(v); ok {
			out = append(out, "args["+strconv.Itoa(i)+"]")
		}
	}
	return out
}

// absolutePaths names the values holding an absolute path. Arguments are named
// by index — args[1], not args — because Fields is what `mcp list --json`
// exposes, and three unportable arguments collapsing to one "args" tells a
// caller there is a problem without saying where.
func absolutePaths(s Server) []string {
	var out []string
	if isAbsPath(s.Command) {
		out = append(out, "command")
	}
	for i, a := range s.Args {
		if isAbsPath(a) {
			out = append(out, "args["+strconv.Itoa(i)+"]")
		}
	}
	return out
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
	// \\server\share\…, a UNC path, which is absolute and is not rooted at a
	// drive letter. Checked before the drive form because it matches neither.
	if strings.HasPrefix(v, `\\`) {
		return true
	}
	// C:\… and C:/…, which filepath.IsAbs does not recognise off Windows.
	return len(v) > 2 && v[1] == ':' && (v[2] == '\\' || v[2] == '/')
}
