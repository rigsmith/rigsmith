package commitartifact

import (
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// SSHOptions selects a trusted OpenSSH executable, one identity file and an
// explicit host-key database. All paths must be absolute. The caller owns these
// files and must keep them stable during an operation. No key bytes are copied
// into artifacts, argv or the environment. Agents and passphrase prompts are
// disabled; an encrypted identity cannot silently fall back to another key.
type SSHOptions struct {
	Executable, IdentityFile, KnownHostsFile string
}

// sshRemote accepts explicit users in SSH URLs and scp-style destinations.
// Keep the original spelling for queue binding and Git's relative-path semantics.
// Conservative ASCII components exclude shell syntax, URL escapes and options.
func sshRemote(remote string) bool {
	var user, host, path string
	bracketed := false
	if strings.HasPrefix(remote, "ssh://") {
		u, err := url.Parse(remote)
		if err != nil || u.User == nil || u.Opaque != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(remote, "%?#") {
			return false
		}
		if _, password := u.User.Password(); password {
			return false
		}
		user, host, path = u.User.Username(), u.Hostname(), u.Path
		bracketed = strings.HasPrefix(u.Host, "[")
		if !strings.HasPrefix(path, "/") {
			return false
		}
		if port := u.Port(); port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return false
			}
		} else if strings.HasSuffix(u.Host, ":") {
			return false
		}
	} else {
		var rest string
		var ok bool
		user, rest, ok = strings.Cut(remote, "@")
		if !ok {
			return false
		}
		if strings.HasPrefix(rest, "[") {
			bracketed = true
			end := strings.Index(rest, "]:")
			if end < 0 {
				return false
			}
			host, path = rest[1:end], rest[end+2:]
		} else {
			host, path, ok = strings.Cut(rest, ":")
			if !ok {
				return false
			}
		}
	}
	if !sshComponent(user, "_-") || strings.HasPrefix(user, "-") || len(user) > 128 {
		return false
	}
	if net.ParseIP(host) == nil {
		if bracketed {
			return false
		}
		if len(host) > 253 {
			return false
		}
		for _, label := range strings.Split(host, ".") {
			if !sshComponent(label, "-") || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return false
			}
		}
	}
	if !sshComponent(path, "/._-") || strings.HasPrefix(path, "-") {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func sshComponent(s, punctuation string) bool {
	if s == "" {
		return false
	}
	for _, ch := range s {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune(punctuation, ch)) {
			return false
		}
	}
	return true
}

// OpenSSH expands tokens/environment variables inside file options. Reject
// those forms rather than letting a worker login redirect a bound credential.
// IsAbs already excludes leading home expansion; interior tildes are literal
// and must remain valid for Windows short paths such as C:/Users/RUNNER~1.
func sshFile(path string) bool {
	if !filepath.IsAbs(path) || len(path) > 4096 {
		return false
	}
	for _, ch := range filepath.ToSlash(path) {
		if ch < 32 || ch == 127 || strings.ContainsRune("\"\\%$", ch) {
			return false
		}
	}
	return true
}

func sshArguments(options SSHOptions) ([]string, error) {
	if !sshFile(options.Executable) || !sshFile(options.IdentityFile) || !sshFile(options.KnownHostsFile) {
		return nil, ErrInvalid
	}
	args := []string{filepath.ToSlash(options.Executable), "-F", "none", "-T"}
	for _, option := range []string{
		// Unlike -i, IdentityFile configuration retains a missing path and thus
		// prevents OpenSSH from replacing it with default identity files.
		"IdentityFile=\"" + filepath.ToSlash(options.IdentityFile) + "\"",
		"BatchMode=yes", "IdentitiesOnly=yes", "IdentityAgent=none", "CertificateFile=none",
		"PreferredAuthentications=publickey", "PasswordAuthentication=no", "KbdInteractiveAuthentication=no",
		"StrictHostKeyChecking=yes", "UserKnownHostsFile=\"" + filepath.ToSlash(options.KnownHostsFile) + "\"",
		"GlobalKnownHostsFile=none", "KnownHostsCommand=none", "VerifyHostKeyDNS=no", "UpdateHostKeys=no", "CheckHostIP=no",
		"ForwardAgent=no", "ForwardX11=no", "ClearAllForwardings=yes", "PermitLocalCommand=no",
		"ProxyCommand=none", "ProxyJump=none", "ControlMaster=no", "ControlPath=none", "ControlPersist=no",
		"CanonicalizeHostname=no", "AddKeysToAgent=no",
	} {
		args = append(args, "-o", option)
	}
	return args, nil
}

func sshCommand(options SSHOptions) (string, error) {
	args, err := sshArguments(options)
	if err != nil {
		return "", err
	}

	// Git interprets GIT_SSH_COMMAND using its shell, including Git for Windows.
	// Quote every argument, then quote paths again for OpenSSH's option parser
	// where it accepts lists (UserKnownHostsFile). No caller supplies shell text.
	for i := range args {
		args[i] = "'" + strings.ReplaceAll(args[i], "'", "'\\''") + "'"
	}
	return strings.Join(args, " "), nil
}
