package merge

import (
	"encoding/json"
	"strings"
	"testing"
)

func resolve(t *testing.T, path, ours, theirs string) Result {
	t.Helper()
	res, ok := Resolve(Sides{Path: path, Ours: []byte(ours), Theirs: []byte(theirs)})
	if !ok {
		t.Fatalf("no policy resolved %s", path)
	}
	return res
}

// The registry is the file both machines touch on every sync, each writing only
// its own row. A whole-file pick would delete the other machine's row outright —
// the loss this package exists to prevent.
func TestDevicesUnionKeepsBothMachines(t *testing.T) {
	ours := `{"schema":1,"devices":{
		"Pro16":{"name":"Pro16","os":"macos","lastSync":"2026-08-08T12:00:00Z","claudeVersion":"2.1.223"}}}`
	theirs := `{"schema":1,"devices":{
		"Air13":{"name":"Air13","os":"macos","lastSync":"2026-08-08T09:00:00Z","claudeVersion":"2.1.212"}}}`

	res := resolve(t, "clauderig-devices.json", ours, theirs)

	var got deviceRegistry
	if err := json.Unmarshal(res.Content, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Devices) != 2 {
		t.Fatalf("want both machines, got %d: %s", len(got.Devices), res.Content)
	}
	if _, ok := got.Devices["Pro16"]; !ok {
		t.Error("lost the local machine")
	}
	if _, ok := got.Devices["Air13"]; !ok {
		t.Error("lost the remote machine")
	}
}

