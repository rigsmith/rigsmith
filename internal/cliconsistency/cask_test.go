package cliconsistency

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

// The casks used to strip the macOS quarantine attribute after install, from
// the days when releases were unsigned. They are Developer ID signed and
// notarized now, so the hook was disabling a check that passes — and Homebrew
// had deprecated the `postflight` stanza GoReleaser emits for it, so every
// `brew upgrade` printed a warning naming our tap.
//
// Nothing in a Go test suite would notice it coming back. A hook is config, it
// reaches users through a generated cask in another repository, and the warning
// only appears on somebody else's machine at upgrade time. So the gate is here:
// adding `hooks:` to a cask fails the build that adds it.
//
// If a hook is ever genuinely needed again, this test is the conversation about
// it — GoReleaser still cannot emit `postflight_steps`, so whatever goes in
// will carry the deprecation with it.
func TestCasksDeclareNoInstallHooks(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Casks []struct {
			Name  string         `yaml:"name"`
			Hooks map[string]any `yaml:"hooks"`
		} `yaml:"homebrew_casks"`
	}
	if uerr := yaml.Unmarshal(body, &cfg); uerr != nil {
		t.Fatal(uerr)
	}
	if len(cfg.Casks) == 0 {
		t.Fatal("no homebrew_casks in .goreleaser.yaml — this guard is checking nothing")
	}

	for _, c := range cfg.Casks {
		if len(c.Hooks) > 0 {
			t.Errorf("cask %q declares hooks, which GoReleaser emits as the deprecated "+
				"`postflight` stanza — every `brew upgrade` will warn, naming our tap. "+
				"If the hook is a quarantine strip, the binaries are signed and notarized "+
				"and do not need one.", c.Name)
		}
	}

	// And the artefact of the old hook, whichever cask it might reappear under.
	// Checked as text because a hook written any other way still produces it.
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue // the comment explaining why it is gone is welcome to say so
		}
		if strings.Contains(line, "com.apple.quarantine") {
			t.Errorf("the quarantine strip is back in .goreleaser.yaml: %q\n"+
				"Signed, notarized binaries pass Gatekeeper; stripping the attribute "+
				"turns that check off on every machine that installs.", trimmed)
		}
	}
}
