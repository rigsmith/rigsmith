package pathmap

import "testing"

func TestPortablize_Home(t *testing.T) {
	tmpl, ok := Portablize("/Users/john/Git/x", map[string]string{"HOME": "/Users/john"}, OSMacOS)
	if !ok || tmpl != "$HOME/Git/x" {
		t.Fatalf("got %q ok=%v", tmpl, ok)
	}
}

func TestPortablize_ExactHome(t *testing.T) {
	tmpl, ok := Portablize("/Users/john", map[string]string{"HOME": "/Users/john"}, OSMacOS)
	if !ok || tmpl != "$HOME" {
		t.Fatalf("got %q ok=%v", tmpl, ok)
	}
}

func TestPortablize_Windows(t *testing.T) {
	tmpl, ok := Portablize(`C:\Users\John\Git\x`, map[string]string{"HOME": `C:\Users\John`}, OSWindows)
	if !ok || tmpl != "$HOME/Git/x" {
		t.Fatalf("got %q ok=%v", tmpl, ok)
	}
}

func TestPortablize_LongestPrefixWins(t *testing.T) {
	folders := map[string]string{"HOME": "/Users/john", "GIT": "/Users/john/Git"}
	tmpl, ok := Portablize("/Users/john/Git/x", folders, OSMacOS)
	if !ok || tmpl != "$GIT/x" {
		t.Fatalf("got %q ok=%v (want $GIT/x)", tmpl, ok)
	}
}

func TestPortablize_BoundaryNotSubstring(t *testing.T) {
	// /Users/johnny must NOT match home /Users/john (not a segment boundary).
	tmpl, ok := Portablize("/Users/johnny/x", map[string]string{"HOME": "/Users/john"}, OSMacOS)
	if ok {
		t.Fatalf("should not match across a segment boundary, got %q", tmpl)
	}
}

func TestPortablize_CaseInsensitiveOffLinux(t *testing.T) {
	// macOS: home /Users/john matches a path captured as /Users/John.
	if tmpl, ok := Portablize("/Users/John/x", map[string]string{"HOME": "/Users/john"}, OSMacOS); !ok || tmpl != "$HOME/x" {
		t.Fatalf("macos fold: got %q ok=%v", tmpl, ok)
	}
	// Linux: case-sensitive, so it does NOT match.
	if _, ok := Portablize("/Users/John/x", map[string]string{"HOME": "/Users/john"}, OSLinux); ok {
		t.Fatalf("linux should be case-sensitive")
	}
}

func TestPortablize_LeadingDoubleSlash(t *testing.T) {
	// A permission rule's "//Users/john/Git/x/**" still portablizes (the leading
	// slash-run collapses to match the home prefix; the glob suffix is preserved).
	tmpl, ok := Portablize("//Users/john/Git/gitninja/**", map[string]string{"HOME": "/Users/john"}, OSMacOS)
	if !ok || tmpl != "$HOME/Git/gitninja/**" {
		t.Fatalf("got %q ok=%v", tmpl, ok)
	}
}

func TestPortablize_NoMatch(t *testing.T) {
	if _, ok := Portablize("/opt/elsewhere", map[string]string{"HOME": "/Users/john"}, OSMacOS); ok {
		t.Fatalf("unrelated path should not portablize")
	}
}

// Round-trip: portablize on the source machine, resolve on the target machine.
func TestPortablize_RoundTripCrossOS(t *testing.T) {
	tmpl, ok := Portablize("/Users/john/Git/rigsmith", map[string]string{"HOME": "/Users/john"}, OSMacOS)
	if !ok {
		t.Fatal("portablize failed")
	}
	r := NewResolver(MapFolders{"HOME": `C:\Users\John`}, OSWindows, nil)
	if got := r.Resolve(tmpl); got.Path != `C:\Users\John\Git\rigsmith` {
		t.Fatalf("round-trip got %+v", got)
	}
}
