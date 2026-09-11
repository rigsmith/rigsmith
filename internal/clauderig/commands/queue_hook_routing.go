package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/rigsmith/rigsmith/internal/agentrig/durable"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/hooks"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
	"github.com/spf13/cobra"
)

// This descriptor stays outside synchronized settings. A disabled descriptor is
// retained so uncertain writes can be retried without deleting recovery state.
type queueHookRouting struct {
	Version         int
	Enabled         bool
	Runtime         string
	Inbox           string
	Profiles        []string
	Scope           string
	UnknownIdentity bool
	Checksum        string
}

func queueHookRoutingPath() (string, error) {
	dir, err := config.Dir()
	return filepath.Join(dir, "queue-hooks.json"), err
}

func routingChecksum(s queueHookRouting) string {
	s.Checksum = ""
	return privateQueueStateChecksum(s)
}

func loadHookRouting(path string) (queueHookRouting, error) {
	var s queueHookRouting
	if err := readPrivateQueueState(path, queueRequestLimit, "hook routing", &s); err != nil {
		return s, err
	}
	if s.Version != 1 || s.Scope == "" || !filepath.IsAbs(s.Runtime) || !filepath.IsAbs(s.Inbox) || s.Checksum != routingChecksum(s) {
		return s, fmt.Errorf("invalid hook routing descriptor or checksum")
	}
	return s, nil
}

func saveHookRouting(ctx context.Context, path string, s queueHookRouting) error {
	s.Checksum = routingChecksum(s)
	return writePrivateQueueState(ctx, path, s, queueRequestLimit, durable.Write)
}

func checkRoutingPaths(r *service.QueueRuntime, path, inbox string) error {
	for _, p := range []string{path, inbox} {
		if err := r.CheckRequestPath(p); err != nil {
			return err
		}
	}
	// Resolve existing ancestors, including the not-yet-created inbox's parent.
	resolved := func(p string) (string, error) {
		parent, err := filepath.EvalSymlinks(filepath.Dir(p))
		if err != nil {
			return "", err
		}
		target := filepath.Join(parent, filepath.Base(p))
		if _, err := os.Lstat(target); err == nil {
			return filepath.EvalSymlinks(target)
		} else if !os.IsNotExist(err) {
			return "", err
		}
		return target, nil
	}
	p, err := resolved(path)
	if err != nil {
		return err
	}
	root, err := resolved(inbox)
	if err != nil {
		return err
	}

	// Compare directory identities on the actual filesystem. Case folding would
	// reject distinct directories on case-sensitive volumes (including macOS),
	// while spelling alone would miss aliases on case-insensitive volumes.
	rootInfo, err := os.Stat(root)
	if os.IsNotExist(err) {
		return nil
	} // An absent inbox cannot contain the existing descriptor parent.
	if err != nil {
		return err
	}
	for ancestor := p; ; ancestor = filepath.Dir(ancestor) {
		info, err := os.Stat(ancestor)
		if err == nil && os.SameFile(rootInfo, info) {
			return fmt.Errorf("hook routing descriptor must be outside its inbox")
		}
		if err != nil && !(ancestor == p && os.IsNotExist(err)) {
			return err
		}
		if filepath.Dir(ancestor) == ancestor {
			break
		}
	}

	return nil
}

