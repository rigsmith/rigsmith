// Package mcp manages Claude Code MCP server definitions across the three scopes
// clauderig understands, editing the canonical Claude Code files directly — the
// same approach the hooks package takes for settings.json:
//
//	user     ~/.claude.json    → mcpServers                  (every project)
//	project  <repo>/.mcp.json  → mcpServers                  (committed, shared)
//	local    ~/.claude.json    → projects[<repo>].mcpServers (this checkout)
//
// Mutations work on the raw decoded document, so servers and keys clauderig
// doesn't touch — including unknown fields on other servers — survive a rewrite.
//
// Enable/disable applies only to project (.mcp.json) servers: Claude Code gates
// those behind approval recorded in settings.json's enabled/disabled lists.
// clauderig records that approval at local scope (settings.local.json) — your
// machine's view, never committed — while reading the merged view of all tiers.
package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/settings"
)

// Transport is an MCP server's connection type.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
	TransportSSE   = "sse"
)

// Server is one MCP server definition in Claude Code's schema. stdio servers use
// Command/Args/Env; http and sse servers use URL/Headers. Only servers clauderig
// writes are reduced to these fields — existing servers keep any extra fields,
// since mutations operate on the raw document map rather than re-serializing.
type Server struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Transport reports the server's effective transport, defaulting to stdio when
// the Type field is omitted (as it is for command-based servers).
func (s Server) Transport() string {
	if s.Type != "" {
		return s.Type
	}
	if s.URL != "" {
		return TransportHTTP
	}
	return TransportStdio
}

// Summary is a one-line description of where the server points, for listings.
func (s Server) Summary() string {
	if s.URL != "" {
		return s.URL
	}
	if s.Command == "" {
		return ""
	}
	if len(s.Args) == 0 {
		return s.Command
	}
	return s.Command + " " + strings.Join(s.Args, " ")
}

// State is a project server's approval state (meaningful only at project scope).
type State string

const (
	// StateNA is used for user/local servers: present means active.
	StateNA State = ""
	// StateEnabled means the project server is approved (auto-loaded).
	StateEnabled State = "enabled"
	// StateDisabled means the project server is explicitly blocked.
	StateDisabled State = "disabled"
	// StatePending means a project server exists but hasn't been approved yet.
	StatePending State = "pending"
)

// Entry is one server with its scope and (project scope only) approval state.
type Entry struct {
	Name   string
	Scope  settings.Scope
	Server Server
	State  State
}

// fileFor returns the file holding a scope's mcpServers map. user and local both
// live in ~/.claude.json (under different keys); project lives in <repo>/.mcp.json.
func fileFor(scope settings.Scope, home, repoRoot string) (string, error) {
	switch scope {
	case settings.User, settings.Local:
		if home == "" {
			return "", fmt.Errorf("cannot resolve home directory")
		}
		return filepath.Join(home, ".claude.json"), nil
	case settings.Project:
		if repoRoot == "" {
			return "", fmt.Errorf("project scope needs a git repository (run inside one)")
		}
		return filepath.Join(repoRoot, ".mcp.json"), nil
	}
	return "", fmt.Errorf("unknown scope %q", scope)
}

// serversMap returns the mcpServers map for the scope within doc, creating the
// nesting when create is true. Local scope descends into projects[repoRoot].
func serversMap(doc map[string]any, scope settings.Scope, repoRoot string, create bool) (map[string]any, bool) {
	switch scope {
	case settings.User, settings.Project:
		return childMap(doc, "mcpServers", create)
	case settings.Local:
		projects, ok := childMap(doc, "projects", create)
		if !ok {
			return nil, false
		}
		proj, ok := childMap(projects, repoRoot, create)
		if !ok {
			return nil, false
		}
		return childMap(proj, "mcpServers", create)
	}
	return nil, false
}

// childMap fetches m[key] as a nested object, optionally creating it.
func childMap(m map[string]any, key string, create bool) (map[string]any, bool) {
	if c, ok := m[key].(map[string]any); ok {
		return c, true
	}
	if !create {
		return nil, false
	}
	c := map[string]any{}
	m[key] = c
	return c, true
}

// rawServers loads the scope's server map as decoded JSON (nil when absent).
func rawServers(scope settings.Scope, home, repoRoot string) (map[string]any, error) {
	path, err := fileFor(scope, home, repoRoot)
	if err != nil {
		return nil, err
	}
	doc, err := load(path)
	if err != nil {
		return nil, err
	}
	servers, _ := serversMap(doc, scope, repoRoot, false)
	return servers, nil
}

