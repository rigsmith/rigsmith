package pathmap

import "testing"

func mac(home string) KnownFolders { return MapFolders{"HOME": home} }

func TestResolve_PortableHome(t *testing.T) {
	r := NewResolver(mac("/Users/john"), OSMacOS, nil)
	got := r.Resolve("$HOME/Git/x")
	if !got.IsResolved() || got.Path != "/Users/john/Git/x" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolve_TildeSugar(t *testing.T) {
	r := NewResolver(mac("/home/john"), OSLinux, nil)
	if got := r.Resolve("~/Git/x"); got.Path != "/home/john/Git/x" {
		t.Fatalf("got %+v", got)
	}
	// '~' only stands alone as a segment; "~foo" is not HOME sugar → stays literal,
	// then fails the rooted check.
	if got := r.Resolve("~foo/bar"); got.Status != StatusInvalid {
		t.Fatalf("want invalid for ~foo, got %+v", got)
	}
}

func TestResolve_Braces(t *testing.T) {
	r := NewResolver(mac("/Users/john"), OSMacOS, nil)
	if got := r.Resolve("${HOME}/x"); got.Path != "/Users/john/x" {
		t.Fatalf("got %+v", got)
	}
	if got := r.Resolve("${HOME/x"); got.Status != StatusInvalid {
		t.Fatalf("want invalid for unterminated brace, got %+v", got)
	}
}

// The headline cross-OS case: one portable template, expanded into each OS's
// native layout for that machine's home — what restore does per target.
func TestResolve_CrossOS(t *testing.T) {
	tmpl := "$HOME/Git/rigsmith"
	cases := []struct {
		os, home, want string
	}{
		{OSMacOS, "/Users/john", "/Users/john/Git/rigsmith"},
		{OSLinux, "/home/john", "/home/john/Git/rigsmith"},
		{OSWindows, `C:\Users\John`, `C:\Users\John\Git\rigsmith`},
	}
	for _, c := range cases {
		r := NewResolver(MapFolders{"HOME": c.home}, c.os, nil)
		got := r.Resolve(tmpl)
		if !got.IsResolved() || got.Path != c.want {
			t.Errorf("os=%s: got %+v, want %q", c.os, got, c.want)
		}
	}
}

func TestResolve_CascadePrecedence(t *testing.T) {
	tokens := map[string]Cascade{
		"WORK": {
			Override: "",
			PerOS:    map[string]string{OSWindows: `D:\work`, OSMacOS: "/Volumes/work"},
			Portable: "$HOME/work",
		},
	}
	// per-OS literal wins over portable
	r := NewResolver(mac("/Users/john"), OSMacOS, tokens)
	if got := r.Resolve("$WORK/p"); got.Path != "/Volumes/work/p" {
		t.Fatalf("per-os: got %+v", got)
	}
	// no per-OS literal for linux → falls to portable
	rl := NewResolver(mac("/home/john"), OSLinux, tokens)
	if got := rl.Resolve("$WORK/p"); got.Path != "/home/john/work/p" {
		t.Fatalf("portable: got %+v", got)
	}
	// override beats everything
	tokens["WORK"] = Cascade{Override: "/over", PerOS: tokens["WORK"].PerOS, Portable: tokens["WORK"].Portable}
	ro := NewResolver(mac("/Users/john"), OSMacOS, tokens)
	if got := ro.Resolve("$WORK/p"); got.Path != "/over/p" {
		t.Fatalf("override: got %+v", got)
	}
}

func TestResolve_Cycle(t *testing.T) {
	tokens := map[string]Cascade{
		"A": {Portable: "$B/x"},
		"B": {Portable: "$A/y"},
	}
	r := NewResolver(mac("/Users/john"), OSMacOS, tokens)
	got := r.Resolve("$A/z")
	if got.Status != StatusCycle {
		t.Fatalf("want cycle, got %+v", got)
	}
}

func TestResolve_UndefinedToken(t *testing.T) {
	r := NewResolver(mac("/Users/john"), OSMacOS, nil)
	got := r.Resolve("$NOPE/x")
	if got.Status != StatusInvalid || got.Token != "NOPE" {
		t.Fatalf("want invalid NOPE, got %+v", got)
	}
}

func TestResolve_LoneDollarIsLiteral(t *testing.T) {
	// A '$' mid-segment (not at a boundary) is literal; "/a$b" stays as-is and is
	// rooted, so it resolves verbatim.
	r := NewResolver(mac("/Users/john"), OSMacOS, nil)
	if got := r.Resolve("/a$b/c"); !got.IsResolved() || got.Path != "/a$b/c" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolve_BlankUnconfigured(t *testing.T) {
	r := NewResolver(mac("/Users/john"), OSMacOS, nil)
	if got := r.Resolve("   "); got.Status != StatusUnconfigured {
		t.Fatalf("want unconfigured, got %+v", got)
	}
}

func TestResolve_MissingHome(t *testing.T) {
	r := NewResolver(MapFolders{}, OSMacOS, nil)
	got := r.Resolve("~/x")
	if got.Status != StatusUnconfigured || got.MissingToken != "HOME" {
		t.Fatalf("want unconfigured HOME, got %+v", got)
	}
}

func TestResolve_WindowsSeparatorNormalize(t *testing.T) {
	// A portable template written with '/' lands with native '\' on Windows.
	r := NewResolver(MapFolders{"HOME": `C:\Users\John`}, OSWindows, nil)
	if got := r.Resolve("$HOME/Git/x"); got.Path != `C:\Users\John\Git\x` {
		t.Fatalf("got %+v", got)
	}
}

func TestResolve_NotRootedIsInvalid(t *testing.T) {
	r := NewResolver(MapFolders{"REL": "relative/dir"}, OSMacOS, map[string]Cascade{
		"REL": {Portable: "relative/dir"},
	})
	if got := r.Resolve("$REL/x"); got.Status != StatusInvalid {
		t.Fatalf("want invalid for unrooted, got %+v", got)
	}
}
