package mcp

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/settings"
)

func folders() pathmap.MapFolders { return pathmap.MapFolders{"HOME": "/Users/x"} }

// env() is the ordinary case: this machine, and a .mcp.json git carries.
func env() Env {
	return Env{Folders: folders(), OS: pathmap.OSMacOS, ProjectFile: CarriageTracked}
}

func kinds(p Portability) []string {
	out := make([]string, 0, len(p.Notes))
	for _, n := range p.Notes {
		out = append(out, string(n.Kind))
	}
	return out
}

func has(p Portability, k NoteKind) bool {
	for _, n := range p.Notes {
		if n.Kind == k {
			return true
		}
	}
	return false
}

// The finding this feature exists for, and the one nothing in clauderig said:
// user- and local-scope servers live in ~/.claude.json, which is a SIBLING of
// the sync root. No allowlist rule reaches it, so they are not backed up at all.
func TestUserAndLocalServersAreNotBackedUp(t *testing.T) {
	for _, scope := range []settings.Scope{settings.User, settings.Local} {
		e := Entry{Name: "thing", Scope: scope, Server: Server{Command: "npx", Args: []string{"-y", "pkg"}}}
		p := Judge(e, env())
		if p.BackedUp {
			t.Errorf("%s scope reported as backed up; it lives in ~/.claude.json, outside the sync root", scope)
		}
		if !has(p, NoteNotBackedUp) {
			t.Errorf("%s scope: notes = %v, want it to say so", scope, kinds(p))
		}
		if p.Clean() {
			t.Errorf("%s scope came back clean", scope)
		}
	}
}

// A project server does travel — through the user's own repository. Saying so
// stops somebody looking for it in the backup.
func TestAProjectServerTravelsInTheRepositoryAndNeedsReapproving(t *testing.T) {
	e := Entry{Name: "thing", Scope: settings.Project, Server: Server{Command: "npx"}}
	p := Judge(e, env())
	if p.BackedUp {
		t.Error("clauderig does not carry .mcp.json; the repo does")
	}
	if p.Carrier != "your repository" {
		t.Errorf("Carrier = %q", p.Carrier)
	}
	if !has(p, NoteInYourRepository) {
		t.Errorf("notes = %v", kinds(p))
	}
	// The definition arrives and the permission does not: approval is recorded
	// in .claude/settings.local.json, which is gitignored by convention.
	if !has(p, NoteNeedsApproval) {
		t.Errorf("notes = %v, want the re-approval warning", kinds(p))
	}
}

// A token in a project server's env is committed to the user's own repo in
// plain text. clauderig redacts what it syncs, and it does not sync that file —
// so the warning has to be different from the one about its own backup.
func TestSecretsInAProjectServerAreCommittedNotRedacted(t *testing.T) {
	e := Entry{Name: "thing", Scope: settings.Project, Server: Server{
		Command: "npx", Env: map[string]string{"API_TOKEN": "ghp_x", "REGION": "eu"},
	}}
	p := Judge(e, env())
	if !has(p, NoteSecretsCommitted) {
		t.Fatalf("notes = %v, want the committed-secrets warning", kinds(p))
	}
	if has(p, NoteNotCommitted) {
		t.Error("a project server's env is not redacted by clauderig; it is committed")
	}
	for _, n := range p.Notes {
		if n.Kind != NoteSecretsCommitted {
			continue
		}
		// EVERY env key, not the ones that look like secrets: the redactor
		// treats env as a container, so the answer does not depend on guessing.
		if len(n.Fields) != 2 {
			t.Errorf("fields = %v, want every env key named", n.Fields)
		}
		for _, f := range n.Fields {
			if !strings.HasPrefix(f, "env.") {
				t.Errorf("field %q is not qualified by its container", f)
			}
		}
	}
}

