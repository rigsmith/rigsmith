package hooks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/appserver"
	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
)

// CheckResult keeps non-current hooks visible without identifying them as ours.
type CheckResult struct {
	Ours         []appserver.HookInfo `json:"ours"`
	Lookalikes   []appserver.HookInfo `json:"lookalikes"`
	Foreign      []appserver.HookInfo `json:"foreign"`
	StillTrusted []appserver.HookInfo `json:"stillTrusted"`
	Unreported   []appserver.HookInfo `json:"unreported"`
	Incompatible []appserver.HookInfo `json:"incompatible"`
	Warnings     []string             `json:"warnings"`
	ScopeErrors  []string             `json:"scopeErrors"`
}

func emptyCheck() CheckResult {
	return CheckResult{Ours: []appserver.HookInfo{}, Lookalikes: []appserver.HookInfo{}, Foreign: []appserver.HookInfo{}, StillTrusted: []appserver.HookInfo{}, Unreported: []appserver.HookInfo{}, Incompatible: []appserver.HookInfo{}, Warnings: []string{}, ScopeErrors: []string{}}
}

type Refusal struct {
	appserver.HookInfo
	Reason   string `json:"reason"`
	NextStep string `json:"nextStep"`
}

type TrustResult struct {
	Trusted      []appserver.HookInfo `json:"trusted"`
	Already      []appserver.HookInfo `json:"already"`
	Foreign      []Refusal            `json:"foreign"`
	Lookalikes   []Refusal            `json:"lookalikes"`
	Outside      []Refusal            `json:"outside"`
	Refused      []Refusal            `json:"refused"`
	StillTrusted []Refusal            `json:"stillTrusted"`
	Incompatible []Refusal            `json:"incompatible"`
	Warnings     []string             `json:"warnings"`
}

func emptyTrust() TrustResult {
	return TrustResult{Trusted: []appserver.HookInfo{}, Already: []appserver.HookInfo{}, Foreign: []Refusal{}, Lookalikes: []Refusal{}, Outside: []Refusal{}, Refused: []Refusal{}, StillTrusted: []Refusal{}, Incompatible: []Refusal{}, Warnings: []string{}}
}

// Matches uses only the plans the requested scope's installer writes. Raw file
// validation is also mandatory: Codex may omit platform-specific fields.
func Matches(h appserver.HookInfo, scope Scope) bool {
	if h.ValidationIssue != "" || len(h.UnknownFields) != 0 || h.Async || h.StatusMessage != nil || h.AdditionalContextLimit != nil || (h.TimeoutSec != nil && *h.TimeoutSec != 600) {
		return false
	}
	return matchesCommand(h.EventName, h.Matcher, h.HandlerType, h.Command, scope.Plans())
}

func wireEvent(event Event) string {
	if event == "" {
		return ""
	}
	return strings.ToLower(string(event[:1])) + string(event[1:])
}

func matchesCommand(event, matcher, handlerType, command string, plans []Plan) bool {
	if handlerType != "command" {
		return false
	}
	for _, p := range plans {
		if event == wireEvent(p.Event) && command == p.Command && matcher == p.Matcher {
			return true
		}
	}
	return false
}

// Client is the small app-server seam used by both the CLI and regression fakes.
type Client interface {
	ListHooks(context.Context) ([]appserver.HookInfo, error)
	WriteConfig(context.Context, string, any) error
}

func Trust(ctx context.Context, home, cwd string, scope Scope) (TrustResult, error) {
	client, err := appserver.Start(ctx, home, cwd)
	if err != nil {
		return emptyTrust(), DisplayError(err)
	}
	defer client.Close()
	return TrustWithClient(ctx, client, home, scope)
}

// RevokeHint gives a manual repair; codexrig never revokes approval implicitly.
func RevokeHint(home string, h appserver.HookInfo) string {
	if h.TrustStatus == "managed" {
		return "Ask the Codex policy administrator to revoke this managed hook; codexrig cannot remove policy approval."
	}
	if h.Key == "" {
		return "Stop Codex and remove or disable the handler in " + h.SourcePath + ". Update Codex to identify its missing trust key before editing trust records; then restart and run `codexrig doctor`."
	}
	if home == "" {
		if value, ok := codexhome.Env(); ok {
			home = value
		} else {
			home, _ = codexhome.Default()
		}
	}
	table := strings.TrimSuffix(trustKeyPath(h.Key), ".trusted_hash")
	return "Stop Codex. In " + filepath.Join(home, "config.toml") + ", remove trusted_hash from [" + table + "]. Review or remove the handler in " + h.SourcePath + ", restart Codex, then run `codexrig doctor`."
}