func addQueueHookRoutingCommands(parent *cobra.Command, deps queueCommandDeps, open func(context.Context, bool) (*service.QueueRuntime, error), profiles *[]string) {
	var inbox string
	var unknown bool
	enable := &cobra.Command{Use: "enable-hooks", Short: "Use the queue for this machine's installed sync hooks and manual sync", Args: cobra.NoArgs,
		Long: "Opt this machine into queued Stop/SessionEnd hooks and queue-aware manual sync.\nFirst stop Claude sessions, workers and other sync producers; install the standard\nsync hooks with clauderig hooks install and initialize the queue. Select every\nlocal Desktop profile, as required by queue sync. The portable hook commands\nstay unchanged; a private local descriptor pins --dir, --profile, --inbox and\nthe explicit unknown-identity choice. No worker or background service is installed.\nStart queue run separately to publish queued work. Windows callers must provide\nprivate directory ACLs; they are not validated or repaired.",
		RunE: func(c *cobra.Command, _ []string) error {
			path, err := deps.routingPath()
			if err != nil {
				return err
			}
			release, err := queueRequestLease(c.Context(), path)
			if err != nil {
				return err
			}
			defer release()
			old, err := loadHookRouting(path)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			r, err := open(c.Context(), false)
			if err != nil {
				return err
			}
			if err := r.CheckHookRouting(c.Context()); err != nil {
				return err
			}
			settings, err := deps.hooksPath()
			if err != nil {
				return err
			}
			if err := hooks.CheckSyncRouting(settings); err != nil {
				return err
			}
			root := inbox
			if root == "" {
				root = filepath.Join(filepath.Dir(path), "hook-inbox")
			}
			root, err = filepath.Abs(root)
			if err != nil {
				return err
			}
			if err := checkRoutingPaths(r, path, root); err != nil {
				return err
			}
			selected := slices.Clone(*profiles)
			slices.Sort(selected)
			wanted := queueHookRouting{Version: 1, Enabled: true, Runtime: r.Directory(), Inbox: root, Profiles: selected, Scope: r.ScopeID(), UnknownIdentity: unknown}
			if old.Enabled && routingChecksum(old) != routingChecksum(wanted) {
				return fmt.Errorf("hook routing is already enabled with different options; stop producers, recover, drain and disable it before changing options")
			}
			in := hookInbox{dir: root, save: durable.Write}
			_, unlock, err := storelock.Acquire(c.Context(), root, service.StoreWait)
			if err != nil {
				return err
			}
			defer unlock()
			state, err := in.load(r.ScopeID())
			if os.IsNotExist(err) && !old.Enabled {
				if err := os.Mkdir(root, 0700); err != nil {
					return fmt.Errorf("incomplete hook inbox; restore its journal before enabling: %s", sanitizeForDisplay(err.Error()))
				}
				state = hookInboxState{Version: 1, Scope: r.ScopeID(), Requests: []queueSubmission{}}
			} else if err != nil {
				return err
			}
			if err := in.persist(c.Context(), state); err != nil {
				return err
			}
			if err := deps.persistHookRouting(c.Context(), path, wanted); err != nil {
				return err
			}
			_, err = fmt.Fprintln(c.OutOrStdout(), "Queued hooks enabled on this machine. Run a queue worker to publish; manual sync now uses the queue-aware sync path.")
			return err
		}}
	enable.Flags().StringVar(&inbox, "inbox", "", "private hook inbox directory (default ~/.clauderig/hook-inbox)")
	_ = enable.MarkFlagDirname("inbox")
	enable.Flags().BoolVar(&unknown, "unknown-identity", false, "explicitly record unknown account attribution for every hook")
	parent.AddCommand(enable)
	parent.AddCommand(&cobra.Command{Use: "disable-hooks", Short: "Restore synchronous sync after the saved inbox and queue are empty", Args: cobra.NoArgs,
		Long: "Stop Claude sessions and all other producers first. Run recover-hooks with\nthe saved inbox and runtime, then drain the queue and stop every worker.\nThis command refuses pending inbox requests, queued work or an active worker.\nIt restores synchronous sync by retaining a disabled local descriptor; hook\nsettings and all recovery records remain intact. Use the saved --dir and --profile.",
		RunE: func(c *cobra.Command, _ []string) error {
			path, err := deps.routingPath()
			if err != nil {
				return err
			}
			release, err := queueRequestLease(c.Context(), path)
			if err != nil {
				return err
			}
			defer release()
			s, err := loadHookRouting(path)
			if os.IsNotExist(err) {
				_, err = fmt.Fprintln(c.OutOrStdout(), "Queued hooks are not enabled on this machine.")
				return err
			}
			if err != nil {
				return err
			}
			// Reflush an already-disabled descriptor after an uncertain disable write.
			if !s.Enabled {
				return deps.persistHookRouting(c.Context(), path, s)
			}
			r, err := open(c.Context(), false)
			if err != nil {
				return err
			}
			if r.Directory() != s.Runtime || r.ScopeID() != s.Scope {
				return queue.ErrBinding
			}
			if err := checkRoutingPaths(r, path, s.Inbox); err != nil {
				return err
			}
			in := hookInbox{dir: s.Inbox, save: durable.Write}
			_, unlock, err := storelock.Acquire(c.Context(), s.Inbox, service.StoreWait)
			if err != nil {
				return err
			}
			defer unlock()
			state, err := in.load(s.Scope)
			if err != nil {
				return err
			}
			if len(state.Requests) != 0 {
				return fmt.Errorf("hook inbox still has saved requests; run queue recover-hooks before draining and disabling")
			}
			if err := in.persist(c.Context(), state); err != nil {
				return err
			}
			if err := r.WhileIdle(c.Context(), func() error { s.Enabled = false; return deps.persistHookRouting(c.Context(), path, s) }); err != nil {
				return err
			}
			_, err = fmt.Fprintln(c.OutOrStdout(), "Queued hooks disabled. Sync and installed hooks are synchronous again; recovery state was preserved.")
			return err
		}})
	parent.AddCommand(&cobra.Command{Use: "hook-status", Short: "Show this machine's saved hook routing options", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		path, err := deps.routingPath()
		if err != nil {
			return err
		}
		s, err := loadHookRouting(path)
		if os.IsNotExist(err) {
			_, err = fmt.Fprintln(c.OutOrStdout(), "Queued hooks are not enabled on this machine.")
			return err
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(s)
	}})
}

