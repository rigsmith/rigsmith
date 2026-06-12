// Ported from net-changesets Version/ChangelogFormatterTests.cs (10 cases),
// with a fake Runner instead of a mocked process executor.
package mdfmt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type call struct {
	dir  string
	name string
	args []string
}

func fakeRunner(calls *[]call, err error) Runner {
	return func(dir, name string, args ...string) (string, error) {
		*calls = append(*calls, call{dir, name, args})
		return "", err
	}
}

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWhenDisabledDoesNothing(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "CHANGELOG.md", "# x\n")
	var calls []call
	FormatFiles([]string{f}, "", dir, fakeRunner(&calls, nil), nil)
	if len(calls) != 0 {
		t.Errorf("disabled format ran %d command(s)", len(calls))
	}
}

func TestWithNoFilesDoesNothing(t *testing.T) {
	var calls []call
	FormatFiles(nil, "prettier", t.TempDir(), fakeRunner(&calls, nil), nil)
	if len(calls) != 0 {
		t.Errorf("empty file list ran %d command(s)", len(calls))
	}
}

func TestExplicitOxfmtRunsViaPackageManager(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "CHANGELOG.md", "# x\n")
	var calls []call
	FormatFiles([]string{f}, "oxfmt", dir, fakeRunner(&calls, nil), nil)
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	// No lockfile → npx.
	if calls[0].name != "npx" {
		t.Errorf("executable = %q, want npx", calls[0].name)
	}
	if want := []string{"oxfmt", "--write", f}; strings.Join(calls[0].args, " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", calls[0].args, want)
	}
}

func TestDenoRunsDenoFmtDirectly(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "CHANGELOG.md", "# x\n")
	var calls []call
	FormatFiles([]string{f}, "deno", dir, fakeRunner(&calls, nil), nil)
	if len(calls) != 1 || calls[0].name != "deno" {
		t.Fatalf("calls = %+v, want one direct deno invocation", calls)
	}
	if want := []string{"fmt", f}; strings.Join(calls[0].args, " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", calls[0].args, want)
	}
}

func TestAutoDetectsFormatterFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "CHANGELOG.md", "# x\n")
	writeTemp(t, dir, ".prettierrc", "{}")
	var calls []call
	FormatFiles([]string{f}, "auto", dir, fakeRunner(&calls, nil), nil)
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	if calls[0].name != "npx" || calls[0].args[0] != "prettier" {
		t.Errorf("auto with .prettierrc → %q %v, want npx prettier", calls[0].name, calls[0].args)
	}

	// Detection order: dprint outranks prettier when both configs exist.
	writeTemp(t, dir, "dprint.json", "{}")
	calls = nil
	FormatFiles([]string{f}, "auto", dir, fakeRunner(&calls, nil), nil)
	if len(calls) != 1 || calls[0].args[0] != "dprint" {
		t.Errorf("auto with dprint.json+prettierrc → %v, want dprint first", calls)
	}
}

func TestAutoNoFormatterConfiguredDoesNothing(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "CHANGELOG.md", "# x\n")
	var calls []call
	FormatFiles([]string{f}, "auto", dir, fakeRunner(&calls, nil), nil)
	if len(calls) != 0 {
		t.Errorf("auto with no config files ran %d command(s)", len(calls))
	}
}

func TestInPnpmRepoUsesPnpmExec(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "CHANGELOG.md", "# x\n")
	writeTemp(t, dir, "pnpm-lock.yaml", "")
	var calls []call
	FormatFiles([]string{f}, "prettier", dir, fakeRunner(&calls, nil), nil)
	if len(calls) != 1 || calls[0].name != "pnpm" {
		t.Fatalf("calls = %+v, want pnpm", calls)
	}
	if want := []string{"exec", "prettier", "--write", f}; strings.Join(calls[0].args, " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", calls[0].args, want)
	}
}

func TestUnknownFormatterDoesNothing(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "CHANGELOG.md", "# x\n")
	var calls []call
	var warned []string
	warnf := func(format string, a ...any) { warned = append(warned, format) }
	FormatFiles([]string{f}, "rubocop", dir, fakeRunner(&calls, nil), warnf)
	if len(calls) != 0 {
		t.Errorf("unknown formatter ran %d command(s)", len(calls))
	}
	if len(warned) != 1 {
		t.Errorf("expected one warning, got %v", warned)
	}
}

func TestWhenFormatterCannotStartDegradesGracefully(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "CHANGELOG.md", "# x\n")
	var calls []call
	var warned []string
	warnf := func(format string, a ...any) { warned = append(warned, format) }
	// Must not panic or propagate the error — just warn.
	FormatFiles([]string{f}, "prettier", dir, fakeRunner(&calls, errors.New("exec: not found")), warnf)
	if len(warned) != 1 {
		t.Errorf("expected one warning, got %v", warned)
	}
}

func TestCustomCommandRunsAsWritten(t *testing.T) {
	dir := t.TempDir()
	f := writeTemp(t, dir, "CHANGELOG.md", "# x\n")
	var calls []call
	FormatFilesCustom([]string{f}, []string{"myfmt", "--write"}, dir, fakeRunner(&calls, nil), nil)
	if len(calls) != 1 || calls[0].name != "myfmt" || calls[0].dir != dir {
		t.Fatalf("calls = %+v, want one myfmt run in %s", calls, dir)
	}
	if want := []string{"--write", f}; strings.Join(calls[0].args, " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", calls[0].args, want)
	}

	// Empty inputs are no-ops; failure only warns.
	calls = nil
	FormatFilesCustom(nil, []string{"myfmt"}, dir, fakeRunner(&calls, nil), nil)
	FormatFilesCustom([]string{f}, nil, dir, fakeRunner(&calls, nil), nil)
	if len(calls) != 0 {
		t.Errorf("empty inputs ran %d command(s)", len(calls))
	}
	var warned []string
	FormatFilesCustom([]string{f}, []string{"myfmt"}, dir,
		fakeRunner(&calls, errors.New("exec: not found")),
		func(format string, a ...any) { warned = append(warned, format) })
	if len(warned) != 1 {
		t.Errorf("expected one warning, got %v", warned)
	}
}

func TestNativeRewritesInPlaceWithoutRunningProcess(t *testing.T) {
	dir := t.TempDir()
	// Raw @changesets output: no blank line between version and section heading.
	raw := "# pkg-a\n\n## 1.1.0\n### Minor Changes\n\n- A cool change\n"
	f := writeTemp(t, dir, "CHANGELOG.md", raw)
	var calls []call
	FormatFiles([]string{f}, "native", dir, fakeRunner(&calls, nil), nil)
	if len(calls) != 0 {
		t.Errorf("native formatter ran %d subprocess(es)", len(calls))
	}
	got, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if want := Format(raw); string(got) != want {
		t.Errorf("file not rewritten natively:\ngot  %q\nwant %q", got, want)
	}
	if !strings.Contains(string(got), "## 1.1.0\n\n### Minor Changes") {
		t.Errorf("expected the blank line between version and section, got:\n%s", got)
	}

	// Already formatted → file untouched (same content, no error).
	before, _ := os.ReadFile(f)
	FormatFiles([]string{f}, "native", dir, fakeRunner(&calls, nil), nil)
	after, _ := os.ReadFile(f)
	if string(before) != string(after) {
		t.Error("native re-run changed an already-formatted file")
	}
}
