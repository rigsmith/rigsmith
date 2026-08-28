package bridge

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/sessions"
)

// promptsShown is how many prompts each end of a conversation contributes to
// the detail panel. Two is enough to recognise a session — what you opened it
// for and what you last asked — without turning the panel into a transcript
// viewer, which `peek show` already is.
const promptsShown = 2

// Prompt is one thing a person typed.
type Prompt struct {
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// SessionDetail is one session opened in the detail panel.
type SessionDetail struct {
	Session LibrarySession `json:"session"`
	First   []Prompt       `json:"first"`
	Last    []Prompt       `json:"last"`
	// Prompts is every human turn, so the panel can say how much sits between
	// the two ends rather than implying it is showing all of them.
	Prompts int `json:"prompts"`
	// Paths is every file this session occupies, keyed by store — what a delete
	// would actually remove.
	Paths map[string]string `json:"paths"`
	// ResumeCommand is the ready-to-run `cd … && claude --resume …`.
	ResumeCommand string `json:"resumeCommand"`
	// Resumable is false when no transcript sits in the live ~/.claude: that is
	// the only copy `claude --resume` and Claude Desktop can read, so both
	// resume paths are impossible until it is restored.
	Resumable bool `json:"resumable"`
	// Profiles are the clauderig-managed Desktop installs a session can be
	// opened in. The machine-wide install is deliberately absent — see Profiles.
	Profiles []string `json:"profiles"`
	// VSCode reports that the Claude Code extension is installed, so the window
	// offers the button only where it would work.
	VSCode bool   `json:"vscode"`
	Error  string `json:"error,omitempty"`
}

// Detail reads one session in full: its row, the ends of its conversation, and
// everything the actions below need to be offered honestly.
func (l *Library) Detail(ctx context.Context, id string) (SessionDetail, error) {
	var d SessionDetail
	row, _, err := l.find(id)
	if err != nil {
		return SessionDetail{Error: err.Error()}, nil
	}
	d.Session = toLibrarySession(row)
	d.Paths = row.Paths
	d.Resumable = row.CLILive
	d.ResumeCommand = resumeCommand(row)
	d.Profiles, _ = managedProfiles()
	d.VSCode = vscodeInstalled()

	if row.Path != "" {
		c, perr := sessions.Prompts(row.Path, promptsShown)
		if perr != nil {
			// A transcript we cannot read is worth saying so about; the row's
			// own facts are still worth showing.
			d.Error = perr.Error()
		}
		d.First, d.Last, d.Prompts = toPrompts(c.First), toPrompts(c.Last), c.Total
	}
	return d, nil
}

func toPrompts(ps []sessions.Prompt) []Prompt {
	out := make([]Prompt, 0, len(ps))
	for _, p := range ps {
		out = append(out, Prompt{Text: p.Text, At: p.At})
	}
	return out
}

// find locates one session by id, ignoring the window filters — the caller
// asked for it by name.
func (l *Library) find(id string) (sessions.Row, config.Machine, error) {
	cfg, err := config.LoadOrDefault()
	if err != nil {
		return sessions.Row{}, config.Machine{}, err
	}
	me := config.DetectFor(cfg)
	rows, _ := sessions.List(sessions.Options{
		Machine: me,
		Roots:   sessions.Roots(cfg, me, false, false),
		Targets: sessions.Targets(cfg, me, false, false),
		Scope:   sessions.Scope{Now: time.Now(), Me: me.Name, LiveInScope: true, Ledger: sessions.LoadLedger()},
		OnlyID:  id,
	})
	if len(rows) == 0 {
		return sessions.Row{}, me, fmt.Errorf("no session with id %q", id)
	}
	return rows[0], me, nil
}

// resumeCommand builds the command that reopens a session in a terminal, shell
// quoted, matching what `clauderig recent -l` prints.
func resumeCommand(row sessions.Row) string {
	if row.Cwd != "" {
		return "cd " + shQuote(row.Cwd) + " && claude --resume " + shQuote(row.ID)
	}
	return "claude --resume " + shQuote(row.ID)
}

// shQuote wraps a value in single quotes for a POSIX shell.
func shQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func managedProfiles() ([]string, error) {
	st, err := desktop.DefaultStore()
	if err != nil {
		return nil, err
	}
	profiles, err := st.List()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		names = append(names, p.Name)
	}
	return names, nil
}

// OpenDesktop opens a session in one named Claude Desktop profile.
//
// The write itself stays in the CLI: `desktop open` owns the deep link, the
// refusal when several profile windows are open (the OS would pick which one
// receives it, crossing an account boundary), and the check that the transcript
// is somewhere Desktop can read. This only names the profile and reports back.
func (l *Library) OpenDesktop(ctx context.Context, id, profile string) error {
	if strings.TrimSpace(profile) == "" {
		return errors.New("name which Desktop profile to open it in")
	}
	// Validated by membership rather than by pattern: the set of profiles is
	// known, so anything outside it is refused without having to guess what a
	// legal profile name looks like.
	names, err := managedProfiles()
	if err != nil {
		return err
	}
	known := false
	for _, n := range names {
		if n == profile {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("%q is not a clauderig-managed Desktop profile", profile)
	}
	if !idRule.MatchString(id) {
		return fmt.Errorf("invalid session id")
	}

	bin, err := resolveCLI()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "desktop", "open", profile, "--session", id)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// The CLI's own refusal is the useful message — "other profiles are
		// open" is guidance, not a failure of this button.
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// OpenTerminal launches a terminal already running the resume command.
//
// `claude --resume` is an interactive program: it cannot run inside this window,
// so the only honest options are to hand it a terminal or hand you the command.
// Terminal.app is the default because it is the one macOS always has; set
// CLAUDERIG_TERMINAL to the name of another application to use that instead.
// The Copy button remains the path that works with any terminal, multiplexer or
// remote host.
func (l *Library) OpenTerminal(ctx context.Context, id string) error {
	row, _, err := l.find(id)
	if err != nil {
		return err
	}
	if !row.CLILive {
		return errors.New("that session's transcript is not on this Mac — restore it first")
	}
	if runtime.GOOS != "darwin" {
		return errors.New("opening a terminal is macOS-only for now — use Copy command")
	}

	// A script rather than an argument: it survives quoting, and the terminal
	// is left sitting in the session's directory when claude exits.
	script := filepath.Join(os.TempDir(), "clauderig-resume-"+row.ID+".command")
	body := "#!/bin/sh\n" + resumeCommand(row) + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		return err
	}
	app := os.Getenv("CLAUDERIG_TERMINAL")
	if app == "" {
		app = "Terminal"
	}
	if out, err := exec.CommandContext(ctx, "open", "-a", app, script).CombinedOutput(); err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("could not open %s: %s", app, msg)
		}
		return err
	}
	return nil
}