// When both sides carry the same machine, the newer lastSync wins for that row
// only — the rest of the registry is untouched.
func TestDevicesUnionTakesNewerRow(t *testing.T) {
	ours := `{"schema":1,"devices":{
		"Air13":{"name":"Air13","lastSync":"2026-08-08T09:00:00Z","claudeVersion":"old"},
		"Pro16":{"name":"Pro16","lastSync":"2026-08-08T12:00:00Z"}}}`
	theirs := `{"schema":1,"devices":{
		"Air13":{"name":"Air13","lastSync":"2026-08-08T18:00:00Z","claudeVersion":"new"}}}`

	res := resolve(t, "clauderig-devices.json", ours, theirs)

	var got struct {
		Devices map[string]struct {
			LastSync      string `json:"lastSync"`
			ClaudeVersion string `json:"claudeVersion"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(res.Content, &got); err != nil {
		t.Fatal(err)
	}
	if got.Devices["Air13"].ClaudeVersion != "new" {
		t.Errorf("older row won: %s", res.Content)
	}
	if got.Devices["Pro16"].LastSync == "" {
		t.Errorf("untouched row was dropped: %s", res.Content)
	}
}

// An older remote row must not clobber a newer local one.
func TestDevicesUnionKeepsNewerLocal(t *testing.T) {
	ours := `{"schema":1,"devices":{"Air13":{"lastSync":"2026-08-08T18:00:00Z","claudeVersion":"new"}}}`
	theirs := `{"schema":1,"devices":{"Air13":{"lastSync":"2026-08-08T09:00:00Z","claudeVersion":"old"}}}`

	res := resolve(t, "clauderig-devices.json", ours, theirs)
	if !strings.Contains(string(res.Content), `"new"`) {
		t.Fatalf("stale remote row overwrote a newer local one: %s", res.Content)
	}
}

// The documented hazard, as a test: the manifest's projects map auto-merges
// entries from both machines, and only claudeVersion actually conflicts. A
// whole-file resolution silently drops the other machine's projects.
func TestManifestUnionKeepsBothProjectSets(t *testing.T) {
	ours := `{"schema":1,"claudeVersion":"2.1.223","sourceOS":"macos","projects":{
		"-Users-john-Git-a":{"template":"$HOME/Git/a","cwd":"/Users/john/Git/a"}}}`
	theirs := `{"schema":1,"claudeVersion":"2.1.212","sourceOS":"macos","projects":{
		"-Users-john-Git-b":{"template":"$HOME/Git/b","cwd":"/Users/john/Git/b"}}}`

	res := resolve(t, "clauderig-manifest.json", ours, theirs)

	var got manifestDoc
	if err := json.Unmarshal(res.Content, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 2 {
		t.Fatalf("want both projects, got %d: %s", len(got.Projects), res.Content)
	}
	// Either claudeVersion is acceptable; prefer not going backwards.
	if got.ClaudeVersion != "2.1.223" {
		t.Errorf("claudeVersion = %q, want the higher 2.1.223", got.ClaudeVersion)
	}
}

// Transcripts are append-only, so a session resumed on both machines yields a
// shared prefix and two tails. Both tails have to survive.
func TestTranscriptSupersetUnionsBothTails(t *testing.T) {
	ours := "{\"i\":1}\n{\"i\":2}\n{\"local\":\"a\"}\n"
	theirs := "{\"i\":1}\n{\"i\":2}\n{\"remote\":\"b\"}\n"

	res := resolve(t, "cli/projects/-x/s.jsonl", ours, theirs)
	got := string(res.Content)

	for _, want := range []string{`{"i":1}`, `{"i":2}`, `{"local":"a"}`, `{"remote":"b"}`} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %s from the union:\n%s", want, got)
		}
	}
	// The shared prefix appears once, not twice.
	if strings.Count(got, `{"i":1}`) != 1 {
		t.Errorf("shared prefix duplicated:\n%s", got)
	}
	// Local side first.
	if strings.Index(got, `{"local":"a"}`) > strings.Index(got, `{"remote":"b"}`) {
		t.Errorf("remote tail landed before the local one:\n%s", got)
	}
}

func TestTranscriptSupersetIsIdempotent(t *testing.T) {
	same := "{\"i\":1}\n{\"i\":2}\n"
	res := resolve(t, "s.jsonl", same, same)
	if string(res.Content) != same {
		t.Fatalf("identical sides changed the file:\n%q", res.Content)
	}
}

// Losing a MEMORY.md line orphans a memory file that still exists on disk but
// is never recalled again — so both sides' entries must survive.
func TestMemoryUnionKeepsBothSides(t *testing.T) {
	ours := strings.Join([]string{
		"- [alpha](alpha.md) — local wording",
		"- [beta](beta.md) — shared",
	}, "\n") + "\n"
	theirs := strings.Join([]string{
		"- [beta](beta.md) — REWORDED on the remote",
		"- [gamma](gamma.md) — remote only",
	}, "\n") + "\n"

	res := resolve(t, "memory/MEMORY.md", ours, theirs)
	got := string(res.Content)

	if !strings.Contains(got, "alpha.md") || !strings.Contains(got, "gamma.md") {
		t.Fatalf("union dropped an entry:\n%s", got)
	}
	// Entries on both sides keep OUR text.
	if !strings.Contains(got, "shared") || strings.Contains(got, "REWORDED") {
		t.Errorf("local wording was not preserved:\n%s", got)
	}
	// One line per memory file.
	if strings.Count(got, "beta.md") != 1 {
		t.Errorf("entry duplicated:\n%s", got)
	}
}

// A remote-only entry is spliced after the entry it followed on the remote, so
// related memories stay adjacent instead of piling up at the end.
func TestMemoryUnionSplicesAfterItsAnchor(t *testing.T) {
	ours := strings.Join([]string{
		"- [one](one.md) — x",
		"- [two](two.md) — x",
		"- [three](three.md) — x",
	}, "\n") + "\n"
	theirs := strings.Join([]string{
		"- [one](one.md) — x",
		"- [inserted](inserted.md) — new, followed one",
		"- [three](three.md) — x",
	}, "\n") + "\n"

	res := resolve(t, "memory/MEMORY.md", ours, theirs)
	lines := strings.Split(strings.TrimSpace(string(res.Content)), "\n")

	idx := map[string]int{}
	for i, l := range lines {
		if k, ok := entryKey(l); ok {
			idx[k] = i
		}
	}
	if idx["inserted.md"] != idx["one.md"]+1 {
		t.Fatalf("inserted entry not spliced after its anchor:\n%s", res.Content)
	}
	if idx["two.md"] <= idx["inserted.md"] {
		t.Errorf("splice reordered the local entries:\n%s", res.Content)
	}
}

// Headers and prose around the index must survive untouched.
func TestMemoryUnionPreservesNonEntryLines(t *testing.T) {
	ours := "# Memory index\n\n- [a](a.md) — x\n"
	theirs := "# Memory index\n\n- [a](a.md)— x\n- [b](b.md) — y\n"

	res := resolve(t, "memory/MEMORY.md", ours, theirs)
	got := string(res.Content)
	if !strings.HasPrefix(got, "# Memory index\n\n") {
		t.Fatalf("header lost:\n%q", got)
	}
	if !strings.Contains(got, "b.md") {
		t.Errorf("remote entry lost:\n%s", got)
	}
}

// Caches refetched as a unit take a side wholesale — half of each would be
// incoherent. Desktop writes epoch millis.
func TestNewestTimestampEpochMillis(t *testing.T) {
	ours := `{"lastUpdated":1786221963266,"skills":["local"]}`
	theirs := `{"lastUpdated":1786221999999,"skills":["remote"]}`

	res := resolve(t, "desktop/x/manifest.json", ours, theirs)
	if !strings.Contains(string(res.Content), "remote") {
		t.Fatalf("older copy won:\n%s", res.Content)
	}
}

// The blocklist cache nests an RFC3339 lastUpdated one level into an array.
func TestNewestTimestampNestedRFC3339(t *testing.T) {
	ours := `[{"entries":["local"],"lastUpdated":"2026-08-08T20:22:44.252Z"}]`
	theirs := `[{"entries":["remote"],"lastUpdated":"2026-08-01T10:00:00.000Z"}]`

	res := resolve(t, "desktop/extensions-blocklist.json", ours, theirs)
	if !strings.Contains(string(res.Content), "local") {
		t.Fatalf("stale remote copy won:\n%s", res.Content)
	}
}

// A JSON file with no timestamp has nothing for this policy to go on, so it
// must fall through to a human rather than be decided arbitrarily.
func TestJSONWithoutTimestampIsResidual(t *testing.T) {
	_, ok := Resolve(Sides{
		Path:   "desktop/settings.json",
		Ours:   []byte(`{"theme":"dark"}`),
		Theirs: []byte(`{"theme":"light"}`),
	})
	if ok {
		t.Fatal("a timestamp-less JSON conflict was resolved by guesswork")
	}
}

// Anything the package doesn't understand stays conflicted — silence here would
// mean inventing an answer for a file whose semantics we don't know.
func TestUnknownFileIsResidual(t *testing.T) {
	if _, ok := Resolve(Sides{Path: "skills/thing.md", Ours: []byte("a"), Theirs: []byte("b")}); ok {
		t.Fatal("an unknown file type was resolved")
	}
}

// A file added or deleted on one side only is a judgment call: resurrecting
// something the other machine deleted, or dropping something it kept, is not
// mechanical.
func TestDeleteModifyIsResidual(t *testing.T) {
	if _, ok := Resolve(Sides{Path: "s.jsonl", Ours: []byte("x\n"), Theirs: nil}); ok {
		t.Error("a delete/modify conflict was resolved")
	}
	if _, ok := Resolve(Sides{Path: "s.jsonl", Ours: nil, Theirs: []byte("x\n")}); ok {
		t.Error("a modify/delete conflict was resolved")
	}
}

// Malformed JSON must not be "resolved" into something worse.
func TestCorruptJSONIsResidual(t *testing.T) {
	if _, ok := Resolve(Sides{
		Path: "clauderig-devices.json", Ours: []byte(`{"devices":`), Theirs: []byte(`{"devices":{}}`),
	}); ok {
		t.Fatal("corrupt JSON was resolved")
	}
}

func TestPolicyFor(t *testing.T) {
	cases := map[string]string{
		"clauderig-devices.json":            "devices-union",
		"clauderig-manifest.json":           "manifest-union",
		"cli/projects/x/memory/MEMORY.md":   "memory-union",
		"cli/projects/x/abc.jsonl":          "transcript-superset",
		"desktop/extensions-blocklist.json": "newest-timestamp",
		"skills/whatever.md":                "",
	}
	for rel, want := range cases {
		if got := PolicyFor(rel); got != want {
			t.Errorf("PolicyFor(%q) = %q, want %q", rel, got, want)
		}
	}
}

// Every resolution has to be able to say what it did — the ledger is what makes
// the Resolve button auditable instead of magic.
func TestEveryResultExplainsItself(t *testing.T) {
	cases := []Sides{
		{Path: "clauderig-devices.json", Ours: []byte(`{"schema":1,"devices":{}}`), Theirs: []byte(`{"schema":1,"devices":{}}`)},
		{Path: "clauderig-manifest.json", Ours: []byte(`{"schema":1,"projects":{}}`), Theirs: []byte(`{"schema":1,"projects":{}}`)},
		{Path: "memory/MEMORY.md", Ours: []byte("- [a](a.md) — x\n"), Theirs: []byte("- [a](a.md) — x\n")},
		{Path: "x.jsonl", Ours: []byte("{}\n"), Theirs: []byte("{}\n")},
		{Path: "m.json", Ours: []byte(`{"lastUpdated":1}`), Theirs: []byte(`{"lastUpdated":2}`)},
	}
	for _, s := range cases {
		res, ok := Resolve(s)
		if !ok {
			t.Errorf("%s was not resolved", s.Path)
			continue
		}
		if res.Policy == "" || res.Detail == "" {
			t.Errorf("%s resolved without a ledger entry: %+v", s.Path, res)
		}
	}
}

// dev is one registry row, so a three-way case reads as what each side did to
// the same starting point.
func dev(name, lastSync string) string {
	return `"` + name + `":{"name":"` + name + `","os":"macos","lastSync":"` + lastSync + `"}`
}

func registry(entries ...string) string {
	return `{"schema":1,"devices":{` + strings.Join(entries, ",") + `}}`
}

func resolve3(t *testing.T, path, base, ours, theirs string) Result {
	t.Helper()
	res, ok := Resolve(Sides{Path: path, Base: []byte(base), Ours: []byte(ours), Theirs: []byte(theirs)})
	if !ok {
		t.Fatalf("no policy resolved %s", path)
	}
	return res
}

func devicesIn(t *testing.T, res Result) map[string]json.RawMessage {
	t.Helper()
	var got deviceRegistry
	if err := json.Unmarshal(res.Content, &got); err != nil {
		t.Fatal(err)
	}
	return got.Devices
}

// `clauderig device remove` has to survive the next merge. Without the common
// ancestor, a union cannot tell "they never had this row" from "we deleted it",
// and the removed machine reappears on every sync for ever.
func TestDevicesRemovalIsNotResurrected(t *testing.T) {
	old := dev("Retired", "2026-08-01T09:00:00Z")
	base := registry(dev("Pro16", "2026-08-08T12:00:00Z"), old)
	ours := registry(dev("Pro16", "2026-08-09T12:00:00Z")) // removed here
	theirs := registry(dev("Pro16", "2026-08-08T12:00:00Z"), old)

	got := devicesIn(t, resolve3(t, "clauderig-devices.json", base, ours, theirs))
	if _, back := got["Retired"]; back {
		t.Error("the removed machine came back from the remote's untouched copy")
	}
	if _, ok := got["Pro16"]; !ok {
		t.Error("lost the machine that is still here")
	}
}

// The other direction: a machine removed on the remote must not be resurrected
// by this machine's untouched copy either.
func TestDevicesRemovalOnTheRemoteStands(t *testing.T) {
	old := dev("Retired", "2026-08-01T09:00:00Z")
	base := registry(dev("Pro16", "2026-08-08T12:00:00Z"), old)
	ours := registry(dev("Pro16", "2026-08-08T12:00:00Z"), old)
	theirs := registry(dev("Pro16", "2026-08-09T12:00:00Z")) // removed there

	if _, back := devicesIn(t, resolve3(t, "clauderig-devices.json", base, ours, theirs))["Retired"]; back {
		t.Error("a machine removed on the remote came back")
	}
}

// A machine that has synced since the removal is a live machine again, not the
// stale row someone meant to clear out.
func TestDevicesRemovalYieldsToAMachineThatCameBack(t *testing.T) {
	base := registry(dev("Pro16", "2026-08-08T12:00:00Z"), dev("Air13", "2026-08-01T09:00:00Z"))
	ours := registry(dev("Pro16", "2026-08-09T12:00:00Z")) // Air13 removed here
	theirs := registry(dev("Pro16", "2026-08-08T12:00:00Z"), dev("Air13", "2026-08-10T09:00:00Z"))

	if _, ok := devicesIn(t, resolve3(t, "clauderig-devices.json", base, ours, theirs))["Air13"]; !ok {
		t.Error("a machine that synced after the removal was still dropped")
	}
}

// With no common ancestor there is nothing to distinguish a removal from an
// absence, and keeping too much is the safe way to be wrong.
func TestDevicesWithNoBaseStillUnions(t *testing.T) {
	ours := registry(dev("Pro16", "2026-08-09T12:00:00Z"))
	theirs := registry(dev("Air13", "2026-08-08T09:00:00Z"))

	if n := len(devicesIn(t, resolve(t, "clauderig-devices.json", ours, theirs))); n != 2 {
		t.Errorf("got %d devices, want both kept when there is no base", n)
	}
}