// Superseded the "portable paths are fine" version of this test. That reading
// borrowed a guarantee from the sync path: Portablize matters for files
// clauderig CARRIES, and it does not carry .mcp.json. Git moves that file byte
// for byte, so /Users/x/bin/server is as broken under a different home as
// /opt/homebrew/bin/server is — the one difference being that clauderig could
// have rewritten the first, if it were ever in a position to rewrite anything.
func TestAnAbsolutePathIsFlaggedWhoeverCouldHaveTranslatedIt(t *testing.T) {
	portable := Entry{Name: "a", Scope: settings.Project, Server: Server{Command: "/Users/x/bin/server"}}
	if !has(Judge(portable, env()), NoteMachinePath) {
		t.Error("a path under HOME went unflagged, but nothing rewrites this file")
	}
	local := Entry{Name: "b", Scope: settings.Project, Server: Server{Command: "/opt/homebrew/bin/server"}}
	p := Judge(local, env())
	if !has(p, NoteMachinePath) {
		t.Errorf("notes = %v, want the machine-path warning for a path outside any known folder", kinds(p))
	}
	relative := Entry{Name: "c", Scope: settings.Project, Server: Server{Command: "npx", Args: []string{"-y", "pkg"}}}
	if has(Judge(relative, env()), NoteMachinePath) {
		t.Error("a bare command is resolved on PATH and is not a machine path")
	}
}

func TestWindowsDriveLettersCountAsAbsolute(t *testing.T) {
	// filepath.IsAbs does not recognise C:\… off Windows, so a config written on
	// a Windows machine would otherwise pass unflagged.
	for _, cmd := range []string{`C:\tools\server.exe`, "C:/tools/server.exe"} {
		e := Entry{Name: "w", Scope: settings.Project, Server: Server{Command: cmd}}
		if !has(Judge(e, env()), NoteMachinePath) {
			t.Errorf("%q was not recognised as an absolute path", cmd)
		}
	}
}

func TestNotesCarryStableTokensNotJustProse(t *testing.T) {
	// Scripts branch on the kind; the sentence beside it can be reworded.
	e := Entry{Name: "thing", Scope: settings.User, Server: Server{Command: "npx"}}
	for _, n := range Judge(e, env()).Notes {
		if n.Kind == "" {
			t.Errorf("note %q has no kind", n.Text)
		}
		if n.Text == "" {
			t.Errorf("note %q has a kind and nothing to read", n.Kind)
		}
	}
}

// No scope Claude Code stores an MCP server in is inside clauderig's sync root:
// user and local live in ~/.claude.json beside it, project lives in the user's
// own repo. That is the whole point of the column, and it is the kind of fact
// that would rot silently if the allowlist ever grew a rule — so pin it here.
func TestJudge_NoScopeIsCarriedByTheBackup(t *testing.T) {
	for _, sc := range []settings.Scope{settings.User, settings.Project, settings.Local} {
		p := Judge(Entry{Scope: sc, Name: "x", Server: Server{Command: "npx"}}, Env{OS: "darwin", ProjectFile: CarriageTracked})
		if p.BackedUp {
			t.Fatalf("scope %v now reports as backed up — if that is deliberate, `travelsText`'s \"yes\" branch is live and this test should say which scope reaches it", sc)
		}
		if len(p.Notes) == 0 {
			t.Fatalf("scope %v travels through nothing and says nothing about it", sc)
		}
	}
}

// "It travels with your repo" is a claim about git, so it has to be checked
// against git. A .mcp.json that is gitignored or was never added is on this
// machine and nowhere else, and reading the file off disk cannot tell.
func TestAProjectServerInAnUncommittedFileDoesNotTravel(t *testing.T) {
	e := Entry{Scope: settings.Project, Name: "tidy", Server: Server{Command: "npx"}}
	for _, tc := range []struct {
		carriage Carriage
		wantWord string
	}{
		{CarriageUntracked, "is not committed"},
		{CarriageIgnored, "gitignore"},
	} {
		p := Judge(e, Env{Folders: folders(), OS: pathmap.OSMacOS, ProjectFile: tc.carriage})
		if p.Carrier != "" {
			t.Errorf("carriage %v: Carrier = %q, want empty — nothing carries it", tc.carriage, p.Carrier)
		}
		if !has(p, NoteNotCommitted) {
			t.Errorf("carriage %v: notes = %v, want not-committed", tc.carriage, kinds(p))
		}
		if has(p, NoteInYourRepository) {
			t.Errorf("carriage %v: still claims the repository carries it", tc.carriage)
		}
		var found bool
		for _, n := range p.Notes {
			if n.Kind == NoteNotCommitted && strings.Contains(n.Text, tc.wantWord) {
				found = true
			}
		}
		if !found {
			t.Errorf("carriage %v: note does not say why: %v", tc.carriage, p.Notes)
		}
	}
}

