// Package mcp reads Codex's MCP server configuration and says what will happen
// to it on the way to another machine.
//
// codexrig deliberately does NOT add, remove or edit servers: `codex mcp` does
// that already, and a second writer for one TOML table is a way for the two to
// disagree. What Codex cannot tell you is the thing this package exists for —
// which of your servers will work on another machine, and which will arrive
// needing something the backup could not carry.
//
// Two things stop a server from travelling, and they are different problems:
// a value in its [env] table is a secret, so it is redacted and the other
// machine has to supply its own; and a command or cwd that is an absolute path
// outside your home cannot be portablized, so it will arrive spelled for this
// machine.
package mcp

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/codexrig/codec"
	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
)

// Transport is how Codex reaches a server.
type Transport string

const (
	Stdio Transport = "stdio"
	HTTP  Transport = "http"
)

// Server is one configured MCP server.
type Server struct {
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	URL     string            `json:"url,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled bool              `json:"enabled"`
}

// Transport reports how this server is reached.
func (s Server) Transport() Transport {
	if s.URL != "" {
		return HTTP
	}
	return Stdio
}

// Summary is the one-line form: the URL, or the command and its arguments.
func (s Server) Summary() string {
	if s.URL != "" {
		return s.URL
	}
	return strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
}

// Portability is what will survive a sync and a restore.
type Portability struct {
	// SecretEnv names the [env] keys that are redacted out. Every one of them
	// has to be supplied again on the other machine — Codex will start the
	// server without them and it will fail at the first authenticated call,
	// which is a confusing way to find out.
	SecretEnv []string `json:"secretEnv,omitempty"`
	// LocalPaths names values that are absolute paths this machine's layout
	// cannot express portably, so they arrive spelled for here. Each entry is
	// "<field>=<path>" — command=/opt/x, args[1]=/srv/y — because a field
	// name alone told a script that something needed attention and not what.
	LocalPaths []string `json:"localPaths,omitempty"`
}

// Portable reports whether this server needs nothing on arrival.
func (p Portability) Portable() bool { return len(p.SecretEnv) == 0 && len(p.LocalPaths) == 0 }

// Entry is a server with its verdict.
type Entry struct {
	Server
	Portability Portability `json:"portability"`
}

// List reads the servers configured in a Codex home.
func List(home string, folders pathmap.MapFolders, osToken string) ([]Entry, error) {
	b, err := os.ReadFile(codexhome.Config(home))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := codec.TOML{}.Decode(b)
	if err != nil {
		return nil, err
	}
	root, _ := v.(map[string]any)
	table, _ := root["mcp_servers"].(map[string]any)

	var out []Entry
	for name, raw := range table {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		s := Server{Name: name, Enabled: true}
		s.Command, _ = m["command"].(string)
		s.URL, _ = m["url"].(string)
		s.Cwd, _ = m["cwd"].(string)
		if en, present := m["enabled"].(bool); present {
			s.Enabled = en
		}
		if args, ok := m["args"].([]any); ok {
			for _, a := range args {
				if str, ok := a.(string); ok {
					s.Args = append(s.Args, str)
				}
			}
		}
		if env, ok := m["env"].(map[string]any); ok {
			s.Env = map[string]string{}
			for k, val := range env {
				// An environment value is a string or it is a mistake. Dropping
				// a non-string entry made SecretEnv say a value was not needed
				// on arrival when it was; stringifying it would report
				// portability for a server Codex cannot start. Name it instead.
				str, ok := val.(string)
				if !ok {
					return nil, fmt.Errorf("server %q: env %s is %T, not a string", name, k, val)
				}
				s.Env[k] = str
			}
		}
		out = append(out, Entry{Server: s, Portability: judge(s, folders, osToken)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// judge works out what this server will need on another machine.
func judge(s Server, folders pathmap.MapFolders, osToken string) Portability {
	var p Portability
	// EVERY value under [env] is redacted, not only the ones that look like
	// secrets: the redactor treats env as a secret container, which is the
	// right default and means the answer here does not depend on guessing
	// whether a particular value is one.
	for k := range s.Env {
		p.SecretEnv = append(p.SecretEnv, k)
	}
	sort.Strings(p.SecretEnv)

	check := func(label, v string) {
		if v == "" || !strings.HasPrefix(v, "/") && !looksWindowsAbs(v) {
			return
		}
		if _, ok := pathmap.Portablize(v, folders, osToken); !ok {
			p.LocalPaths = append(p.LocalPaths, label+"="+v)
		}
	}
	check("command", s.Command)
	check("cwd", s.Cwd)
	for i, a := range s.Args {
		check("args["+strconv.Itoa(i)+"]", a)
	}
	sort.Strings(p.LocalPaths)
	p.LocalPaths = dedup(p.LocalPaths)
	return p
}

func looksWindowsAbs(v string) bool {
	return len(v) > 2 && v[1] == ':' && (v[2] == '\\' || v[2] == '/')
}

func dedup(in []string) []string {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}
