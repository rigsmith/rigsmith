package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// wireTwoMembers builds the state every case here starts from: two members that
// cross, and the overlay wire writes for them.
func wireTwoMembers(t *testing.T, root string) {
	t.Helper()
	csproj(t, root, "app/src/App", "Acme.App", "Acme.Lib")
	csproj(t, root, "lib/src/Lib", "Acme.Lib")
	var out bytes.Buffer
	if _, err := stackWire(context.Background(), &out, owned("app", "lib"), &gitrepo.Repo{Dir: root}, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "Directory.Build.targets")); err != nil {
		t.Fatalf("setup did not leave an overlay to test against: %v", err)
	}
}

// A seed clone is a manifest, an overlay, and no members. Nothing crosses
// between members that are not there — which reads identically to "the overlay
// is left over" and is a completely different fact. doctor advised deleting it;
// wire did delete it. On a fresh clone that is the first thing anyone runs.
func TestUnimportedWorkspaceKeepsItsOverlay(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	wireTwoMembers(t, root)

	// The seed state: the overlay and the manifest are committed, the member
	// directories are not.
	for _, dir := range []string{"app", "lib"} {
		if err := os.RemoveAll(filepath.Join(root, dir)); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if _, err := stackWire(ctx, &out, owned("app", "lib"), &gitrepo.Repo{Dir: root}, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "Directory.Build.targets")); err != nil {
		t.Fatalf("wire removed the overlay the workspace is about to need: %v", err)
	}
	if !strings.Contains(out.String(), "not imported yet") {
		t.Errorf("wire did not say why it did nothing: %q", out.String())
	}

	// doctor reads the same state and must not advise what wire just refused.
	_, _, notes, failed := stackCheckOverlay(ctx, root, owned("app", "lib"))
	if len(failed) != 0 {
		t.Fatalf("scan failed: %v", failed)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "not imported yet") {
		t.Fatalf("notes = %v, want one saying the members are not imported", notes)
	}
}

// Half an import is not an import. The links are read from the members' own
// build files, so a member that is not there contributes none — and an overlay
// written from that graph is missing whatever crossed through it. The damage
// arrives by a slower route than deleting the file outright, which is why it
// was easy to miss.
func TestPartlyImportedWorkspaceKeepsItsOverlay(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	wireTwoMembers(t, root)

	// lib is imported; app, which is what consumes it, is not.
	if err := os.RemoveAll(filepath.Join(root, "app")); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if _, err := stackWire(ctx, &out, owned("app", "lib"), &gitrepo.Repo{Dir: root}, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "Directory.Build.targets")); err != nil {
		t.Fatalf("a half-imported workspace had its overlay rewritten away: %v", err)
	}
	if !strings.Contains(out.String(), "app not imported yet") {
		t.Errorf("wire did not name the member it was waiting on: %q", out.String())
	}
}

// The other end of the same rule. When `rm` takes the last member, nothing can
// cross because there is nothing left to cross between — so the overlay really
// is stale, and the verb that judges it has to run rather than defer.
func TestRemovingTheLastMemberTakesTheOverlay(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	wireTwoMembers(t, root)

	// What `rm` leaves behind: an empty manifest and an empty tree.
	for _, dir := range []string{"app", "lib"} {
		if err := os.RemoveAll(filepath.Join(root, dir)); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if _, err := stackWire(ctx, &out, owned(), &gitrepo.Repo{Dir: root}, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "Directory.Build.targets")); !os.IsNotExist(err) {
		t.Fatalf("the overlay outlived the last member it pointed at: %v", err)
	}
}
