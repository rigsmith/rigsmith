package bridge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// cliName is the engine binary every write goes through.
const cliName = "clauderig"

// Action is a verb the UI is allowed to run. Actions are an allowlist mapped to
// fixed argv, never a string assembled from anything the frontend sends — the
// window is a webview, and "run this command" must not be a reachable primitive
// from inside it.
type Action string

const (
	ActionSync          Action = "sync"
	ActionPull          Action = "pull"
	ActionMerge         Action = "merge"
	ActionMaterialize   Action = "materialize"
	ActionAccountSwitch Action = "account-switch"
	ActionAccountAdd    Action = "account-add"
)

// idRule is the only shape an action argument may take: a session uuid or an
// account id. Deliberately narrow — no separators, no spaces, nothing a shell
// or a flag parser could reinterpret. An action taking a free-form string would
// undo the point of having an allowlist at all.
var idRule = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// spec is one runnable action: its fixed argv, and whether a caller-supplied id
// is appended.
type spec struct {
	args  []string
	takes bool // appends one validated id
}

// specs is the complete set of commands the UI may run.
var specs = map[Action]spec{
	ActionSync:          {args: []string{"sync"}},
	ActionPull:          {args: []string{"pull"}},
	ActionMerge:         {args: []string{"merge"}},
	ActionMaterialize:   {args: []string{"peek", "materialize"}, takes: true},
	ActionAccountSwitch: {args: []string{"account", "switch"}, takes: true},
	ActionAccountAdd:    {args: []string{"account", "add"}},
}

// Allowed reports whether a is a runnable action.
func Allowed(a Action) bool {
	_, ok := specs[a]
	return ok
}

// argvFor builds the exact command line for an action, rejecting an argument
// that doesn't match idRule or one supplied to an action that takes none.
func argvFor(a Action, arg string) ([]string, error) {
	sp, ok := specs[a]
	if !ok {
		return nil, fmt.Errorf("unknown action %q", a)
	}
	if !sp.takes {
		if arg != "" {
			return nil, fmt.Errorf("action %q takes no argument", a)
		}
		return sp.args, nil
	}
	if !idRule.MatchString(arg) {
		return nil, fmt.Errorf("invalid id for %q", a)
	}
	return append(append([]string{}, sp.args...), arg), nil
}

// Line is one line of an action's output.
type Line struct {
	// Stream is "stdout" or "stderr". The CLI writes progress to stdout and
	// nothing else, so stderr lines are worth rendering differently.
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// ansiRE matches the SGR escapes lipgloss emits. The CLI already drops colour
// when its output isn't a terminal, but a pipe is exactly the case where a
// missed escape would render as garbage in the drawer, so strip defensively.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// ErrBusy means an action is already running. Two syncs at once would race on
// the staging repo's index, and the second would fail confusingly.
var ErrBusy = errors.New("an action is already running")

// runner executes engine commands one at a time.
type runner struct {
	mu      sync.Mutex
	running bool

	// resolve finds the CLI; injected so tests don't need one installed.
	resolve func() (string, error)
}

func newRunner() *runner { return &runner{resolve: resolveCLI} }

// run executes the action, calling onLine for each line of output as it
// arrives. It returns ErrBusy when another action is in flight.
//
// Output is streamed rather than collected because a sync over a large history
// takes long enough that a silent window reads as a hang.
func (r *runner) run(ctx context.Context, a Action, arg string, onLine func(Line)) error {
	args, err := argvFor(a, arg)
	if err != nil {
		return err
	}

	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return ErrBusy
	}
	r.running = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()

	bin, rerr := r.resolve()
	if rerr != nil {
		return rerr
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s %s: %w", cliName, strings.Join(args, " "), err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); scanInto(stdout, "stdout", onLine) }()
	go func() { defer wg.Done(); scanInto(stderr, "stderr", onLine) }()
	wg.Wait() // both pipes drained before Wait, or output can be lost

	return cmd.Wait()
}

// busy reports whether an action is in flight.
func (r *runner) busy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

func scanInto(rd io.Reader, stream string, onLine func(Line)) {
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		text := strings.TrimRight(ansiRE.ReplaceAllString(sc.Text(), ""), " \t")
		if strings.TrimSpace(text) == "" {
			continue // the CLI pads its styled blocks; blank rows add nothing here
		}
		onLine(Line{Stream: stream, Text: text})
	}
}

// resolveCLI locates the engine binary: next to this executable first, then
// PATH.
//
// The sibling is preferred because a packaged app ships its own CLI inside the
// bundle, and that copy must win over whatever version happens to be on the
// user's PATH — a UI driving a different clauderig than the one it shipped with
// is a support problem nobody could diagnose from the outside.
func resolveCLI() (string, error) {
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		sibling := filepath.Join(filepath.Dir(exe), cliName)
		if isExecutableFile(sibling) {
			return sibling, nil
		}
	}
	if p, err := exec.LookPath(cliName); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("can't find the %s binary — install it, or put it next to this app", cliName)
}

func isExecutableFile(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}