// TrustWithClient validates the full file snapshot against the SAME Codex
// listing whose keys/hashes it writes. A changed file never gets a fresh hash.
func TrustWithClient(ctx context.Context, client Client, home string, scope Scope) (TrustResult, error) {
	out := emptyTrust()
	if err := scope.Validate(); err != nil {
		return invalidScope(scope, err)
	}
	snap := readSnapshot(scope)
	list, err := client.ListHooks(ctx)
	out.Warnings = clientWarnings(client)
	if err != nil {
		return out, DisplayError(err)
	}
	if err := scope.Validate(); err != nil {
		result, err := invalidScope(scope, err)
		result.Warnings = out.Warnings
		return result, err
	}
	seen := map[int]bool{}
	var failures []error
	for _, h := range list {
		if h.SourcePath == "" {
			r := Refusal{h, "Codex did not say where this hook came from", "Update Codex and retry."}
			if candidate(h) && h.Trusted() {
				r.NextStep = RevokeHint(home, h) + " Update Codex to report source paths."
				out.StillTrusted = append(out.StillTrusted, r)
			} else {
				out.Refused = append(out.Refused, r)
			}
			failures = append(failures, errors.New("missing hook source path"))
			continue
		}
		index, _ := snap.locate(h)
		if index >= 0 {
			h.CodexrigCandidate = snap.hooks[index].info.CodexrigCandidate
		}
		if !candidate(h) {
			out.Foreign = append(out.Foreign, Refusal{h, "not codexrig's", "Manage this hook in Codex itself."})
			continue
		}
		if !scope.addresses(h.SourcePath) {
			exact, issue := outsideAssessment(ctx, h, []Scope{scope})
			if h.Trusted() && !exact {
				h.ValidationIssue = issue
				r := Refusal{h, issue, RevokeHint(home, h)}
				if compatibilityIssue(issue) {
					r.NextStep = "Update codexrig before deciding whether to change trust."
					out.Incompatible = append(out.Incompatible, r)
				} else {
					out.StillTrusted = append(out.StillTrusted, r)
				}
				failures = append(failures, errors.New(Display(issue)))
			} else {
				reason := "outside requested scope"
				if exact {
					reason = "outside requested scope (exact codexrig hook)"
				}
				out.Outside = append(out.Outside, Refusal{h, reason, "No trust was changed here."})
			}
			continue
		}
		index, issue := snap.validate(h)
		if index >= 0 {
			seen[index] = true
		}
		if issue != "" {
			h.ValidationIssue = issue
			refusal := Refusal{h, issue, "Review this hook manually. Reinstall only recognized outdated codexrig commands; do not approve lookalikes."}
			if compatibilityIssue(issue) {
				refusal.NextStep = "Update codexrig before deciding whether to change trust."
				out.Incompatible = append(out.Incompatible, refusal)
			} else if h.Trusted() {
				refusal.NextStep = RevokeHint(home, h)
				out.StillTrusted = append(out.StillTrusted, refusal)
			} else {
				out.Lookalikes = append(out.Lookalikes, refusal)
			}
			failures = append(failures, fmt.Errorf("%s: %s", Display(h.EventName), Display(issue)))
			continue
		}
		if h.Trusted() {
			out.Already = append(out.Already, h)
			continue
		}
		if h.CurrentHash == "" || h.Key == "" {
			out.Refused = append(out.Refused, Refusal{h, "Codex reported no hash or trust key", "Update Codex and retry."})
			failures = append(failures, errors.New("missing hook hash/key"))
			continue
		}
		// Repeat the file/symlink check immediately before each write. This check
		// never substitutes newer command text or a newer CurrentHash.
		if !snap.unchanged() {
			out.Refused = append(out.Refused, Refusal{h, "hooks file changed before the write", "Review the hooks file and retry."})
			failures = append(failures, errors.New("hook file changed"))
			continue
		}
		if err := client.WriteConfig(ctx, trustKeyPath(h.Key), h.CurrentHash); err != nil {
			out.Refused = append(out.Refused, Refusal{h, "could not record trust: " + err.Error(), "Resolve the Codex error and retry."})
			failures = append(failures, DisplayError(err))
			continue
		}
		out.Trusted = append(out.Trusted, h)
		if !snap.unchanged() {
			out.Refused = append(out.Refused, Refusal{h, "the file changed during the config write; a trust record was written for the listed snapshot, but current trust is unknown", RevokeHint(home, h)})
			failures = append(failures, errors.New("hook file changed during config write; inspect the written trust record"))
		}
	}
	for i, local := range snap.hooks {
		if seen[i] {
			continue
		}
		h := local.info
		if candidate(h) {
			reason := h.ValidationIssue
			if reason == "" {
				reason = "Codex did not list this installed hook; trust is unknown"
			}
			out.Refused = append(out.Refused, Refusal{h, reason, "Review the file and Codex configuration before retrying."})
			failures = append(failures, errors.New(Display(reason)))
		} else {
			duplicate := false
			for _, f := range out.Foreign {
				if f.Command == h.Command && scope.addresses(f.SourcePath) {
					duplicate = true
				}
			}
			if !duplicate {
				out.Foreign = append(out.Foreign, Refusal{h, "not codexrig's", "Manage this hook in Codex itself."})
			}
		}
	}
	if snap.err != nil {
		failures = append(failures, fmt.Errorf("reading hooks: %s", Display(snap.err.Error())))
	}
	if len(failures) == 0 {
		return out, nil
	}
	messages := make([]string, len(failures))
	for i, err := range failures {
		messages[i] = Display(err.Error())
	}
	return out, errors.New(strings.Join(messages, "; "))
}

