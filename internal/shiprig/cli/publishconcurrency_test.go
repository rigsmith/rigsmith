package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A dry run's cost is almost entirely waiting. Each package is one registry
// round trip — the npm adapter asks whether the version already exists, which
// is what lets a dry run say "already published" rather than "would publish" —
// and done one at a time that is a second of latency per package. On the repo
// this was written for, 41 generated wrapper packages took 29 seconds at 27%
// CPU to report what a publish would do.
//
// So dry-run probes run concurrently. These hold the three things that must
// stay true while they do: the report is in workspace order rather than
// completion order, a real publish is still strictly sequential, and the
// concurrency is real rather than aspirational.

const (
	probeSleep   = 200 * time.Millisecond
	probePkgs    = 6
	probeSlowPkg = "aaa-slow"
)

// probeRepo writes a repo of npm packages and a fake `npm` that sleeps on the
// version probe, logging when each call starts and ends so overlap is visible.
func probeRepo(t *testing.T) (root, logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake `npm` on PATH is a shell script")
	}
	root = t.TempDir()
	logPath = filepath.Join(root, "npm.log")

	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".changeset/config.json", `{ "node": { "oidc": "off" } }`)

	// One package sorts first and sleeps longest, so completion order and
	// workspace order genuinely disagree.
	write("packages/"+probeSlowPkg+"/package.json", `{"name":"`+probeSlowPkg+`","version":"1.0.0"}`)
	for i := 1; i < probePkgs; i++ {
		name := "pkg-" + string(rune('a'+i))
		write("packages/"+name+"/package.json", `{"name":"`+name+`","version":"1.0.0"}`)
	}

	bin := filepath.Join(root, "fakebin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// Sleeps on `view` (the probe) and on `publish`; logs start/end with the
	// package directory, so a serial run shows start/end strictly paired.
	fake := "#!/bin/sh\n" +
		"name=$(basename \"$PWD\")\n" +
		"printf 'start %s %s\\n' \"$1\" \"$name\" >> \"" + logPath + "\"\n" +
		"sleep " + strconv.FormatFloat(probeSleep.Seconds(), 'f', -1, 64) + "\n" +
		"printf 'end %s %s\\n' \"$1\" \"$name\" >> \"" + logPath + "\"\n" +
		// A non-zero exit from `view` means "not published", which is the state
		// that makes a dry run report what it would do.
		"if [ \"$1\" = view ]; then exit 1; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(root)
	return root, logPath
}

func runPublish(t *testing.T, args ...string) (string, time.Duration) {
	t.Helper()
	cmd := newPublishCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	start := time.Now()
	if err := cmd.Execute(); err != nil {
		t.Fatalf("publish %v: %v\n%s", args, err, buf.String())
	}
	return buf.String(), time.Since(start)
}

// The report must read in workspace order. Concurrency finishes the shortest
// probe first, so completion order would put the slow package last — and a
// dry run whose order differs from the publish it previews is worse than a
// slow one.
func TestDryRunReportsInWorkspaceOrderNotCompletionOrder(t *testing.T) {
	probeRepo(t)
	out, _ := runPublish(t, "--dry-run", "--no-git-tag", "--yes")

	var seen []string
	for _, line := range strings.Split(out, "\n") {
		for _, name := range packageNamesIn(line) {
			seen = append(seen, name)
		}
	}
	if len(seen) != probePkgs {
		t.Fatalf("reported %d package(s), want %d:\n%s", len(seen), probePkgs, out)
	}
	if seen[0] != probeSlowPkg {
		t.Errorf("first reported package is %q, want %q — the report is in completion order, "+
			"not workspace order", seen[0], probeSlowPkg)
	}
	sorted := append([]string(nil), seen...)
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1] > sorted[i] {
			t.Errorf("packages are not in workspace order: %v", seen)
			break
		}
	}
}

// The saving has to be real: serially this is probePkgs × probeSleep.
func TestDryRunProbesConcurrently(t *testing.T) {
	probeRepo(t)
	_, elapsed := runPublish(t, "--dry-run", "--no-git-tag", "--yes")

	serial := time.Duration(probePkgs) * probeSleep
	// Generous: the point is "not one at a time", not a precise speedup, and CI
	// machines are shared.
	if elapsed > serial*2/3 {
		t.Errorf("dry run took %v; serial would be about %v, so the probes are not overlapping",
			elapsed, serial)
	}
}

// A real publish is a side effect per package. They stay strictly sequential,
// so the log shows each call closing before the next opens.
func TestRealPublishStaysSequential(t *testing.T) {
	_, logPath := probeRepo(t)
	runPublish(t, "--no-git-tag", "--yes")

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	depth := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		switch {
		case strings.HasPrefix(line, "start "):
			depth++
			if depth > 1 {
				t.Fatalf("two npm calls overlapped during a real publish:\n%s", raw)
			}
		case strings.HasPrefix(line, "end "):
			depth--
		}
	}
}

// packageNamesIn pulls the package name out of a report line, which reads
// "<status> <name>@<version>  <message>".
func packageNamesIn(line string) []string {
	fields := strings.Fields(line)
	for _, f := range fields {
		if i := strings.LastIndex(f, "@"); i > 0 {
			return []string{f[:i]}
		}
	}
	return nil
}
