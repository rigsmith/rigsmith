package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSourceInstallerWritesFamilyZshCompletions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("zsh installation is for Unix")
	}
	tools := t.TempDir()
	fakeGo := "#!/bin/sh\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = -o ]; then shift; printf 'fake binary' > \"$1\"; exit 0; fi\n  shift\ndone\nexit 1\n"
	if err := os.WriteFile(filepath.Join(tools, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	prefix := t.TempDir()
	data := t.TempDir()
	t.Setenv("RIGSMITH_INSTALL", prefix)
	t.Setenv("XDG_DATA_HOME", data)
	if err := run(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rig", "shiprig", "changerig", "clauderig"} {
		path := filepath.Join(data, "rigsmith", "zsh", "site-functions", "_"+name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s completion missing: %v", name, err)
		}
	}
}
