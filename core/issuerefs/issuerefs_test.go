package issuerefs

import (
	"reflect"
	"testing"
)

func TestCollect_ForgeMentionVsClosing(t *testing.T) {
	got := Collect([]string{
		"feat: a thing (#7)",        // mention
		"fix: bug\n\nCloses #12",    // closing
		"chore: see #7 for context", // same id as a mention again
	}, nil)
	want := []Ref{
		{ID: "7", Kind: Forge, Closing: false},
		{ID: "12", Kind: Forge, Closing: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCollect_ClosingKeywordForms(t *testing.T) {
	forms := []string{
		"close #1", "closes #2", "closed #3",
		"fix #4", "fixes #5", "fixed #6",
		"resolve #7", "resolves #8", "resolved #9",
		"Closes: #10", // capitalized + colon
		"FIXES #11",   // upper
	}
	for _, msg := range forms {
		refs := Collect([]string{msg}, nil)
		if len(refs) != 1 {
			t.Fatalf("%q: got %d refs, want 1", msg, len(refs))
		}
		if !refs[0].Closing {
			t.Errorf("%q: expected a closing ref, got mention", msg)
		}
	}
}

func TestCollect_ClosingOredAcrossOccurrences(t *testing.T) {
	// One mention and one closing of the same id ⇒ Closing wins.
	got := Collect([]string{"see #4", "fixes #4"}, nil)
	want := []Ref{{ID: "4", Kind: Forge, Closing: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCollect_NotGluedToWord(t *testing.T) {
	// "#" glued to a preceding word char is not a real issue link.
	got := Collect([]string{"abc#9 and version v1#2"}, nil)
	if len(got) != 0 {
		t.Fatalf("expected no refs, got %+v", got)
	}
}

func TestCollect_KeywordInsideWordIsNotClosing(t *testing.T) {
	// "prefix #2" is a valid mention, but the "fix" buried in "prefix" must NOT
	// make it a closing ref.
	got := Collect([]string{"prefix #2 done"}, nil)
	want := []Ref{{ID: "2", Kind: Forge, Closing: false}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCollect_Dedupe(t *testing.T) {
	got := Collect([]string{"#3", "#3", "#3"}, nil)
	if len(got) != 1 || got[0].ID != "3" {
		t.Fatalf("expected one deduped ref #3, got %+v", got)
	}
}

func TestCollect_SortedForgeNumericThenJira(t *testing.T) {
	got := Collect([]string{"#10 #2 #1", "ENG-9 OPS-1"}, []string{"ENG", "OPS"})
	want := []Ref{
		{ID: "1", Kind: Forge},
		{ID: "2", Kind: Forge},
		{ID: "10", Kind: Forge}, // numeric, not lexical (10 after 2)
		{ID: "ENG-9", Kind: Jira},
		{ID: "OPS-1", Kind: Jira},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCollect_Jira(t *testing.T) {
	got := Collect([]string{"ENG-1 mentioned; resolves ENG-2"}, []string{"ENG"})
	want := []Ref{
		{ID: "ENG-1", Kind: Jira, Closing: false},
		{ID: "ENG-2", Kind: Jira, Closing: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCollect_JiraCaseSensitiveAndUnconfigured(t *testing.T) {
	// Lowercase key doesn't match; a project not in the list is ignored.
	got := Collect([]string{"eng-1 and OTHER-2 and ENG-3"}, []string{"ENG"})
	want := []Ref{{ID: "ENG-3", Kind: Jira}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCollect_NoJiraWithoutProjects(t *testing.T) {
	got := Collect([]string{"ENG-1 fixes ENG-2"}, nil)
	if len(got) != 0 {
		t.Fatalf("expected no refs without configured projects, got %+v", got)
	}
}

func TestCollect_Empty(t *testing.T) {
	if got := Collect(nil, []string{"ENG"}); len(got) != 0 {
		t.Fatalf("expected no refs for no messages, got %+v", got)
	}
}

func TestKindString(t *testing.T) {
	if Forge.String() != "forge" || Jira.String() != "jira" {
		t.Fatalf("kind strings: %q / %q", Forge.String(), Jira.String())
	}
}
