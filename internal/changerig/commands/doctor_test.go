package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/doctor"
	"github.com/rigsmith/rigsmith/core/ecosystem"
	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/versionstate"
)

func TestCheckChangesetConfig_ScaffoldsAbsent(t *testing.T) {
	dir := t.TempDir()
	ws := &Workspace{Root: dir, ChangesetDir: filepath.Join(dir, ".changeset"), Config: config.Default()}

	r := checkChangesetConfig(ws)
	if r.Status != doctor.Warn || r.Fix == nil {
		t.Fatalf("absent config: got %+v, want Warn with a Fix", r)
	}
	if err := r.Fix(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r2 := checkChangesetConfig(ws); r2.Status != doctor.OK {
		t.Fatalf("after scaffold: %+v, want OK", r2)
	}
}

func TestCheckChangesetConfig_InvalidIsFailNotFixable(t *testing.T) {
	dir := t.TempDir()
	cd := filepath.Join(dir, ".changeset")
	if err := os.MkdirAll(cd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cd, "config.json"), []byte("{ not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{Root: dir, ChangesetDir: cd, Config: config.Default()}

	// A broken-but-present config is a hard fail with no auto-fix — scaffolding is
	// deliberately non-destructive and must not clobber the user's file.
	r := checkChangesetConfig(ws)
	if r.Status != doctor.Fail || r.Fix != nil {
		t.Fatalf("invalid config: got %+v, want Fail with no Fix", r)
	}
}

func TestUniqueEcosystems(t *testing.T) {
	got := UniqueEcosystems(map[string]string{"a": "node", "b": "go", "c": "node"})
	if len(got) != 2 || got[0] != "go" || got[1] != "node" {
		t.Fatalf("UniqueEcosystems = %v, want [go node]", got)
	}
}

// TestCheckReleaseRecord_ReportsEveryDrift: a hand-edited manifest and a
// recorded release that was never tagged are two problems with two remedies.
// doctor used to report only the first one its switch matched, so the edit hid
// every untagged release.
func TestCheckReleaseRecord_ReportsEveryDrift(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "init", "-b", "main")
	gitCmd(t, dir, "config", "user.email", "test@example.com")
	gitCmd(t, dir, "config", "user.name", "Test")
	gitCmd(t, dir, "config", "commit.gpgsign", "false")
	writeF(t, filepath.Join(dir, "package.json"), `{"name":"root","private":true,"workspaces":["packages/*"]}`)
	writeF(t, filepath.Join(dir, "packages", "a", "package.json"), `{"name":"a","version":"1.0.0"}`)
	writeF(t, filepath.Join(dir, "packages", "b", "package.json"), `{"name":"b","version":"1.0.0"}`)
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "chore: initial")
	gitCmd(t, dir, "tag", "a@1.0.0")
	gitCmd(t, dir, "tag", "b@1.0.0")

	// Released a@1.1.0 and b@1.0.1 on record, neither tagged; then a's
	// manifest is edited by hand past its record.
	writeF(t, filepath.Join(dir, "packages", "a", "package.json"), `{"name":"a","version":"1.1.5"}`)
	writeF(t, filepath.Join(dir, "packages", "b", "package.json"), `{"name":"b","version":"1.0.1"}`)
	cd := filepath.Join(dir, ".changeset")
	st := &versionstate.State{}
	st.SetReleased("a", "1.1.0")
	st.SetReleased("b", "1.0.1")
	if err := os.MkdirAll(cd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := versionstate.Write(cd, st); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Parse([]byte(`{"versioning":{"record":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{Root: dir, ChangesetDir: cd, Config: cfg, Registry: ecosystem.Default()}

	// Preconditions: the tree really has both drifts, as the check will see them.
	pkgs, _, err := ws.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range pkgs {
		got[p.Name] = p.Version
	}
	if got["a"] != "1.1.5" || got["b"] != "1.0.1" {
		t.Fatalf("setup: discovered versions %v, want a=1.1.5 b=1.0.1", got)
	}
	if rec, err := versionstate.Read(cd); err != nil || rec.ReleasedAt("a") != "1.1.0" || rec.ReleasedAt("b") != "1.0.1" {
		t.Fatalf("setup: record %+v (err %v), want a=1.1.0 b=1.0.1", rec, err)
	}
	for _, tag := range []string{"a@1.0.0", "b@1.0.0"} {
		if !gitutil.TagExists(ctx, dir, tag) {
			t.Fatalf("setup: tag %s missing", tag)
		}
	}
	for _, tag := range []string{"a@1.1.0", "b@1.0.1"} {
		if gitutil.TagExists(ctx, dir, tag) {
			t.Fatalf("setup: tag %s should not exist", tag)
		}
	}

	rs := checkReleaseRecord(ctx, ws)
	var details []string
	for _, r := range rs {
		if r.Status != doctor.Warn {
			t.Errorf("result %+v: want Warn", r)
		}
		details = append(details, r.Detail)
	}
	all := strings.Join(details, "\n")
	if len(rs) != 2 {
		t.Fatalf("got %d result(s), want 2 (edited + untagged):\n%s", len(rs), all)
	}
	if !strings.Contains(all, "a (1.1.5 here, 1.1.0 recorded)") {
		t.Errorf("hand edit not reported:\n%s", all)
	}
	if !strings.Contains(all, "2 recorded release(s) have no tag: a@1.1.0, b@1.0.1") {
		t.Errorf("untagged releases not reported:\n%s", all)
	}
}

// TestChangesetChecks_ReportsUnparseableChangeset: a changeset the parser
// refuses stops status and version, but doctor used to drop the whole pending
// block on the read error and report "all good". It must fail naming the file
// and the parse error, and still check the changesets that did parse.
func TestChangesetChecks_ReportsUnparseableChangeset(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cd := filepath.Join(dir, ".changeset")
	writeF(t, filepath.Join(cd, "config.json"), `{}`)
	writeF(t, filepath.Join(dir, "go.mod"), "module example.com/lib\n\ngo 1.26\n")
	// One good changeset naming a package that doesn't exist (so the targets
	// check has something to say), one the parser refuses.
	writeChangeset(t, cd, "good", "---\n\"example.com/nope\": patch\n---\n\nfine")
	writeChangeset(t, cd, "neg", "---\nlib: \"\"\n---\n\nNegative case 1.")

	// Preconditions: the strict loader status uses really fails on this tree,
	// on neg.md, and good.md parses on its own.
	if _, err := changeset.Dir(cd, ""); err == nil || !strings.Contains(err.Error(), `changeset "neg"`) {
		t.Fatalf("setup: changeset.Dir err = %v, want a parse error for neg", err)
	}
	data, err := os.ReadFile(filepath.Join(cd, "good.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := changeset.Parse(string(data), "good"); err != nil {
		t.Fatalf("setup: good.md should parse: %v", err)
	}

	cfg, err := config.Load(cd)
	if err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{Root: dir, ChangesetDir: cd, Config: cfg, Registry: ecosystem.Default()}
	rs := changesetChecks(ctx, ws, discover(ctx, ws))

	byName := map[string]doctor.Result{}
	for _, r := range rs {
		byName[r.Name] = r
	}
	bad, ok := byName["changeset files"]
	if !ok {
		t.Fatalf("no unparseable-changeset result in %+v", rs)
	}
	if bad.Status != doctor.Fail {
		t.Errorf("unparseable changeset: status %v, want Fail", bad.Status)
	}
	if !strings.Contains(bad.Detail, ".changeset/neg.md") || !strings.Contains(bad.Detail, `malformed frontmatter line "lib: \"\""`) {
		t.Errorf("detail %q should name .changeset/neg.md and its parse error", bad.Detail)
	}
	if strings.Contains(bad.Detail, "good.md") {
		t.Errorf("detail %q names the changeset that parsed", bad.Detail)
	}
	if fails, _, _ := doctor.Counts([]doctor.Section{{Results: rs}}); fails == 0 {
		t.Error("report has no failing check: doctor would say all good")
	}
	// The other checks still ran, on the file that did parse.
	if p := byName["pending"]; p.Detail != "2 changeset(s)" {
		t.Errorf("pending = %+v, want 2 changeset(s)", p)
	}
	if tg := byName["changeset targets"]; tg.Status != doctor.Warn || !strings.Contains(tg.Detail, "good") {
		t.Errorf("targets check = %+v, want Warn naming good", tg)
	}
}
