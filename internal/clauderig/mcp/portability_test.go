package mcp

import (
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/settings"
)

func folders() pathmap.MapFolders { return pathmap.MapFolders{"HOME": "/Users/x"} }

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
		p := Judge(e, folders(), pathmap.OSMacOS)
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
	p := Judge(e, folders(), pathmap.OSMacOS)
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
	p := Judge(e, folders(), pathmap.OSMacOS)
	if !has(p, NoteSecretsCommitted) {
		t.Fatalf("notes = %v, want the committed-secrets warning", kinds(p))
	}
	if has(p, NoteSecretsNotCarried) {
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

func TestAnAbsolutePathOutsideAKnownFolderIsFlagged(t *testing.T) {
	portable := Entry{Name: "a", Scope: settings.Project, Server: Server{Command: "/Users/x/bin/server"}}
	if has(Judge(portable, folders(), pathmap.OSMacOS), NoteMachinePath) {
		t.Error("a path under HOME can be translated and should not be flagged")
	}
	local := Entry{Name: "b", Scope: settings.Project, Server: Server{Command: "/opt/homebrew/bin/server"}}
	p := Judge(local, folders(), pathmap.OSMacOS)
	if !has(p, NoteMachinePath) {
		t.Errorf("notes = %v, want the machine-path warning for a path outside any known folder", kinds(p))
	}
	relative := Entry{Name: "c", Scope: settings.Project, Server: Server{Command: "npx", Args: []string{"-y", "pkg"}}}
	if has(Judge(relative, folders(), pathmap.OSMacOS), NoteMachinePath) {
		t.Error("a bare command is resolved on PATH and is not a machine path")
	}
}

func TestWindowsDriveLettersCountAsAbsolute(t *testing.T) {
	// filepath.IsAbs does not recognise C:\… off Windows, so a config written on
	// a Windows machine would otherwise pass unflagged.
	for _, cmd := range []string{`C:\tools\server.exe`, "C:/tools/server.exe"} {
		e := Entry{Name: "w", Scope: settings.Project, Server: Server{Command: cmd}}
		if !has(Judge(e, folders(), pathmap.OSMacOS), NoteMachinePath) {
			t.Errorf("%q was not recognised as an absolute path", cmd)
		}
	}
}

func TestNotesCarryStableTokensNotJustProse(t *testing.T) {
	// Scripts branch on the kind; the sentence beside it can be reworded.
	e := Entry{Name: "thing", Scope: settings.User, Server: Server{Command: "npx"}}
	for _, n := range Judge(e, folders(), pathmap.OSMacOS).Notes {
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
		p := Judge(Entry{Scope: sc, Name: "x", Server: Server{Command: "npx"}}, pathmap.MapFolders{}, "darwin")
		if p.BackedUp {
			t.Fatalf("scope %v now reports as backed up — if that is deliberate, `travelsText`'s \"yes\" branch is live and this test should say which scope reaches it", sc)
		}
		if len(p.Notes) == 0 {
			t.Fatalf("scope %v travels through nothing and says nothing about it", sc)
		}
	}
}
