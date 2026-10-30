// Package completion serves rig-family zsh scripts without constructing a command tree.
package completion

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

//go:embed zsh.tmpl
var zshTemplate string

// Try handles the static zsh completion commands before normal CLI startup.
// Other shells and Cobra's runtime __complete protocol use the ordinary command tree.
func Try(args []string, name string, out io.Writer) (bool, error) {
	if len(args) < 2 || args[0] != "completion" {
		return false, nil
	}
	switch args[1] {
	case "zsh":
		if len(args) != 2 {
			return false, nil
		}
		_, err := io.WriteString(out, ZshScript(name))
		return true, err
	case "install":
		dev := len(args) == 3 && args[2] == "--dev"
		if len(args) != 2 && !dev {
			return false, nil
		}
		if dev {
			name += "-dev"
		}
		exe, err := os.Executable()
		if err != nil {
			return true, err
		}
		path, changed, err := Install(name, exe, "")
		if err != nil {
			return true, err
		}
		state := "already current"
		if changed {
			state = "installed"
		}
		_, err = fmt.Fprintf(out, "%s: %s (%s)\n", name, path, state)
		return true, err
	}
	return false, nil
}

// Run keeps the completion path ahead of the command tree and its setup work.
func Run(args []string, name string, out io.Writer, normal func() error) error {
	if handled, err := Try(args, name, out); handled {
		return err
	}
	return normal()
}

// ZshScript is Cobra's zsh completion script, with only the command name substituted.
// Completion candidates still come from the running tool when Tab is pressed.
func ZshScript(name string) string {
	script := strings.ReplaceAll(zshTemplate, "\r\n", "\n")
	script = strings.ReplaceAll(script, "{{NAME}}", name)
	return strings.ReplaceAll(script, "{{PAD}}", strings.Repeat(" ", 37-len(name)))
}

// Dir is the directory to prepend to zsh's fpath before compinit.
func Dir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "rigsmith", "zsh", "site-functions"), nil
}

// Install writes an autoloadable zsh function and refreshes it when the binary
// content changes. The optional dir is for callers that manage their own fpath.
func Install(name, executable, dir string) (string, bool, error) {
	if !validName(name) {
		return "", false, fmt.Errorf("unsupported completion name %q", name)
	}
	if dir == "" {
		var err error
		dir, err = Dir()
		if err != nil {
			return "", false, err
		}
	}
	bin, err := os.Open(executable)
	if err != nil {
		return "", false, err
	}
	defer bin.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, bin); err != nil {
		return "", false, err
	}
	script := ZshScript(name)
	firstLine, rest, _ := strings.Cut(script, "\n")
	content := []byte(fmt.Sprintf("%s\n# rigsmith binary sha256: %x\n%s", firstLine, hash.Sum(nil), rest))
	path := filepath.Join(dir, "_"+name)
	if old, err := os.ReadFile(path); err == nil && string(old) == string(content) {
		return path, false, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, err
	}
	tmp, err := os.CreateTemp(dir, ".rigsmith-completion-*")
	if err != nil {
		return "", false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return "", false, err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", false, err
	}
	if err := tmp.Close(); err != nil {
		return "", false, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", false, err
	}
	return path, true, nil
}

func validName(name string) bool {
	switch name {
	case "rig", "shiprig", "changerig", "clauderig", "rig-dev", "shiprig-dev", "changerig-dev", "clauderig-dev":
		return true
	}
	return false
}