func invalidScope(scope Scope, err error) (TrustResult, error) {
	out := emptyTrust()
	out.Refused = append(out.Refused, Refusal{HookInfo: appserver.HookInfo{SourcePath: scope.path}, Reason: err.Error(), NextStep: "Remove the unsafe project symlink before retrying; no hook was classified or changed."})
	return out, DisplayError(fmt.Errorf("%s: %w", scope.path, err))
}

// Check validates each requested scope independently while using one listing.
func Check(ctx context.Context, home, cwd string, scopes ...Scope) (CheckResult, error) {
	client, err := appserver.Start(ctx, home, cwd)
	if err != nil {
		return emptyCheck(), DisplayError(err)
	}
	defer client.Close()
	return CheckWithClient(ctx, client, scopes...)
}

func CheckWithClient(ctx context.Context, client Client, scopes ...Scope) (CheckResult, error) {
	out := emptyCheck()
	if len(scopes) == 0 {
		return out, errors.New("explicit hook scopes required")
	}
	var snapshots []snapshot
	for _, scope := range scopes {
		if err := scope.Validate(); err != nil {
			out.ScopeErrors = append(out.ScopeErrors, scope.path+": "+err.Error())
			continue
		}
		snapshots = append(snapshots, readSnapshot(scope))
	}
	if len(snapshots) == 0 {
		return out, nil
	}
	list, err := client.ListHooks(ctx)
	out.Warnings = clientWarnings(client)
	if err != nil {
		return out, DisplayError(err)
	}
	handled := map[int]bool{}
	for _, snap := range snapshots {
		if err := snap.scope.Validate(); err != nil {
			out.ScopeErrors = append(out.ScopeErrors, snap.scope.path+": "+err.Error())
			continue
		}
		if snap.err != nil {
			out.ScopeErrors = append(out.ScopeErrors, snap.scope.path+": "+snap.err.Error())
			continue
		}
		seen := map[int]bool{}
		for li, h := range list {
			if !snap.scope.addresses(h.SourcePath) {
				continue
			}
			handled[li] = true
			i, issue := snap.validate(h)
			if i >= 0 {
				seen[i] = true
				h.CodexrigCandidate = snap.hooks[i].info.CodexrigCandidate
			}
			if issue != "" {
				h.ValidationIssue = issue
			}
			addClassified(&out, h, snap.scope)
		}
		for i, local := range snap.hooks {
			if !seen[i] && candidate(local.info) {
				out.Unreported = append(out.Unreported, local.info)
			}
		}
	}
	for i, h := range list {
		if handled[i] || !candidate(h) || !h.Trusted() {
			continue
		}
		exact, issue := outsideAssessment(ctx, h, scopes)
		if exact {
			continue
		}
		h.ValidationIssue = issue
		if compatibilityIssue(issue) {
			out.Incompatible = append(out.Incompatible, h)
		} else {
			out.StillTrusted = append(out.StillTrusted, h)
		}
	}
	return out, nil
}

const compatibilityPrefix = "codexrig doesn't recognise field(s) "

func compatibilityIssue(issue string) bool { return strings.HasPrefix(issue, compatibilityPrefix) }
func clientWarnings(client Client) []string {
	if c, ok := client.(interface{ HookWarnings() []string }); ok {
		return c.HookWarnings()
	}
	return []string{}
}

