package cli

import (
	"errors"
	"strings"
	"testing"
)

// runUpgradeCommands keeps going after a failing command so one un-upgradable
// package (a version pinned in imported props / CPM) doesn't abort the rest.
func TestRunUpgradeCommands_ContinuesPastFailures(t *testing.T) {
	cmds := [][]string{
		{"dotnet", "add", "/r/A", "package", "X", "--version", "2"},
		{"dotnet", "add", "/r/B", "package", "Y", "--version", "3"}, // fails
		{"dotnet", "add", "/r/C", "package", "Z", "--version", "4"},
	}
	run := func(argv []string) error {
		for _, a := range argv {
			if a == "/r/B" {
				return errors.New("Cannot edit items in imported files")
			}
		}
		return nil
	}
	done, failed := runUpgradeCommands(run, cmds)
	if done != 2 {
		t.Errorf("done=%d, want 2 (the other two still applied)", done)
	}
	if len(failed) != 1 || failed[0][2] != "/r/B" {
		t.Errorf("failed=%v, want just the /r/B command", failed)
	}
}

func TestDotnetAddLabel(t *testing.T) {
	got := dotnetAddLabel([]string{"dotnet", "add", "/path/to/Tweed.App.csproj", "package", "CommunityToolkit.Mvvm", "--version", "8.4.2"})
	for _, want := range []string{"CommunityToolkit.Mvvm", "8.4.2", "Tweed.App.csproj"} {
		if !strings.Contains(got, want) {
			t.Errorf("label %q missing %q", got, want)
		}
	}
	// Falls back to the joined argv for an unexpected shape.
	if got := dotnetAddLabel([]string{"weird", "command"}); got != "weird command" {
		t.Errorf("fallback = %q, want the joined argv", got)
	}
}

func TestDotnetListFailed(t *testing.T) {
	if dotnetListFailed(`{"projects":[]}`) {
		t.Error("a JSON report is a successful scan")
	}
	if dotnetListFailed("\n  {\"projects\":[]}\n") {
		t.Error("leading whitespace before JSON is still a report")
	}
	if !dotnetListFailed("error NU1605: Detected package downgrade") {
		t.Error("a non-JSON error must read as a failed scan")
	}
	if !dotnetListFailed("") {
		t.Error("empty output must read as a failed scan")
	}
}
