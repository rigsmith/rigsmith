package commitartifact

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const configuredRemote = "rig-publication"

// NewConfiguredGitTransport connects retained publication to normal system Git
// configuration, including credential helpers installed by gh auth setup-git.
// Git owns authentication and TLS; rig never resolves or stores credentials.
// The initial network path is HTTPS. Absolute local paths support integration
// fixtures. Existing SSH users continue using synchronous sync for now.
//
// The caller must have verified the remote's privacy and hold configuration
// stable for the operation. The private workspace must have no caller-added
// configuration. Global/system Git configuration and helpers are trusted, as in
// existing sync. Repository-local authentication overrides are not imported from
// canonical staging. No command or queued hook is enabled by construction.
func NewConfiguredGitTransport(options GitTransportOptions) (*GitTransport, error) {
	if filepath.IsAbs(options.Remote) {
		t, err := NewGitTransport(options)
		if err == nil {
			t.configured = true
		}
		return t, err
	}
	u, err := url.Parse(options.Remote)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" ||
		u.RawQuery != "" || u.ForceQuery || strings.Contains(options.Remote, "#") ||
		len(options.Remote) > 4096 || strings.ContainsAny(options.Remote, "\x00\r\n\t ") || !transportBranch(options.Branch) {
		return nil, ErrInvalid
	}
	return &GitTransport{remote: options.Remote, branch: options.Branch, configured: true}, nil
}

func (t *GitTransport) gitRemote() string {
	if t.configured {
		return configuredRemote
	}
	return t.remote
}

func (t *GitTransport) configuredCommand(dir string, args ...string) *exec.Cmd {
	// Use the user's system Git and its normal credential configuration. These
	// overrides constrain publication side effects, not credential selection.
	flags := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "gc.auto=0", "-c", "maintenance.auto=false",
		"-c", "remote." + configuredRemote + ".url=" + t.remote,
		"-c", "remote." + configuredRemote + ".pushurl=" + t.remote,
		"-c", "remote." + configuredRemote + ".mirror=false", "-c", "http.followRedirects=false"}
	cmd := exec.Command("git", append(flags, args...)...)
	cmd.Dir = dir
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		// Repository/object overrides must not move the operation out of its
		// private workspace. Keep HOME, GH_*, SSH_* and normal Git config paths.
		switch strings.ToUpper(key) {
		case "GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_SHALLOW_FILE", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_CONFIG", "GIT_TERMINAL_PROMPT":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
	cmd.WaitDelay = 5 * time.Second
	return cmd
}
