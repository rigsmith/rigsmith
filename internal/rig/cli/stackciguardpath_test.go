package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func stackGuardPathFixture(t *testing.T) (root, member, sibling string) {
	t.Helper()
	root = t.TempDir()
	member = filepath.Join(root, "Acme Member With Long Name", "Member Project.csproj")
	sibling = filepath.Join(root, "Acme Sibling With Long Name", "Sibling Project.csproj")
	for _, path := range []string{member, sibling} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("<Project/>"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return
}

// Exercise the actual diagnostic consumers, not just path utility equality.
func stackGuardCheckPathSpellings(t *testing.T, root, member, sibling, reportedMember, reportedSibling string) {
	t.Helper()
	warning := func(code, path string) string { return fmt.Sprintf("a.cs(1,1): warning %s: fixture [%s]", code, path) }
	var meta stackGuardMetadata
	meta.Items.StackOnlyWarning = []stackGuardItem{{Identity: "ACME0001"}}
	held, visible := stackGuardOutput(warning("ACME0001", reportedMember), member, meta, false)
	if len(held) != 1 || len(visible) != 0 {
		t.Fatalf("same-file declaration not held: %v %v", held, visible)
	}
	held, _ = stackGuardOutput(warning("ACME0001", reportedSibling), member, meta, false)
	if len(held) != 0 {
		t.Fatal("different project's diagnostic held")
	}
	meta.Properties.RigStackspace = "true"
	meta.Properties.RigStackspaceRoot = root
	meta.MemberRoot = filepath.Dir(member)
	siblings, _, fatal := stackGuardVisiblePolicy([]string{warning("CS0168", reportedSibling)}, member, meta, false)
	if fatal || len(siblings) != 1 {
		t.Fatalf("sibling spelling not recognized: %v %v", siblings, fatal)
	}
	siblings, _, fatal = stackGuardVisiblePolicy([]string{warning("CS0168", reportedMember)}, member, meta, false)
	if !fatal || len(siblings) != 0 {
		t.Fatal("member's own warning excused as a sibling")
	}
	meta.Properties.WarningsNotAsErrors = "CS0168"
	_, tolerated, fatal := stackGuardVisiblePolicy([]string{warning("CS0168", reportedMember)}, member, meta, false)
	if fatal || len(tolerated) != 1 {
		t.Fatal("member tolerance did not follow file identity")
	}
	_, _, fatal = stackGuardVisiblePolicy([]string{warning("CS0168", reportedSibling)}, member, meta, true)
	if !fatal {
		t.Fatal("strict excused sibling warning")
	}
	// Both the root and the reported file may have an alternate spelling.
	if !stackGuardWithin(filepath.Dir(reportedMember), member) || !stackGuardWithin(root, reportedSibling) {
		t.Fatal("same-file ancestry was not resolved")
	}
	if stackGuardWithin(filepath.Dir(member), reportedSibling) {
		t.Fatal("sibling attributed to member")
	}
	if stackGuardWithin(root, filepath.Join(root, "missing.csproj")) {
		t.Fatal("missing path qualified for exemption")
	}
	if stackGuardSamePath(member, sibling) {
		t.Fatal("different existing projects have the same identity")
	}
}

func TestStackCIGuardRealPathAliases(t *testing.T) {
	root, member, sibling := stackGuardPathFixture(t)
	memberAlias := filepath.Join(filepath.Dir(member), "Alias.csproj")
	siblingAlias := filepath.Join(filepath.Dir(sibling), "Alias.csproj")
	for source, target := range map[string]string{member: memberAlias, sibling: siblingAlias} {
		if err := os.Link(source, target); err != nil {
			t.Fatal(err)
		}
	}
	stackGuardCheckPathSpellings(t, root, member, sibling, memberAlias, siblingAlias)
	t.Run("directory-symlink", func(t *testing.T) {
		alias := filepath.Join(t.TempDir(), "StackAlias")
		if err := os.Symlink(root, alias); err != nil {
			t.Skipf("directory symlink unavailable: %v", err)
		}
		memberRel, _ := filepath.Rel(root, member)
		siblingRel, _ := filepath.Rel(root, sibling)
		stackGuardCheckPathSpellings(t, alias, member, sibling, filepath.Join(alias, memberRel), filepath.Join(alias, siblingRel))
	})
	t.Run("symlink-outside-stack", func(t *testing.T) {
		external := filepath.Join(t.TempDir(), "External.csproj")
		if err := os.WriteFile(external, []byte("<Project/>"), 0644); err != nil {
			t.Fatal(err)
		}
		alias := filepath.Join(filepath.Dir(sibling), "Outside")
		if err := os.Symlink(filepath.Dir(external), alias); err != nil {
			t.Skipf("directory symlink unavailable: %v", err)
		}
		reported := filepath.Join(alias, filepath.Base(external))
		if stackGuardWithin(root, reported) {
			t.Fatal("outside project qualified as a source sibling through a symlink")
		}
		var meta stackGuardMetadata
		meta.Properties.RigStackspaceRoot = root
		meta.MemberRoot = filepath.Dir(member)
		siblings, _, fatal := stackGuardVisiblePolicy([]string{fmt.Sprintf("a.cs(1,1): warning CS0168: fixture [%s]", reported)}, member, meta, false)
		if !fatal || len(siblings) != 0 {
			t.Fatal("outside warning excused as a sibling")
		}
	})

}