// List gathers servers from every available scope. repoRoot may be "" (project
// and local are then skipped). Project servers carry their merged approval state.
func List(home, repoRoot string) ([]Entry, error) {
	scopes := []settings.Scope{settings.User}
	if repoRoot != "" {
		scopes = append(scopes, settings.Project, settings.Local)
	}
	var out []Entry
	for _, sc := range scopes {
		servers, err := rawServers(sc, home, repoRoot)
		if err != nil {
			return nil, err
		}
		for name, raw := range servers {
			srv, err := parseServer(raw)
			if err != nil {
				return nil, fmt.Errorf("server %q (%s): %w", name, sc, err)
			}
			out = append(out, Entry{Name: name, Scope: sc, Server: srv})
		}
	}
	if repoRoot != "" {
		state, err := projectStates(home, repoRoot)
		if err != nil {
			return nil, err
		}
		for i := range out {
			if out[i].Scope == settings.Project {
				out[i].State = state(out[i].Name)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return scopeRank(out[i].Scope) < scopeRank(out[j].Scope)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func scopeRank(s settings.Scope) int {
	for i, x := range settings.All {
		if x == s {
			return i
		}
	}
	return len(settings.All)
}

// Get returns the server stored under name at scope.
func Get(scope settings.Scope, home, repoRoot, name string) (Server, bool, error) {
	servers, err := rawServers(scope, home, repoRoot)
	if err != nil {
		return Server{}, false, err
	}
	raw, ok := servers[name]
	if !ok {
		return Server{}, false, nil
	}
	srv, err := parseServer(raw)
	return srv, true, err
}

// Add writes srv under name at the given scope, creating the file and nesting as
// needed. It overwrites an existing server of the same name (callers warn first).
func Add(scope settings.Scope, home, repoRoot, name string, srv Server) error {
	if name == "" {
		return fmt.Errorf("server name is required")
	}
	path, err := fileFor(scope, home, repoRoot)
	if err != nil {
		return err
	}
	doc, err := load(path)
	if err != nil {
		return err
	}
	servers, _ := serversMap(doc, scope, repoRoot, true)
	raw, err := srv.toRaw()
	if err != nil {
		return err
	}
	servers[name] = raw
	return save(path, doc)
}

// Remove deletes name from the scope. Reports whether something was removed.
func Remove(scope settings.Scope, home, repoRoot, name string) (bool, error) {
	path, err := fileFor(scope, home, repoRoot)
	if err != nil {
		return false, err
	}
	doc, err := load(path)
	if err != nil {
		return false, err
	}
	servers, ok := serversMap(doc, scope, repoRoot, false)
	if !ok {
		return false, nil
	}
	if _, exists := servers[name]; !exists {
		return false, nil
	}
	delete(servers, name)
	return true, save(path, doc)
}

// SetEnabled records a project server's approval at local scope (settings.local.
// json) — your machine's view, never committed. enabled=true approves it (adds to
// enabledMcpjsonServers, clears any disable); enabled=false blocks it.
func SetEnabled(home, repoRoot, name string, enabled bool) error {
	path, err := settings.Local.Path(home, repoRoot)
	if err != nil {
		return err
	}
	m, err := load(path)
	if err != nil {
		return err
	}
	add, remove := "enabledMcpjsonServers", "disabledMcpjsonServers"
	if !enabled {
		add, remove = remove, add
	}
	m[add] = addToList(stringList(m[add]), name)
	if rest := removeFromList(stringList(m[remove]), name); len(rest) > 0 {
		m[remove] = rest
	} else {
		delete(m, remove)
	}
	return save(path, m)
}

// projectStates reads the three settings.json approval lists in precedence order
// (user → project → local, most specific winning) and returns the effective
// state of a project server.
func projectStates(home, repoRoot string) (func(name string) State, error) {
	all := false
	enabled := map[string]bool{}
	disabled := map[string]bool{}
	for _, sc := range settings.All {
		path, err := sc.Path(home, repoRoot)
		if err != nil {
			continue // a scope we can't resolve here just doesn't contribute
		}
		m, err := load(path)
		if err != nil {
			return nil, err
		}
		if b, ok := m["enableAllProjectMcpServers"].(bool); ok {
			all = b
		}
		for _, n := range stringList(m["enabledMcpjsonServers"]) {
			enabled[n] = true
			delete(disabled, n)
		}
		for _, n := range stringList(m["disabledMcpjsonServers"]) {
			disabled[n] = true
			delete(enabled, n)
		}
	}
	return func(name string) State {
		switch {
		case disabled[name]:
			return StateDisabled
		case all || enabled[name]:
			return StateEnabled
		default:
			return StatePending
		}
	}, nil
}

// parseServer decodes a raw JSON server object into a Server.
func parseServer(raw any) (Server, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return Server{}, err
	}
	var s Server
	if err := json.Unmarshal(b, &s); err != nil {
		return Server{}, err
	}
	return s, nil
}

// toRaw renders a Server as a decoded JSON object so it merges into the document
// like the values already there (and drops zero fields via omitempty).
func (s Server) toRaw() (map[string]any, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func stringList(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func addToList(list []string, name string) []string {
	for _, x := range list {
		if x == name {
			return list
		}
	}
	return append(list, name)
}

func removeFromList(list []string, name string) []string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		if x != name {
			out = append(out, x)
		}
	}
	return out
}

// load reads a JSON object file, treating absent/empty as an empty object.
func load(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// save writes m as indented JSON, creating parent dirs as needed.
func save(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
