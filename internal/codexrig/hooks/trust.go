package hooks

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rigsmith/rigsmith/internal/codexrig/appserver"
)

// Scope identifies exactly one hooks file, independently of Codex's unstable
// Source enum. Construct it with GlobalScope or ProjectScope; a zero scope
// matches nothing.
type Scope struct {
	path string
	name string
}

func GlobalScope(home string) Scope { return hookScope(home, "global") }
func ProjectScope(repo string) Scope {
	if repo == "" {
		return Scope{}
	}
	return hookScope(filepath.Join(repo, ".codex"), "project")
}

func hookScope(dir, name string) Scope {
	if dir == "" {
		return Scope{}
	}
	return Scope{path: filepath.Join(dir, FileName), name: name}
}

// Contains resolves symlinks (including macOS /var aliases) and accepts Windows
// separators. Only the requested file is eligible, never adjacent hook files.
func (s Scope) Contains(source string) bool {
	return s.path != "" && source != "" && normalizeHookPath(s.path) == normalizeHookPath(source)
}

func normalizeHookPath(path string) string {
	path = filepath.Clean(filepath.FromSlash(strings.ReplaceAll(path, `\`, "/")))
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

// CheckResult keeps foreign commands visible without identifying them as ours.
type CheckResult struct {
	Ours       []appserver.HookInfo `json:"ours"`
	Lookalikes []appserver.HookInfo `json:"lookalikes"`
	Foreign    []appserver.HookInfo `json:"foreign"`
}

// Refusal includes the exact command Codex reported and how to proceed.
type Refusal struct {
	appserver.HookInfo
	Reason   string `json:"reason"`
	NextStep string `json:"nextStep"`
}

type TrustResult struct {
	Trusted    []appserver.HookInfo `json:"trusted"`
	Already    []appserver.HookInfo `json:"already"`
	Foreign    []Refusal            `json:"foreign"`
	Lookalikes []Refusal            `json:"lookalikes"`
	Outside    []Refusal            `json:"outside"`
	Refused    []Refusal            `json:"refused"`
}

// Matches compares the command and its event against today's installer plans.
// A guard matcher is user-selectable and is data, not shell command text. Old
// commands and commands from another machine are deliberately not grandfathered
// in: reinstall first. The wire event is camelCase, unlike the file event.
func Matches(h appserver.HookInfo) bool {
	return matchesCommand(h.EventName, h.Matcher, h.HandlerType, h.Command)
}

func wireEvent(event Event) string {
	if event == "" {
		return ""
	}
	return strings.ToLower(string(event[:1])) + string(event[1:])
}

func matchesCommand(event, matcher, handlerType, command string) bool {
	if handlerType != "command" {
		return false
	}
	plans := append(SyncPlans(), GuardPlans(matcher)...)
	for _, p := range plans {
		if event == wireEvent(p.Event) && command == p.Command && matcher == p.Matcher {
			return true
		}
	}
	return false
}

type trustClient interface {
	ListHooks(context.Context) ([]appserver.HookInfo, error)
	WriteConfig(context.Context, string, any) error
}

// Trust records only exact current codexrig commands in the explicit scope.
// cwd controls discovery, not authorization: Codex may list both scopes there.
func Trust(ctx context.Context, home, cwd string, scope Scope) (TrustResult, error) {
	client, err := appserver.Start(ctx, home, cwd)
	if err != nil {
		return TrustResult{}, err
	}
	defer client.Close()
	return trust(ctx, client, scope)
}

func trust(ctx context.Context, client trustClient, scope Scope) (TrustResult, error) {
	var out TrustResult
	list, err := client.ListHooks(ctx)
	if err != nil {
		return out, err
	}
	for _, h := range list {
		if h.SourcePath == "" {
			out.Refused = append(out.Refused, Refusal{h, "Codex did not say where this hook came from", "Update Codex and retry; no trust was recorded for this hook."})
			continue
		}
		if !Matches(h) && !strings.Contains(h.Command, Marker) {
			out.Foreign = append(out.Foreign, Refusal{h, "not codexrig's", "Manage this hook in Codex itself."})
			continue
		}
		if !scope.Contains(h.SourcePath) {
			out.Outside = append(out.Outside, Refusal{h, "outside requested scope", "Use the trust command for that scope; review foreign hooks in Codex itself."})
			continue
		}
		if !Matches(h) {
			out.Lookalikes = append(out.Lookalikes, Refusal{h, "lookalike or out of date", "For an outdated codexrig hook, run `codexrig " + scope.name + " install`, then trust again. Review lookalikes in Codex itself."})
			continue
		}
		if h.Trusted() {
			out.Already = append(out.Already, h)
			continue
		}
		if h.CurrentHash == "" || h.Key == "" {
			out.Refused = append(out.Refused, Refusal{h, "Codex reported no hash or trust key", "Update Codex and retry; no trust was recorded for this hook."})
			continue
		}
		// Command, key and hash come from the SAME listing. Never reread the
		// file or fetch a newer hash after validating this command. A later file
		// change must not receive its new hash from this approval.
		if err := client.WriteConfig(ctx, trustKeyPath(h.Key), h.CurrentHash); err != nil {
			out.Refused = append(out.Refused, Refusal{h, "could not record trust: " + err.Error(), "Retry the trust command after resolving the Codex error."})
			return out, fmt.Errorf("recording trust for %s: %w", h.EventName, err)
		}
		out.Trusted = append(out.Trusted, h)
	}
	if len(out.Trusted)+len(out.Already) == 0 {
		return out, fmt.Errorf("no current codexrig hooks could be trusted in the requested scope; run `codexrig %s install`, then trust again; review foreign hooks in Codex itself", scope.name)
	}
	return out, nil
}

// Check uses exactly the same identity check as Trust, without writing anything.
// Callers classify both lists by Scope.Contains before reporting on a scope.
func Check(ctx context.Context, home, cwd string) (CheckResult, error) {
	client, err := appserver.Start(ctx, home, cwd)
	if err != nil {
		return CheckResult{}, err
	}
	defer client.Close()
	list, err := client.ListHooks(ctx)
	if err != nil {
		return CheckResult{}, err
	}
	return classify(list), nil
}

func classify(list []appserver.HookInfo) CheckResult {
	var out CheckResult
	for _, h := range list {
		if Matches(h) {
			out.Ours = append(out.Ours, h)
		} else if strings.Contains(h.Command, Marker) {
			out.Lookalikes = append(out.Lookalikes, h)
		} else {
			out.Foreign = append(out.Foreign, h)
		}
	}
	return out
}

// WriteTrustResult is shared by trust and doctor --fix, including partial writes.
func WriteTrustResult(w io.Writer, result TrustResult) {
	if w == nil {
		return
	}
	for _, h := range result.Trusted {
		fmt.Fprintf(w, "trusted %s from %s: %s\n", h.EventName, h.SourcePath, h.Command)
	}
	for _, h := range result.Already {
		fmt.Fprintf(w, "already trusted %s from %s: %s\n", h.EventName, h.SourcePath, h.Command)
	}
	for _, h := range result.Foreign {
		fmt.Fprintf(w, "left alone %s from %s: %s\n  %s; %s\n", h.EventName, h.SourcePath, h.Command, h.Reason, h.NextStep)
	}
	for _, refusals := range [][]Refusal{result.Lookalikes, result.Outside, result.Refused} {
		for _, r := range refusals {
			fmt.Fprintf(w, "refused %s from %s: %s\n  %s; %s\n", r.EventName, r.SourcePath, r.Command, r.Reason, r.NextStep)
		}
	}
}

// trustKeyPath quotes Codex's key as one TOML basic-string segment. Backslashes
// must be escaped before quotes so neither can redirect the config write.
func trustKeyPath(key string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(key)
	return `hooks.state."` + escaped + `".trusted_hash`
}
