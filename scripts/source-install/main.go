// Command source-install builds the rigsmith tools from your working tree and
// installs the real binaries — rig, clauderig, relrig, changerig — onto your
// PATH. It is the stable counterpart to dev-install: where dev-install writes
// "<tool>-dev" launchers that recompile on every run, source-install produces
// ordinary binaries with their real names, so the tools resolve each other on
// PATH the same way a released install does.
//
// Tools are discovered from go.work (any module with a "// Command <name>"
// main.go), so a new workspace module installs automatically. Installers under
// scripts/ are skipped.
//
//	go run ./scripts/source-install                 # install to ~/.local/bin
//	RIGSMITH_INSTALL=/opt/rigsmith go run ./scripts/source-install   # prefix → bin/
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rigsmith/core/gowork"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "source-install:", err)
		os.Exit(1)
	}
}

func run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repo, err := gowork.FindRoot(cwd)
	if err != nil {
		return err
	}
	tools, err := gowork.Tools(repo)
	if err != nil {
		return err
	}

	bin, err := installDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", bin, err)
	}

	var made []string
	for _, t := range tools {
		if strings.HasPrefix(t.Module, "scripts/") {
			continue // don't install the installers themselves
		}
		out := filepath.Join(bin, exeName(t.Name))
		cmd := exec.Command("go", "build", "-C", filepath.Join(repo, t.Module), "-o", out, ".")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("building %s: %w", t.Name, err)
		}
		made = append(made, fmt.Sprintf("  %-12s %s", t.Name, out))
	}

	if len(made) == 0 {
		return fmt.Errorf("no runnable tools found in %s/go.work", repo)
	}
	fmt.Printf("Installed %d binary(ies) from source in %s:\n", len(made), bin)
	fmt.Println(strings.Join(made, "\n"))
	printPathHint(bin)
	return nil
}

// installDir is ${RIGSMITH_INSTALL:-$HOME/.local}/bin — the same prefix the
// release installer (scripts/install.sh) uses.
func installDir() (string, error) {
	prefix := os.Getenv("RIGSMITH_INSTALL")
	if prefix == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		prefix = filepath.Join(home, ".local")
	}
	abs, err := filepath.Abs(filepath.Join(prefix, "bin"))
	if err != nil {
		return "", err
	}
	return abs, nil
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func printPathHint(bin string) {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == bin {
			return // already on PATH
		}
	}
	fmt.Printf("\nNote: %s is not on your PATH. Add it:\n", bin)
	if runtime.GOOS == "windows" {
		fmt.Printf("  setx PATH \"%%PATH%%;%s\"\n", bin)
	} else {
		fmt.Printf("  export PATH=\"%s:$PATH\"   # add to ~/.zshrc or ~/.bashrc\n", bin)
	}
}
