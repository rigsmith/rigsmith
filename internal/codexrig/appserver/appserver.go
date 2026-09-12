// Package appserver is a minimal JSON-RPC client for `codex app-server`, used
// for the few things codexrig should ask Codex rather than work out for itself.
//
// The rule for what belongs here: if the answer is Codex's opinion, ask Codex.
// A hook's trust hash is the clearest case — it is a hash of a file computed the
// way Codex computes it, and a tool that recomputed it would be asserting what
// Codex thinks rather than reading it. Writing config through Codex is the same
// argument from the other side: Codex owns config.toml's formatting, and having
// it make the edit preserves whatever it preserves instead of putting a TOML
// round trip through the user's live file.
//
// Deliberately small. This is not an SDK, and the moment it needs streaming,
// approvals or a session it has outgrown its reason to exist.
package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// startTimeout bounds the handshake. An app-server that has not answered
// initialize by then is not going to.
const startTimeout = 20 * time.Second

// Client is a running app-server.
type Client struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	mu   sync.Mutex
	next int
	// dead is set once a reply missed its deadline. The reader goroutine may
	// still deliver that reply later, into a buffer nobody drains, and every
	// later exchange would be reading one message behind — so a client that
	// has timed out once refuses further calls rather than answer wrongly.
	dead error
}

// Available reports whether the Codex CLI is on PATH at all.
func Available() bool {
	_, err := exec.LookPath("codex")
	return err == nil
}

// Start launches an app-server against a Codex home, with cwd as the working
// directory it resolves project-scoped configuration from.
//
// home may be empty, meaning the machine's own. cwd may be empty, meaning this
// process's — and it matters: the app-server reads project hooks relative to its
// OWN working directory and ignores a cwd passed as a parameter, which is the
// kind of thing only a live probe tells you.
func Start(ctx context.Context, home, cwd string) (*Client, error) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return nil, errors.New("`codex` not found on PATH")
	}
	cmd := exec.CommandContext(ctx, bin, "app-server", "--listen", "stdio://")
	cmd.Env = os.Environ()
	if home != "" {
		cmd.Env = append(cmd.Env, "CODEX_HOME="+home)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}
	// Its diagnostics are not ours to print; a failed call reports itself.
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{cmd: cmd, in: stdin, out: bufio.NewReaderSize(stdout, 1<<20)}

	if _, err := c.Call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{"name": "codexrig", "title": "codexRig", "version": "1"},
	}); err != nil {
		c.Close()
		return nil, fmt.Errorf("codex app-server did not start: %w", err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Close shuts the server down.
func (c *Client) Close() {
	if c == nil || c.cmd == nil {
		return
	}
	_ = c.in.Close()
	done := make(chan struct{})
	go func() { _ = c.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}
}

// Call sends a request and returns its result.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	// The lock covers the whole exchange, not just the id. The stream is one
	// pipe: two callers writing and reading it at once each consume the
	// other's reply and discard it as "not mine", and the other caller waits
	// for an answer that has already gone by.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead != nil {
		return nil, fmt.Errorf("codex app-server client is out of step after an earlier timeout (%v); close it and start another", c.dead)
	}
	c.next++
	id := c.next

	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := c.in.Write(append(b, '\n')); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(startTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	res, err := c.await(id, method, deadline)
	var refused *serverError
	if errors.As(err, &refused) {
		return nil, err
	}
	if err != nil {
		// Whichever way the wait failed — the server closed, a line never
		// finished, or lines kept coming and none was the answer — the answer
		// may yet arrive after this caller has gone, into a buffer nobody
		// drains, and the next exchange would read it as its own. One place
		// retires the client for all of them.
		c.dead = err
		return nil, err
	}
	return res, nil
}

// await reads replies until the one for id, or the deadline.
func (c *Client) await(id int, method string, deadline time.Time) (json.RawMessage, error) {
	for time.Now().Before(deadline) {
		line, err := c.readLine(time.Until(deadline))
		if err != nil {
			return nil, fmt.Errorf("codex app-server closed: %w", err)
		}
		var msg struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		// Notifications and server-to-client requests share this stream. Only
		// our own id is an answer; ignoring the rest is what keeps this a
		// client rather than a protocol implementation.
		if msg.ID == nil || *msg.ID != id {
			continue
		}
		if msg.Error != nil {
			return nil, &serverError{fmt.Errorf("%s: %s", method, msg.Error.Message)}
		}
		return msg.Result, nil
	}
	return nil, fmt.Errorf("%s: no answer from codex app-server before the deadline", method)
}

// serverError is an answer — the server said no — and leaves the client in step.
type serverError struct{ error }

var errDeadline = errors.New("no reply before the deadline")

// readLine is c.out.ReadString with a deadline. A line that arrives after the
// deadline is dropped by the buffered channel; the client is then out of step
// with the server and the caller should Close it.
func (c *Client) readLine(within time.Duration) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		l, e := c.out.ReadString('\n')
		ch <- result{l, e}
	}()
	select {
	case r := <-ch:
		return r.line, r.err
	case <-time.After(within):
		return "", errDeadline
	}
}

func (c *Client) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return err
	}
	_, err = c.in.Write(append(b, '\n'))
	return err
}

// HookInfo is one hook as Codex sees it.
type HookInfo struct {
	Key         string `json:"key"`
	EventName   string `json:"eventName"`
	HandlerType string `json:"handlerType"`
	Command     string `json:"command"`
	Matcher     string `json:"matcher"`
	SourcePath  string `json:"sourcePath"`
	Source      string `json:"source"`
	CurrentHash string `json:"currentHash"`
	TrustStatus string `json:"trustStatus"`
	Enabled     bool   `json:"enabled"`
}

// Trusted reports whether Codex will run this hook as it stands.
func (h HookInfo) Trusted() bool { return h.TrustStatus == "trusted" || h.TrustStatus == "managed" }

// ListHooks reports every hook Codex can see from its working directory,
// together with why it will or will not run.
func (c *Client) ListHooks(ctx context.Context) ([]HookInfo, error) {
	raw, err := c.Call(ctx, "hooks/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var res struct {
		Data []struct {
			Cwd      string     `json:"cwd"`
			Hooks    []HookInfo `json:"hooks"`
			Warnings []string   `json:"warnings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	var out []HookInfo
	var warnings []string
	for _, d := range res.Data {
		out = append(out, d.Hooks...)
		warnings = append(warnings, d.Warnings...)
	}
	if len(out) == 0 && len(warnings) > 0 {
		// A hooks file Codex could not parse is a silent no-op otherwise:
		// nothing errors, nothing runs. Surfacing the warning is the only way
		// anybody finds out.
		return nil, fmt.Errorf("codex could not use the hooks file: %s", strings.Join(warnings, "; "))
	}
	return out, nil
}

// WriteConfig sets one value in Codex's own config, through Codex.
func (c *Client) WriteConfig(ctx context.Context, keyPath string, value any) error {
	_, err := c.Call(ctx, "config/value/write", map[string]any{
		"keyPath": keyPath, "value": value, "mergeStrategy": "upsert",
	})
	return err
}