// A verdict nobody could check says so, rather than guessing confidently.
func TestAProjectServerSaysSoWhenGitCouldNotBeAsked(t *testing.T) {
	e := Entry{Scope: settings.Project, Name: "tidy", Server: Server{Command: "npx"}}
	p := Judge(e, Env{Folders: folders(), OS: pathmap.OSMacOS, ProjectFile: CarriageUnknown})
	if !has(p, NoteCarriageUnknown) {
		t.Errorf("notes = %v, want carriage-unknown", kinds(p))
	}
	if p.Carrier != "" {
		t.Errorf("Carrier = %q — the JSON claimed a carrier for something nobody checked", p.Carrier)
	}
	// A Carriage value nobody has decided the meaning of must land here too,
	// not in the repository case by falling through a default.
	if q := Judge(e, Env{Folders: folders(), OS: pathmap.OSMacOS, ProjectFile: Carriage(99)}); q.Carrier != "" || !has(q, NoteCarriageUnknown) {
		t.Errorf("an unknown Carriage reported %q %v", q.Carrier, kinds(q))
	}
	if has(p, NoteInYourRepository) {
		t.Error("an unchecked verdict claimed the repository carries it")
	}
}

// EVERY absolute path, not only the ones Portablize cannot express. .mcp.json
// travels through git byte for byte and nothing rewrites it, so a path under
// your own home is exactly as broken on a machine with a different home.
func TestEveryAbsolutePathIsFlaggedBecauseNothingRewritesThisFile(t *testing.T) {
	underHome := Entry{Scope: settings.Project, Name: "mine",
		Server: Server{Command: "/Users/x/bin/mine"}}
	p := Judge(underHome, env())
	if !has(p, NoteMachinePath) {
		t.Fatalf("a path under HOME was called portable: %v", kinds(p))
	}
	// And each argument is named by index, so a caller can act on the right one.
	multi := Entry{Scope: settings.Project, Name: "multi", Server: Server{
		Command: "npx", Args: []string{"--root", "/opt/a", "--cache", "/var/b", "rel/c"}}}
	var fields []string
	for _, n := range Judge(multi, env()).Notes {
		if n.Kind == NoteMachinePath {
			fields = n.Fields
		}
	}
	want := []string{"args[1]", "args[3]"}
	if !reflect.DeepEqual(fields, want) {
		t.Errorf("fields = %v, want %v", fields, want)
	}
}

// A UNC path is absolute and is not rooted at a drive letter, so the drive
// check alone reads \\\\server\\share\\x.exe as a relative path.
func TestUNCPathsCountAsAbsolute(t *testing.T) {
	e := Entry{Scope: settings.Project, Name: "unc",
		Server: Server{Command: `\\fileserver\tools\mcp.exe`}}
	if !has(Judge(e, env()), NoteMachinePath) {
		t.Error("a UNC path was not recognised as absolute")
	}
}

// A committed .mcp.json can still hold a server that exists only in the working
// tree, and a clone gets the commit — so the file travelling is necessary and
// not sufficient.
func TestAServerOnlyInTheWorkingCopyDoesNotTravel(t *testing.T) {
	e := Entry{Scope: settings.Project, Name: "brand-new", Server: Server{Command: "npx"}}
	env := Env{Folders: folders(), OS: pathmap.OSMacOS, ProjectFile: CarriageTracked,
		CommittedServers: map[string]string{"already-there": Fingerprint(Server{Command: "npx"})}}
	p := Judge(e, env)
	if p.Carrier != "" {
		t.Errorf("Carrier = %q for a server no clone would receive", p.Carrier)
	}
	if !has(p, NoteNotCommitted) {
		t.Errorf("notes = %v, want not-committed", kinds(p))
	}
	// Its committed neighbour is unaffected.
	e.Name = "already-there"
	if q := Judge(e, env); q.Carrier != "your repository" {
		t.Errorf("a committed server reported Carrier %q", q.Carrier)
	}
	// And with no committed set established, the file-level answer stands.
	env.CommittedServers = nil
	e.Name = "brand-new"
	if q := Judge(e, env); q.Carrier != "your repository" {
		t.Errorf("with carriage unestablished, Carrier = %q", q.Carrier)
	}
}

