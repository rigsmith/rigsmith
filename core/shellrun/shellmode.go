package shellrun

import (
	"fmt"
	"strings"
)

// Shell mode names: how a configured shell command line is executed.
const (
	// ShellPortable runs shell strings through the in-process portable shell
	// (NewPortableRunner / RunPortable), so the same line works on every OS.
	ShellPortable = "portable"
	// ShellSystem runs shell strings through the OS shell (/bin/sh -c, cmd.exe
	// /c) — the escape hatch for scripts that need a real userland (sed, awk) or
	// OS-specific syntax.
	ShellSystem = "system"
)

// ShellMode validates and canonicalizes a configured shell choice: "" or
// "portable" → ShellPortable (the cross-platform default), "system" →
// ShellSystem. Any other value is an error so a typo surfaces instead of
// silently changing behavior.
func ShellMode(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", ShellPortable:
		return ShellPortable, nil
	case ShellSystem:
		return ShellSystem, nil
	default:
		return "", fmt.Errorf("unknown shell %q (want %q or %q)", s, ShellPortable, ShellSystem)
	}
}
