package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// a simple PATH-binary tool with an install command (the cargo-tool shape).
var testTool = extTool{
	name:    "rig-fake-tool",
	why:     "is only used in tests",
	install: []string{"cargo", "install", "rig-fake-tool"},
}

func TestExtToolMode_Default(t *testing.T) {
	root := t.TempDir()
	// No config → auto.
	if got := testTool.mode(root); got != toolAuto {
		t.Errorf("default mode = %q, want auto", got)
	}
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	// persist defaults to the tools.<name> key.
	testTool.persist(cmd, root, toolInstall)
	if got := testTool.mode(root); got != toolInstall {
		t.Errorf("after persist install, mode = %q, want install", got)
	}
	testTool.persist(cmd, root, toolOff)
	if got := testTool.mode(root); got != toolOff {
		t.Errorf("after persist off, mode = %q, want off", got)
	}
}

func TestExtToolInstallable(t *testing.T) {
	if !testTool.installable("") {
		t.Error("a tool with an install command should be installable")
	}
	// dnx has no install command (ships with the SDK) and no canInstall hook.
	if toolDnx.installable("") {
		t.Error("dnx has no install command, so it isn't installable")
	}
}

func TestExtToolUnavailableErr(t *testing.T) {
	// install command → the error names it.
	if msg := testTool.unavailableErr().Error(); !strings.Contains(msg, "cargo install rig-fake-tool") {
		t.Errorf("error should include the install command, got %q", msg)
	}
	// no install command but a hint → the hint is shown.
	if msg := toolDnx.unavailableErr().Error(); !strings.Contains(msg, "the .NET 10 SDK") {
		t.Errorf("error should include the dnx hint, got %q", msg)
	}
}

func TestExtToolResolver_DefaultLooksUpPATH(t *testing.T) {
	// The default resolver reports unavailable for a nonexistent binary.
	if _, ok := testTool.resolver()("", toolAuto); ok {
		t.Error("a nonexistent binary should not resolve")
	}
}
