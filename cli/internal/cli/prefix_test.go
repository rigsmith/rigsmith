package cli

import "testing"

// Ported from the .NET rig's PrefixResolverTests. The verb list is fixed (as in
// the C# tests) so the cases pin the resolution rules, not the command tree.
var prefixVerbs = []string{"run", "build", "rebuild", "test", "coverage", "kill", "publish", "completion"}

func eqSlice(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestResolvePrefix_UnambiguousPrefixIsRewritten(t *testing.T) {
	eqSlice(t, resolvePrefix([]string{"t", "Foo"}, prefixVerbs), []string{"test", "Foo"})
	eqSlice(t, resolvePrefix([]string{"pub"}, prefixVerbs), []string{"publish"})
}

func TestResolvePrefix_AmbiguousPrefixIsLeftAlone(t *testing.T) {
	// "co" matches both coverage and completion.
	eqSlice(t, resolvePrefix([]string{"co"}, prefixVerbs), []string{"co"})
	// "re" vs "run"/"rebuild" — "re" is only rebuild, but "r" is ambiguous.
	eqSlice(t, resolvePrefix([]string{"r"}, prefixVerbs), []string{"r"})
}

func TestResolvePrefix_ExactMatchAndOptionsPassThrough(t *testing.T) {
	eqSlice(t, resolvePrefix([]string{"test"}, prefixVerbs), []string{"test"})
	eqSlice(t, resolvePrefix([]string{"--help"}, prefixVerbs), []string{"--help"})
	eqSlice(t, resolvePrefix(nil, prefixVerbs), nil)
}

func TestResolvePrefix_UnknownTokenPassesThroughForTheParserToHandle(t *testing.T) {
	eqSlice(t, resolvePrefix([]string{"zzz"}, prefixVerbs), []string{"zzz"})
}

func TestExpandWatch_TurnsALeadingWatchModifierIntoAWatchFlag(t *testing.T) {
	eqSlice(t, expandWatch([]string{"w", "r"}), []string{"r", "--watch"})
	eqSlice(t, expandWatch([]string{"watch", "run", "App"}), []string{"run", "App", "--watch"})
	eqSlice(t, expandWatch([]string{"watch", "test", "Foo", "-c", "X"}), []string{"test", "Foo", "-c", "X", "--watch"})
}

func TestExpandWatch_BareWatchIsEmptyAndNonWatchPassesThrough(t *testing.T) {
	eqSlice(t, expandWatch([]string{"watch"}), nil)
	eqSlice(t, expandWatch([]string{"w"}), nil)
	eqSlice(t, expandWatch([]string{"run", "App"}), []string{"run", "App"})
	eqSlice(t, expandWatch(nil), nil)
}

func TestResolvePrefix_LongerUnambiguousPrefixesStillResolveAsAConvenience(t *testing.T) {
	// Curated short forms (r/c) are native aliases, resolved by the parser;
	// resolvePrefix still handles longer unambiguous prefixes.
	eqSlice(t, resolvePrefix([]string{"cove"}, prefixVerbs), []string{"coverage"})
	eqSlice(t, resolvePrefix([]string{"reb"}, prefixVerbs), []string{"rebuild"})
}