// The routing lease spans hook dispatch, preventing rollback during admission.
// External settings/config/path edits and manual or direct queue producers
// must be stopped before toggling. No active-descriptor error falls back to sync.
func routeQueuedSync(c *cobra.Command, deps queueCommandDeps, dryRun, flush, hook bool) (bool, error) {
	path, err := deps.routingPath()
	if err != nil {
		return true, err
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return true, err
	}

	started := time.Now()
	initial, err := loadHookRouting(path)
	if err != nil {
		return true, err
	}
	if !initial.Enabled {
		return false, nil
	}
	if hook && flush {
		return true, fmt.Errorf("queued sync cannot combine --hook and --flush")
	}
	ctx := c.Context()
	var input []byte
	if !dryRun && (hook || flush) {
		terminal := false
		if f, ok := c.InOrStdin().(*os.File); ok {
			terminal = isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
		}
		if !terminal || hook {
			input, err = readQueueHookInput(ctx, c.InOrStdin(), 2*time.Second)
			if err != nil {
				return true, err
			}
		}
		if hook || len(input) != 0 {
			p, err := decodeQueueHook(input)
			if err != nil {
				return true, err
			}
			expected := "SessionEnd"
			if hook {
				expected = "Stop"
			}
			if p.Event != expected {
				return true, fmt.Errorf("hook event does not match the installed sync command")
			}
			input, err = json.Marshal(map[string]string{"hook_event_name": p.Event, "session_id": p.SessionID, "transcript_path": p.TranscriptPath})
			if err != nil {
				return true, err
			}
			// The complete hook operation, including input and routing contention, has
			// one budget. Manual sync (including an empty-input --flush) keeps its caller context.
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, started.Add(10*time.Second))
			defer cancel()
		}
	}
	release, err := queueRequestLease(ctx, path)
	if err != nil {
		return true, err
	}
	held := true
	defer func() {
		if held {
			release()
		}
	}()
	s, err := loadHookRouting(path)
	if err != nil {
		return true, err
	}
	if !s.Enabled || s.Checksum != initial.Checksum {
		return true, fmt.Errorf("hook routing changed during invocation; inspect status before retrying")
	}
	req, err := deps.resolve()
	if err != nil {
		return true, err
	}
	r, err := service.OpenQueueRuntime(ctx, s.Runtime, req, s.Profiles)
	if err != nil {
		return true, err
	}
	if r.ScopeID() != s.Scope {
		return true, queue.ErrBinding
	}
	if err := checkRoutingPaths(r, path, s.Inbox); err != nil {
		return true, err
	}
	// Reflush before use: a readable descriptor may follow an uncertain rename.
	if err := deps.persistHookRouting(ctx, path, s); err != nil {
		return true, err
	}
	args := []string{"--dir", s.Runtime}
	for _, profile := range s.Profiles {
		args = append(args, "--profile", profile)
	}

	if _, err := (hookInbox{dir: s.Inbox}).load(s.Scope); err != nil {
		return true, err
	}
	if len(input) != 0 {
		args = append(args, "hook", "--inbox", s.Inbox)
		if s.UnknownIdentity {
			args = append(args, "--unknown-identity")
		}
	}
	if len(input) == 0 {
		args = append(args, "sync")
		if dryRun {
			args = append(args, "--dry-run")
		}
		if flush {
			args = append(args, "--flush")
		}
	}
	// Manual sync takes worker/staging ownership itself. Do not hold the routing
	// lease through a long publication: hook producers must remain able to enqueue.
	// As for direct queue sync, stop manual producers before toggling routing.
	if len(input) == 0 {
		release()
		held = false
	}
	child := newQueueCmd(deps)
	child.SilenceErrors, child.SilenceUsage = true, true
	child.SetOut(c.OutOrStdout())
	child.SetErr(c.ErrOrStderr())
	child.SetIn(bytes.NewReader(input))
	child.SetArgs(args)
	return true, child.ExecuteContext(ctx)
}

// The seam exposes an uncertain descriptor result without changing filesystem
// operations or journal behavior in production.
func (d queueCommandDeps) persistHookRouting(ctx context.Context, path string, state queueHookRouting) error {
	if d.saveRouting != nil {
		return d.saveRouting(ctx, path, state)
	}
	return saveHookRouting(ctx, path, state)
}
