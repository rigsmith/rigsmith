package desktop

import "testing"

// A prefix is not a match. "/store/work/data" is a prefix of
// "/store/work/data-old", so a substring test would let one profile's command
// line answer for another — raising the wrong window, and showing one pid on
// two rows of the popover.
func TestCommandHasDataDirNeedsAnArgumentBoundary(t *testing.T) {
	const exe = "/Applications/Claude.app/Contents/MacOS/Claude "
	for _, tc := range []struct {
		name    string
		command string
		dir     string
		want    bool
	}{
		{"exact, end of command", exe + "--user-data-dir=/store/work/data", "/store/work/data", true},
		{"exact, more arguments follow", exe + "--user-data-dir=/store/work/data --enable-logging", "/store/work/data", true},
		{"a longer directory that starts the same", exe + "--user-data-dir=/store/work/data-old", "/store/work/data", false},
		{"a shorter one", exe + "--user-data-dir=/store/work", "/store/work/data", false},
		{"quoted, as Windows writes it", exe + `--user-data-dir="/store/work/data"`, "/store/work/data", true},
		{"no flag at all — the machine-wide app", exe, "/store/work/data", false},
		{"the prefix appears before the real one", exe + "--user-data-dir=/store/work/data-old --user-data-dir=/store/work/data", "/store/work/data", true},
	} {
		if got := CommandHasDataDir(tc.command, tc.dir); got != tc.want {
			t.Errorf("%s: CommandHasDataDir(%q, %q) = %v, want %v", tc.name, tc.command, tc.dir, got, tc.want)
		}
	}
}

// HasDataDir asks the weaker question — is there a profile flag at all — which
// is what tells a profile window from the machine-wide install.
func TestHasDataDir(t *testing.T) {
	if !HasDataDir("/Applications/Claude.app/Contents/MacOS/Claude --user-data-dir=/anything") {
		t.Error("a profile window read as the machine-wide app")
	}
	if HasDataDir("/Applications/Claude.app/Contents/MacOS/Claude") {
		t.Error("the machine-wide app read as a profile window")
	}
}