// Diagnose legacy approval from the listing, without parsing inline config.
// Only the actual global file or a repository root's project file can be exact.
func outsideAssessment(ctx context.Context, h appserver.HookInfo, scopes []Scope) (bool, string) {
	var known []Scope
	for _, scope := range scopes {
		if scope.Validate() != nil {
			continue
		}
		known = append(known, scope)
		for _, path := range scope.globalFiles {
			known = append(known, GlobalScope(filepath.Dir(path)))
		}
	}
	for _, scope := range known {
		if scope.Validate() == nil && scope.addresses(h.SourcePath) {
			snap := readSnapshot(scope)
			_, issue := snap.validate(h)
			return issue == "", issue
		}
	}
	if path, err := canonicalHookPath(h.SourcePath); err == nil && filepath.Base(path) == FileName && filepath.Base(filepath.Dir(path)) == ".codex" {
		if repo, err := gitrepo.Open(ctx, filepath.Dir(path)); err == nil {
			if root, err := repo.Toplevel(ctx); err == nil {
				scope := ProjectScope(root)
				if scope.Validate() == nil && scope.addresses(path) {
					snap := readSnapshot(scope)
					_, issue := snap.validate(h)
					return issue == "", issue
				}
			}
		}
	}
	return false, "trusted marker-bearing hook is not an exact codexrig hook in its own permitted hooks.json"
}

func candidate(h appserver.HookInfo) bool {
	return h.CodexrigCandidate || strings.Contains(h.Command, Marker)
}

func addClassified(out *CheckResult, h appserver.HookInfo, scope Scope) {
	if compatibilityIssue(h.ValidationIssue) {
		out.Incompatible = append(out.Incompatible, h)
	} else if Matches(h, scope) {
		out.Ours = append(out.Ours, h)
	} else if candidate(h) {
		if h.Trusted() {
			out.StillTrusted = append(out.StillTrusted, h)
		} else {
			out.Lookalikes = append(out.Lookalikes, h)
		}
	} else {
		out.Foreign = append(out.Foreign, h)
	}
}

func WriteTrustResult(w io.Writer, result TrustResult) {
	if w == nil {
		return
	}
	for _, h := range result.Trusted {
		fmt.Fprintf(w, "trusted %s from %s (matcher %s): %s\n", Display(h.EventName), Display(h.SourcePath), Display(h.Matcher), Display(h.Command))
	}
	for _, h := range result.Already {
		label := "already trusted"
		if !h.Enabled {
			label = "already trusted but disabled"
		}
		fmt.Fprintf(w, "%s %s from %s (matcher %s): %s\n", label, Display(h.EventName), Display(h.SourcePath), Display(h.Matcher), Display(h.Command))
	}
	for _, h := range result.Foreign {
		fmt.Fprintf(w, "left alone %s from %s: %s\n", Display(h.EventName), Display(h.SourcePath), Display(h.Command))
	}
	for _, h := range result.StillTrusted {
		fmt.Fprintf(w, "STILL TRUSTED: Codex %s this hook; codexrig did not approve it today\n  %s from %s (matcher %s): %s\n  %s\n  %s\n", runningWord(h), Display(h.EventName), Display(h.SourcePath), Display(h.Matcher), Display(h.Command), Display(h.Reason), Display(h.NextStep))
	}
	for _, r := range result.Incompatible {
		fmt.Fprintf(w, "cannot check %s from %s: %s\n  %s\n", Display(r.EventName), Display(r.SourcePath), Display(r.Command), Display(r.Reason))
	}
	for _, r := range result.Outside {
		if r.Reason != "outside requested scope (exact codexrig hook)" {
			fmt.Fprintf(w, "left alone outside requested scope %s from %s: %s\n", Display(r.EventName), Display(r.SourcePath), Display(r.Command))
		}
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(w, "Codex warning: %s\n", Display(warning))
	}
	for _, refusals := range [][]Refusal{result.Lookalikes, result.Refused} {
		for _, r := range refusals {
			if r.Command == "" && r.EventName == "" {
				fmt.Fprintf(w, "refused scope %s: %s; %s\n", Display(r.SourcePath), Display(r.Reason), Display(r.NextStep))
				continue
			}
			fmt.Fprintf(w, "refused %s from %s (matcher %s): %s\n  %s; %s\n", Display(r.EventName), Display(r.SourcePath), Display(r.Matcher), Display(r.Command), Display(r.Reason), Display(r.NextStep))
		}
	}
}

func runningWord(h Refusal) string {
	if h.Enabled {
		return "runs"
	}
	return "has approved but currently disables"
}

func trustKeyPath(key string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(key)
	return `hooks.state."` + escaped + `".trusted_hash`
}