// vscodeResumeURL is the deep link the Claude Code VS Code extension registers.
// Its URI handler switches on the path and reads `session` from the query:
//
//	case "/open": commands.executeCommand("claude-vscode.primaryEditor.open", session, prompt)
//
// Undocumented, so it is verified rather than trusted — [Library.OpenVSCode]
// checks the extension is installed before firing, and the failure mode if the
// shape ever changes is a no-op window rather than a wrong action.
func vscodeResumeURL(id string) string {
	return "vscode://anthropic.claude-code/open?session=" + url.QueryEscape(id)
}

// OpenVSCode resumes a session in VS Code.
//
// Two steps, in order: open the session's project folder so the window has the
// right workspace, then fire the extension's deep link to reopen the session
// inside it. The link alone would resume the conversation in whatever workspace
// happened to be focused, which is the wrong place to be editing from.
func (l *Library) OpenVSCode(ctx context.Context, id string) error {
	row, _, err := l.find(id)
	if err != nil {
		return err
	}
	if !row.CLILive {
		// Same constraint as the other resume paths: the extension reads
		// ~/.claude/projects, so a session only in the sync has nothing to open.
		return errors.New("that session's transcript is not on this Mac — restore it first")
	}
	if !idRule.MatchString(row.ID) {
		return errors.New("invalid session id")
	}
	if !vscodeInstalled() {
		return errors.New("the Claude Code extension for VS Code isn't installed")
	}

	// Best-effort: without the `code` CLI the deep link still resumes the
	// session, just in whichever workspace is already open.
	if cli, lerr := exec.LookPath("code"); lerr == nil && row.Cwd != "" {
		if info, serr := os.Stat(row.Cwd); serr == nil && info.IsDir() {
			if out, rerr := exec.CommandContext(ctx, cli, row.Cwd).CombinedOutput(); rerr != nil {
				return fmt.Errorf("could not open %s in VS Code: %s", row.Cwd, strings.TrimSpace(string(out)))
			}
		}
	}
	return openURL(ctx, vscodeResumeURL(row.ID))
}

// vscodeInstalled reports whether the Claude Code extension is present, so the
// window can say the extension is missing rather than firing a deep link at a
// scheme handler that will silently drop it.
func vscodeInstalled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	// Every VS Code-family editor keeps its extensions in the same layout, and
	// the directory name carries the version, so this matches any of them.
	for _, dir := range []string{".vscode", ".vscode-insiders", ".cursor", ".windsurf"} {
		matches, _ := filepath.Glob(filepath.Join(home, dir, "extensions", "anthropic.claude-code-*"))
		if len(matches) > 0 {
			return true
		}
	}
	return false
}

// openURL hands a URL to the desktop's scheme handler.
func openURL(ctx context.Context, u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", u)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", u)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// DeleteResult reports what a delete removed and, more importantly, what it did
// not.
type DeleteResult struct {
	Removed []string `json:"removed"`
	Failed  []string `json:"failed"`
	// Remaining names stores that still hold a copy. A session deleted here but
	// left in the sync comes back on the next restore, and a window that said
	// "deleted" without saying so would be lying by omission.
	Remaining []string `json:"remaining"`
	Error     string   `json:"error,omitempty"`
}

// Delete removes a session from the named stores only.
//
// The logic sits in internal/clauderig/sessions rather than behind the CLI,
// which is a deliberate exception to "shell out for writes". Every destructive
// verb in clauderig confirms interactively and refuses without a terminal, so a
// GUI cannot drive one — and the fix is not to add a --yes that would let any
// script delete transcripts silently. Instead the logic moved below both front
// ends, as `health` did, and each supplies its own confirmation: a prompt on
// the terminal, this window's dialog here.
func (l *Library) Delete(ctx context.Context, id string, stores []string) (DeleteResult, error) {
	var res DeleteResult
	row, me, err := l.find(id)
	if err != nil {
		return DeleteResult{Error: err.Error()}, nil
	}

	claudeHome := ""
	if cfg, cerr := config.LoadOrDefault(); cerr == nil {
		if loc, st := cfg.RootLocation("cli", me); st == pathmap.StatusResolved {
			claudeHome = loc
		}
	}

	d, err := sessions.Delete(row, stores, claudeHome)
	if err != nil {
		return DeleteResult{Error: err.Error()}, nil
	}
	res.Removed = d.Removed
	res.Remaining = d.Remaining
	for _, f := range d.Failed {
		res.Failed = append(res.Failed, f.Path+": "+f.Reason)
	}
	return res, nil
}