// env and headers are secret containers; arguments are not, so most of them are
// ordinary and only the ones that LOOK like a credential can be named. A token
// in argv is committed just the same when the server is project-scope.
func TestACredentialInAnArgumentIsNamed(t *testing.T) {
	e := Entry{Scope: settings.Project, Name: "x", Server: Server{
		Command: "npx",
		Args:    []string{"-y", "@acme/mcp", "--token=ghp_" + strings.Repeat("a", 40), "--verbose"},
	}}
	var fields []string
	for _, n := range Judge(e, env()).Notes {
		if n.Kind == NoteSecretsCommitted {
			fields = n.Fields
		}
	}
	if len(fields) != 1 || fields[0] != "args[2]" {
		t.Fatalf("fields = %v, want [args[2]]", fields)
	}
	// The ordinary ones stay quiet, or the note is noise rather than a signal.
	plain := Entry{Scope: settings.Project, Name: "y", Server: Server{
		Command: "npx", Args: []string{"-y", "@acme/tidy-mcp", "--verbose"}}}
	if has(Judge(plain, env()), NoteSecretsCommitted) {
		t.Error("an ordinary argument list was reported as carrying a credential")
	}
}

// A name that survives an edit is not the same server: change the command and a
// clone still receives the old definition, under the same name.
func TestAnEditedServerDoesNotTravelUnderItsCommittedName(t *testing.T) {
	committed := Server{Command: "npx", Args: []string{"-y", "@acme/tidy-mcp"}}
	env := Env{Folders: folders(), OS: pathmap.OSMacOS, ProjectFile: CarriageTracked,
		CommittedServers: map[string]string{"tidy": Fingerprint(committed)}}

	edited := Entry{Scope: settings.Project, Name: "tidy",
		Server: Server{Command: "npx", Args: []string{"-y", "@acme/tidy-mcp", "--fast"}}}
	p := Judge(edited, env)
	if p.Carrier != "" {
		t.Errorf("Carrier = %q for a definition no clone would receive", p.Carrier)
	}
	if !has(p, NoteNotCommitted) {
		t.Errorf("notes = %v, want not-committed", kinds(p))
	}
	// Unedited, it travels — or the check is just refusing everything.
	same := Entry{Scope: settings.Project, Name: "tidy", Server: committed}
	if q := Judge(same, env); q.Carrier != "your repository" {
		t.Errorf("an unchanged committed server reported Carrier %q, notes %v", q.Carrier, kinds(q))
	}
}

// With carriage unconfirmed, nothing downstream may assert what a clone gets:
// "a fresh clone asks again", "these values are committed", "it will fail to
// start there" all describe an arrival nobody established.
func TestAnUncheckedVerdictMakesNoClaimsAboutTheClone(t *testing.T) {
	e := Entry{Scope: settings.Project, Name: "x", Server: Server{
		Command: "/opt/homebrew/bin/dbmcp",
		Env:     map[string]string{"TOKEN": "ghp_" + strings.Repeat("a", 40)},
	}}
	p := Judge(e, Env{Folders: folders(), OS: pathmap.OSMacOS, ProjectFile: CarriageUnknown})
	for _, k := range []NoteKind{NoteNeedsApproval, NoteSecretsCommitted, NoteMachinePath} {
		if has(p, k) {
			t.Errorf("an unconfirmed verdict still claimed %q: %v", k, kinds(p))
		}
	}
	// The same server with carriage confirmed says all three, so the gate is
	// suppressing them rather than the fixture failing to provoke them.
	q := Judge(e, Env{Folders: folders(), OS: pathmap.OSMacOS, ProjectFile: CarriageTracked})
	for _, k := range []NoteKind{NoteNeedsApproval, NoteSecretsCommitted, NoteMachinePath} {
		if !has(q, k) {
			t.Errorf("with carriage confirmed, %q is missing: %v", k, kinds(q))
		}
	}
}

// A committed document whose mcpServers is the wrong shape is unreadable, not
// empty — reporting it as empty tells a user their committed servers are gone.
func TestServerDefsInRejectsAMalformedServersField(t *testing.T) {
	for _, doc := range []string{`{"mcpServers":[]}`, `{"mcpServers":"nope"}`, `{"mcpServers":3}`} {
		if _, err := ServerDefsIn([]byte(doc)); !errors.Is(err, ErrNoServersMap) {
			t.Errorf("ServerDefsIn(%s) err = %v, want ErrNoServersMap", doc, err)
		}
	}
	// Absent and null are genuinely "no servers", and must not be errors.
	for _, doc := range []string{`{}`, `{"mcpServers":null}`, ``} {
		got, err := ServerDefsIn([]byte(doc))
		if err != nil || len(got) != 0 {
			t.Errorf("ServerDefsIn(%q) = %v %v, want an empty map", doc, got, err)
		}
	}
}
