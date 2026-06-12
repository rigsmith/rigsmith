package changeset

import "testing"

func TestParseStandard(t *testing.T) {
	content := `---
"PackageA": major
"PackageB": minor
---

Add a telemetry endpoint

With a longer body.`
	cs, err := Parse(content, "happy-lions-jump")
	if err != nil {
		t.Fatal(err)
	}
	if cs.ID != "happy-lions-jump" {
		t.Errorf("ID = %q", cs.ID)
	}
	if len(cs.Releases) != 2 {
		t.Fatalf("releases = %d, want 2", len(cs.Releases))
	}
	if cs.Releases[0] != (Release{"PackageA", BumpMajor}) {
		t.Errorf("release[0] = %+v", cs.Releases[0])
	}
	if cs.Releases[1] != (Release{"PackageB", BumpMinor}) {
		t.Errorf("release[1] = %+v", cs.Releases[1])
	}
	want := "Add a telemetry endpoint\n\nWith a longer body."
	if cs.Summary != want {
		t.Errorf("summary = %q, want %q", cs.Summary, want)
	}
}

func TestParseEmpty(t *testing.T) {
	content := "---\n---\n\nforce a release\n"
	cs, err := Parse(content, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Releases) != 0 {
		t.Fatalf("releases = %d, want 0", len(cs.Releases))
	}
	if cs.Summary != "force a release\n" {
		t.Errorf("summary = %q", cs.Summary)
	}
}

func TestParseCRLF(t *testing.T) {
	content := "---\r\n\"Pkg\": patch\r\n---\r\n\r\nfix it"
	cs, err := Parse(content, "crlf")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Releases) != 1 || cs.Releases[0].Bump != BumpPatch {
		t.Fatalf("releases = %+v", cs.Releases)
	}
	if cs.Summary != "fix it" {
		t.Errorf("summary = %q", cs.Summary)
	}
}

func TestRenderRoundTrip(t *testing.T) {
	releases := []Release{{"PackageA", BumpMajor}, {"PackageB", BumpMinor}}
	out := Render(releases, "a change", "", false)
	cs, err := Parse(out, "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Releases) != 2 || cs.Releases[0].Bump != BumpMajor || cs.Releases[1].Bump != BumpMinor {
		t.Errorf("round-trip releases = %+v", cs.Releases)
	}
	if cs.Summary != "a change" {
		t.Errorf("round-trip summary = %q", cs.Summary)
	}
}

func TestTypeDrivenChangeset(t *testing.T) {
	// type: feat with bumpless package lines (the type-driven form).
	out := Render([]Release{{"Core", BumpNone}}, "add a feature", "feat", false)
	cs, err := Parse(out, "x")
	if err != nil {
		t.Fatal(err)
	}
	if cs.Type != "feat" || cs.Breaking {
		t.Errorf("type = %q breaking = %v", cs.Type, cs.Breaking)
	}
	if len(cs.Releases) != 1 || cs.Releases[0].Bump != BumpNone {
		t.Errorf("releases = %+v (want one bumpless)", cs.Releases)
	}
	typ, breaking, ok := cs.EffectiveType()
	if !ok || typ != "feat" || breaking {
		t.Errorf("EffectiveType = %q %v %v", typ, breaking, ok)
	}
}

func TestBreakingType(t *testing.T) {
	cs, err := Parse("---\ntype: feat!\n\"Core\"\n---\n\ndrop legacy API", "x")
	if err != nil {
		t.Fatal(err)
	}
	if cs.Type != "feat" || !cs.Breaking {
		t.Errorf("type=%q breaking=%v", cs.Type, cs.Breaking)
	}
}

func TestConventionalFromSummary(t *testing.T) {
	cs, err := Parse("---\n\"Core\"\n---\n\nfix!: correct the thing", "x")
	if err != nil {
		t.Fatal(err)
	}
	typ, breaking, ok := cs.EffectiveType()
	if !ok || typ != "fix" || !breaking {
		t.Errorf("EffectiveType from summary = %q %v %v", typ, breaking, ok)
	}
}

func TestBumpMax(t *testing.T) {
	if BumpPatch.Max(BumpMajor) != BumpMajor {
		t.Error("patch.Max(major) should be major")
	}
	if BumpMinor.Max(BumpNone) != BumpMinor {
		t.Error("minor.Max(none) should be minor")
	}
}
