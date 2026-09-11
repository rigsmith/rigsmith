package commands

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/durable"
	"github.com/rigsmith/rigsmith/internal/agentrig/process"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/ghrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
	claudesession "github.com/rigsmith/rigsmith/internal/clauderig/session"
	"github.com/spf13/cobra"
)

const queueRequestLimit = 128 << 10

// queueSubmission is saved before admission. Retrying this file cannot observe
// another login, generate another event or refresh the original replay timestamp.
type queueSubmission struct {
	Checksum string
	Version  int
	Scope    string
	At       time.Time
	Identity service.Identity
	Request  queue.Request
}

type queueCommandDeps struct {
	resolve     func() (service.SyncRequest, error)
	identity    func() (service.Identity, error)
	private     func(context.Context, string) error
	supervise   func(context.Context) (context.Context, error)
	routingPath func() (string, error)
	hooksPath   func() (string, error)
}

// NewQueueCmd exposes a foreground workflow and an explicit local hook opt-in.
// It installs no worker service; ordinary sync remains the default.
func NewQueueCmd() *cobra.Command {
	return newQueueCmd(defaultQueueCommandDeps())
}

func defaultQueueCommandDeps() queueCommandDeps {
	return queueCommandDeps{
		resolve: func() (service.SyncRequest, error) {
			cfg, err := config.LoadOrDefault()
			if err != nil {
				return service.SyncRequest{}, err
			}
			stage, err := config.StagingDir()
			if err != nil {
				return service.SyncRequest{}, err
			}
			return service.SyncRequest{Config: cfg, Machine: config.Detect(machineName(cfg)), StagingDir: stage}, nil
		},
		identity: func() (service.Identity, error) {
			a, o, e, err := account.LiveIdentity()
			return service.Identity{AccountUUID: a, OrganizationUUID: o, Email: e}, err
		},
		routingPath: queueHookRoutingPath,
		hooksPath:   settingsPath,
		private:     ghrepo.EnsurePrivate,
		supervise: func(ctx context.Context) (context.Context, error) {
			executable, err := os.Executable()
			if err != nil {
				return nil, err
			}
			return process.WithSupervisor(ctx, executable, "__queue-supervisor"), nil
		},
	}
}

func newQueueCmd(deps queueCommandDeps) *cobra.Command {
	var dir string
	var profiles []string
	cmd := &cobra.Command{Use: "queue", Short: "Explicitly enqueue and run recoverable Claude syncs", Long: "Explicit queued sync workflow (v2 preview). Initialize after an ordinary sync,\nprepare a saved request (--session or --hook), enqueue it, then run a worker.\nUse queue sync for manual sync that acknowledges fully covered queued requests.\nUse enable-hooks to opt this machine into queued hooks and queue-aware sync.\nUse hook-status to inspect routing and disable-hooks after recovery/drain.\nUse the same --dir and --profile\nselection for every command; keep runtime and request files private.\nWorkers use Git credentials for private HTTPS GitHub/GitLab remotes.\nPrivacy checks use gh/glab or the matching provider token.\nUse hook to save and admit hook input; recover-hooks retries its saved inbox.\nStop producers and recover the inbox before draining. No background service is installed.", Args: cobra.NoArgs}
	cmd.PersistentFlags().StringVar(&dir, "dir", "", "private runtime directory (default ~/.clauderig/queue-runtime)")
	_ = cmd.MarkPersistentFlagDirname("dir")
	cmd.PersistentFlags().StringArrayVar(&profiles, "profile", nil, "explicit Desktop profile to include (repeatable; default none)")
	_ = cmd.RegisterFlagCompletionFunc("profile", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return engine.LocalProfileNames(), cobra.ShellCompDirectiveNoFileComp
	})
	open := func(ctx context.Context, create bool) (*service.QueueRuntime, error) {
		req, err := deps.resolve()
		if err != nil {
			return nil, err
		}
		root := dir
		if root == "" {
			d, err := config.Dir()
			if err != nil {
				return nil, err
			}
			root = filepath.Join(d, "queue-runtime")
		}
		if create {
			if _, err := deps.remote(ctx, req); err != nil {
				return nil, err
			}

			return service.CreateQueueRuntime(ctx, root, req, profiles)
		}
		return service.OpenQueueRuntime(ctx, root, req, profiles)
	}
	cmd.AddCommand(&cobra.Command{Use: "init", Short: "Create a private queue for the current sync configuration", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		_, err := open(c.Context(), true)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(c.OutOrStdout(), "Queue initialized. Run queue prepare to save a request; enable-hooks opts installed hooks into this queue.")
		return err
	}})
	var session, output string
	var flush, prepareHook bool
	var unknownIdentity bool
	prepare := &cobra.Command{Use: "prepare", Short: "Save a manual or hook request and its current account attribution", Long: "Save one new request to an exclusive private file before enqueueing.\nThe file pins this runtime, a new event ID, the timestamp and account identity.\nRetry enqueue with this same file; never rerun prepare for an uncertain enqueue.\nNo transcript bytes are read. --flush requests all changed transcript tails.\n--hook reads a bounded Stop/SessionEnd JSON payload from stdin instead of --session.\nStop records normal intent; SessionEnd records selected-transcript flush intent.\nWorkers fully capture requested sessions and subagents; unrelated plain transcripts\nkeep normal throttling unless all-flush is requested. Chunked tails always flush.\nInput must finish within 2 seconds and 128 KiB. It never falls back to all-flush.\nPreparation saves intent only: enqueue the saved file separately.\nAn unavailable account requires an explicit --unknown-identity choice.", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if output == "" {
			return fmt.Errorf("--output is required")
		}
		intent := queue.Flush{Mode: queue.Normal}
		if flush {
			intent.Mode = queue.All
		}
		var hookPayload queueHookPayload
		if prepareHook {
			var err error
			hookPayload, err = readQueueHook(c.Context(), c.InOrStdin(), 2*time.Second)
			if err != nil {
				return err
			}
			session = hookPayload.SessionID
			if hookPayload.Event == "SessionEnd" {
				intent = queue.Flush{Mode: queue.Selected, Paths: []string{hookPayload.TranscriptPath}}
			}
		}
		canonicalSession := claudesession.CanonicalID(strings.TrimSpace(session))
		if !utf8.ValidString(session) || strings.ContainsAny(session, "\x00\r\n") {
			return fmt.Errorf("a bounded --session identifier is required")
		}
		if err := validateQueueSessionID(canonicalSession); err != nil {
			return err
		}
		r, err := open(c.Context(), false)
		if err != nil {
			return err
		}
		if err = r.CheckRequestPath(output); err != nil {
			return err
		}
		if prepareHook {
			if err := r.ValidateHookTranscript(hookPayload.TranscriptPath, canonicalSession); err != nil {
				return err
			}
		}
		submission, err := newQueueSubmission(r, canonicalSession, intent, unknownIdentity, deps.identity)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(submission, "", "  ")
		if err != nil {
			return err
		}
		if err = writeQueueRequest(c.Context(), output, append(data, '\n')); err != nil {
			return err
		}
		out := c.OutOrStdout()
		if prepareHook {
			out = c.ErrOrStderr()
		}
		_, err = fmt.Fprintln(out, "Request saved. Enqueue this same file on every retry.")
		return err
	}}
	prepare.Flags().StringVar(&session, "session", "", "session identifier for this request")
	prepare.Flags().StringVarP(&output, "output", "o", "", "new private request file; never overwritten")
	prepare.Flags().BoolVar(&flush, "flush", false, "capture every changed transcript tail")
	prepare.Flags().BoolVar(&prepareHook, "hook", false, "read a bounded Stop/SessionEnd payload from stdin; save intent only")
	prepare.MarkFlagsMutuallyExclusive("hook", "session")
	prepare.MarkFlagsMutuallyExclusive("hook", "flush")
	prepare.Flags().BoolVar(&unknownIdentity, "unknown-identity", false, "explicitly record unknown account attribution")
	_ = prepare.MarkFlagFilename("output")
	_ = prepare.RegisterFlagCompletionFunc("session", completeSessionRef)
	cmd.AddCommand(prepare)
	addQueueHookProducerCommands(cmd, deps, open)
	addQueueHookRoutingCommands(cmd, deps, open, &profiles)
	cmd.AddCommand(&cobra.Command{Use: "enqueue <request-file>", Short: "Durably accept a saved request (safe to retry the same file)", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		r, err := open(c.Context(), false)
		if err != nil {
			return err
		}
		if err = r.CheckRequestPath(args[0]); err != nil {
			return err
		}
		release, err := queueRequestLease(c.Context(), args[0])
		if err != nil {
			return err
		}
		defer release()
		saved, err := loadQueueRequest(args[0])
		if err != nil {
			return err
		}
		submission := saved.submission

		if submission.Scope != r.ScopeID() {
			return queue.ErrBinding
		}
		// Confirm the producer file's durability before any queue admission. A
		// successfully read file alone is not a durable retry record.
		if err = saved.confirm(c.Context(), args[0]); err != nil {
			return err
		}
		event, err := r.Enqueue(c.Context(), submission.Identity, submission.Request, submission.At)
		if err != nil {
			return fmt.Errorf("enqueue failed; retain and retry the same request file: %w", err)
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(queueReceiptJSON{event.Generation, event.BatchID})
	}})
	cmd.AddCommand(&cobra.Command{Use: "status", Short: "Show unfinished batches and queue capacity as JSON", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		r, err := open(c.Context(), false)
		if err != nil {
			return err
		}
		jobs, err := r.Snapshot(c.Context())
		if err != nil {
			return err
		}
		capacity, err := r.Capacity(c.Context())
		if err != nil {
			return err
		}
		// Omit producer identities, paths and raw errors. These observations are
		// separate snapshots and may change while producers or a worker are active.

		rows := make([]queueBatchJSON, 0, len(jobs))
		for _, j := range jobs {
			rows = append(rows, queueBatchJSON{j.ID, j.Phase, j.Status, len(j.Events), j.Attempts, j.NotBefore, j.FailureCode})
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(queueStatusJSON{rows, queueCapacityOutput(capacity)})
	}})
	cmd.AddCommand(&cobra.Command{Use: "retry <batch-id>", ValidArgsFunction: cobra.NoFileCompletions, Short: "Unblock a repaired batch without discarding its saved progress", Long: "Explicitly unblock a batch after repairing its reported failure. Saved artifacts\nand attempts are retained. Pending timed retries keep their backoff. This never\nclears a staging process fence or redirects work to changed configuration.", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		id, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil || id == 0 {
			return fmt.Errorf("batch ID must be a positive integer")
		}
		r, err := open(c.Context(), false)
		if err != nil {
			return err
		}
		if err = r.RetryBlocked(c.Context(), id); err != nil {
			return err
		}
		_, err = fmt.Fprintln(c.OutOrStdout(), "Batch unblocked; run a worker or drain to retry.")
		return err
	}})
	var syncDryRun, syncFlush bool
	syncCmd := &cobra.Command{
		Use: "sync", Short: "Run a manual sync and acknowledge fully covered queued requests", Args: cobra.NoArgs,
		Long: "Run one supervised manual sync with the current account, then confirm the\n" +
			"published snapshot before acknowledging fully covered queued requests.\n" +
			"Other accounts, later requests and work without complete evidence stay queued.\n" +
			"Already-attempted or blocked work remains for the queue worker.\n" +
			"Use the same --dir and --profile selection as init; that selection must include\n" +
			"every local Desktop profile discovered by ordinary sync. Repair unreadable\n" +
			"profiles before retrying. Keep profile locations and runtime paths stable.\n\n" +
			"On Windows, provision a private runtime directory; inherited ACLs are not\n" +
			"validated or repaired. Do not use shared or other-user-writable runtime state.\n\n" +
			"Requires initialized shared staging/remote history and the existing private\n" +
			"remote checks, including for --dry-run. --flush includes all changed transcript\n" +
			"tails; this manual command does not read hook payloads from stdin or debounce.\n" +
			"An active queue batch can report busy; retry after it finishes. External merge\n" +
			"tools are disabled. The first interrupt lets this sync finish; a second cancels\n" +
			"and waits for supervised cleanup. This command does not drain the queue.",
		RunE: func(c *cobra.Command, _ []string) error {
			r, err := open(c.Context(), false)
			if err != nil {
				return err
			}
			req, err := deps.resolve()
			if err != nil {
				return err
			}
			remote, err := deps.remote(c.Context(), req)
			if err != nil {
				return err
			}
			ctx, err := deps.supervise(c.Context())
			if err != nil {
				return err
			}
			ctx, _, finish := queueSignalContext(ctx)
			defer finish()
			svc := applicationService(c.OutOrStdout())
			svc.ReadIdentity = deps.identity
			if err := r.CheckStartup(ctx, svc, service.QueueRuntimeInputs{Sync: req, Profiles: profiles, Remote: remote}); err != nil {
				return fmt.Errorf("manual queue sync startup: %w", err)
			}
			req.DryRun, req.AllowMergeTool, req.ResolveFlush = syncDryRun, false, nil
			req.Flush = service.FlushIntent{Mode: service.FlushNormal}
			if syncFlush {
				req.Flush.Mode = service.FlushAll
			}
			result, err := r.SyncWithCoverage(ctx, svc, req)
			if err != nil {
				return fmt.Errorf("manual sync did not confirm queue completion; inspect queue status before retrying: %w", err)
			}
			if syncDryRun {
				_, err = fmt.Fprintln(c.OutOrStdout(), "Dry run finished; queued requests were not acknowledged.")
			} else {
				_, err = fmt.Fprintf(c.OutOrStdout(), "Manual sync finished; %d queued requests acknowledged. Use queue status to inspect remaining work.\n", len(result.Acknowledged))
			}
			return err
		},
	}
	syncCmd.Flags().BoolVarP(&syncDryRun, "dry-run", "n", false, "stage and scan without publishing or acknowledging queued requests")
	syncCmd.Flags().BoolVar(&syncFlush, "flush", false, "include all changed transcript tails, regardless of the large-file throttle")
	cmd.AddCommand(syncCmd)
	for _, drain := range []bool{false, true} {
		name, short := "run", "Run one supervised foreground worker (Ctrl-C stops after the current batch)"
		if drain {
			name, short = "drain", "Process ready work and report any blocked or delayed backlog"
		}
		var maxBytes, maxStored int64
		worker := &cobra.Command{Use: name, Short: short, Long: short + ".\n\nRequires an initialized staging history sharing ancestry with the private remote.\nRun ordinary sync first if needed. Ctrl-C or SIGTERM requests a graceful stop;\na second signal cancels active work and waits for supervised cleanup.\nStop producers before draining; a stopped worker is not a completed drain.\nArchive byte limits apply separately to direct sealed files, not total disk use.", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			if maxBytes < 0 || maxStored < 0 {
				return fmt.Errorf("archive limits must be nonnegative")
			}
			r, err := open(c.Context(), false)
			if err != nil {
				return err
			}
			ctx, err := deps.supervise(c.Context())
			if err != nil {
				return err
			}
			ctx, stop, finish := queueSignalContext(ctx)
			defer finish()
			resolve := func(ctx context.Context) (service.QueueRuntimeInputs, error) {
				req, err := deps.resolve()
				if err != nil {
					return service.QueueRuntimeInputs{}, err
				}
				remote, err := deps.remote(ctx, req)
				if err != nil {
					return service.QueueRuntimeInputs{}, err
				}

				return service.QueueRuntimeInputs{Sync: req, Profiles: profiles, Remote: remote, MaxBytes: maxBytes, MaxStoredBytes: maxStored}, nil
			}
			svc := applicationService(c.ErrOrStderr())
			result, err := r.Run(ctx, r.Adapter(svc, resolve), queue.RunOptions{Drain: drain, Stop: stop, CheckStartup: func(ctx context.Context, _ queue.Binding) error {
				in, err := resolve(ctx)
				if err != nil {
					return err
				}
				return r.CheckStartup(ctx, svc, in)
			}, Observe: func(result queue.ExecutionResult, err error) {
				if err != nil {
					fmt.Fprintf(c.ErrOrStderr(), "Batch %d: %v\n", result.BatchID, err)
				}
			}})
			if err != nil {
				return err
			}
			if drain {
				select {
				case <-stop:
					return fmt.Errorf("drain interrupted; inspect queue status and drain again")
				default:
				}
			}
			_, err = fmt.Fprintf(c.OutOrStdout(), "Worker finished; %d batches completed.\n", result.CompletedBatches)
			return err
		}}
		worker.Flags().Int64Var(&maxBytes, "max-archive-bytes", 0, "per-archive size limit (0 uses 32 GiB)")
		worker.Flags().Int64Var(&maxStored, "max-stored-bytes", 0, "direct sealed bytes per store (0 unlimited; excludes scratch/recovery)")
		cmd.AddCommand(worker)
	}
	cmd.RunE = func(c *cobra.Command, _ []string) error {
		if !Interactive() {
			return c.Help()
		}
		var entries []climenu.Entry
		for _, child := range c.Commands() {
			switch child.Name() {
			case "init", "status", "sync", "run", "drain":
				entries = append(entries, climenu.Entry{Label: child.Name(), Desc: child.Short, Cmd: child})
			}
		}
		return climenu.RunMenu(c, c.CommandPath(), "Prepare/enqueue/retry and hook/inbox commands need input or options; use them directly.", entries)
	}
	return cmd
}

func writeQueueRequest(ctx context.Context, path string, data []byte) error {
	release, err := queueRequestLease(ctx, path)
	if err != nil {
		return err
	}
	defer release()

	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	info, statErr := f.Stat()
	if statErr != nil {
		return errors.Join(statErr, f.Close())
	}
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	err = errors.Join(writeErr, f.Close())
	if err != nil {
		return fmt.Errorf("request write failed; inspect the existing file before retrying: %w", err)
	}
	// Reflush the containing directory using the shared platform primitive.
	return (&savedQueueRequest{data: data, info: info}).confirm(ctx, path)
}

func newQueueSubmission(r *service.QueueRuntime, session string, intent queue.Flush, unknownIdentity bool, readIdentity func() (service.Identity, error)) (queueSubmission, error) {
	if err := validateQueueSessionID(session); err != nil {
		return queueSubmission{}, err
	}
	identity := service.Identity{}
	if !unknownIdentity {
		var err error
		identity, err = readIdentity()
		if err != nil {
			return queueSubmission{}, fmt.Errorf("read producer identity: %w", err)
		}
		if identity == (service.Identity{}) {
			return queueSubmission{}, fmt.Errorf("no account identity; use --unknown-identity to record explicit unknown attribution")
		}
	}
	for _, id := range []*string{&identity.AccountUUID, &identity.OrganizationUUID} {
		if *id == "" {
			continue
		}
		canonical := account.CanonicalUUID(*id)
		if canonical == "" {
			return queueSubmission{}, fmt.Errorf("invalid producer UUID")
		}
		*id = canonical
	}
	provenance, err := service.CaptureProvenance(identity)
	if err != nil {
		return queueSubmission{}, err
	}
	s := queueSubmission{Version: 1, Scope: r.ScopeID(), At: time.Now().UTC(), Identity: identity, Request: queue.Request{EventID: rand.Text(), SessionID: session, ProvenanceID: provenance, Flush: intent}}
	s.Checksum = queueRequestChecksum(s)
	return s, nil
}

func queueRequestChecksum(s queueSubmission) string {
	s.Checksum = ""
	data, _ := json.Marshal(s)
	return artifact.Key(data)
}

type savedQueueRequest struct {
	submission queueSubmission
	data       []byte
	info       os.FileInfo
}

func readQueueRequest(path string) (queueSubmission, error) {
	saved, err := loadQueueRequest(path)
	if err != nil {
		return queueSubmission{}, err
	}
	return saved.submission, nil
}

// Request leases serialize cooperating prepare/enqueue processes. Stable roots
// and no external file editing remain prerequisites, not a hostile-file sandbox.
func queueRequestLease(ctx context.Context, path string) (func(), error) {
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	lockDir := filepath.Join(parent, ".queue-request-"+artifact.Key([]byte(strings.ToLower(filepath.Base(path)))))
	_, release, err := storelock.Acquire(ctx, lockDir, service.StoreWait)
	return release, err
}

func (saved *savedQueueRequest) confirm(ctx context.Context, path string) error {
	current, err := loadQueueRequest(path)
	if err != nil {
		return err
	}
	if !os.SameFile(saved.info, current.info) || !bytes.Equal(saved.data, current.data) {
		return fmt.Errorf("saved request changed before admission; inspect and retry")
	}
	// Write exactly the validated bytes, never reopen the path inside Rewrite.
	return durable.Write(ctx, path, func(out *os.File) error { _, err := out.Write(saved.data); return err })
}

func loadQueueRequest(path string) (*savedQueueRequest, error) {
	var s queueSubmission
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > queueRequestLimit {
		return nil, fmt.Errorf("request must be a regular file at most 128 KiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("request must be regular")
	}

	if err := validateQueueRequestSingleLink(f); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, queueRequestLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > queueRequestLimit {
		return nil, fmt.Errorf("request exceeds 128 KiB")
	}
	if err := validateQueueRequestJSON(data); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&s); err != nil {
		return nil, err
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("request must contain exactly one JSON object")
	}
	if err := validateQueueSubmission(s); err != nil {
		return nil, err
	}
	return &savedQueueRequest{submission: s, data: data, info: info}, nil
}

func validateQueueSubmission(s queueSubmission) error {
	if s.Checksum != queueRequestChecksum(s) {
		return fmt.Errorf("saved request checksum mismatch")
	}
	if s.Version != 1 || s.Scope == "" || s.At.IsZero() || s.Request.EventID == "" || s.Request.SessionID == "" {
		return fmt.Errorf("invalid saved request")
	}
	if err := validateQueueSessionID(s.Request.SessionID); err != nil {
		return err
	}
	for _, id := range []string{s.Identity.AccountUUID, s.Identity.OrganizationUUID} {
		if id != "" && id != account.CanonicalUUID(id) {
			return fmt.Errorf("saved identity UUIDs must already be canonical")
		}
	}
	provenance, err := service.CaptureProvenance(s.Identity)
	if err != nil {
		return err
	}
	if s.Request.ProvenanceID != provenance {
		return queue.ErrBinding
	}
	return nil
}

// The first signal is a graceful stop. A second requests cancellation, but the
// process still waits for the adapter's child cleanup and fence contract.
func queueSignalContext(parent context.Context) (context.Context, <-chan struct{}, func()) {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	ctx, stop, finish := queueStopContext(parent, signals)
	return ctx, stop, func() { signal.Stop(signals); finish() }
}

func queueStopContext(parent context.Context, signals <-chan os.Signal) (context.Context, <-chan struct{}, func()) {
	ctx, cancel := context.WithCancel(parent)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			close(stop)
		case <-done:
			return
		case <-ctx.Done():
			return
		}
		select {
		case <-signals:
			cancel()
		case <-done:
		case <-ctx.Done():
		}
	}()
	return ctx, stop, func() { close(done); cancel() }
}

func (d queueCommandDeps) remote(ctx context.Context, req service.SyncRequest) (*commitartifact.GitTransport, error) {
	if req.Config == nil {
		return nil, queue.ErrBinding
	}
	remote, err := commitartifact.NewConfiguredGitTransport(commitartifact.GitTransportOptions{Remote: req.Config.Remote, Branch: "main"})
	if err != nil {
		return nil, fmt.Errorf("queued sync requires a supported HTTPS remote: %w", err)
	}
	if err = d.private(ctx, req.Config.Remote); err != nil {
		return nil, err
	}
	return remote, nil
}

type queueReceiptJSON struct {
	Generation uint64 `json:"generation"`
	Batch      uint64 `json:"batch"`
}
type queueBatchJSON struct {
	Batch       uint64       `json:"batch"`
	Phase       queue.Phase  `json:"phase"`
	Status      queue.Status `json:"status"`
	Events      int          `json:"events"`
	Attempts    uint64       `json:"attempts"`
	NotBefore   time.Time    `json:"notBefore"`
	FailureCode string       `json:"failureCode"`
}
type queueStatusJSON struct {
	Batches  []queueBatchJSON  `json:"batches"`
	Capacity queueCapacityJSON `json:"capacity"`
}
type queueCapacityJSON struct {
	PayloadBytes       int       `json:"payloadBytes"`
	StateLimit         int       `json:"stateLimit"`
	EnqueueLimit       int       `json:"enqueueLimit"`
	EnqueueHeadroom    int       `json:"enqueueHeadroom"`
	OutstandingBatches int       `json:"outstandingBatches"`
	BatchLimit         int       `json:"batchLimit"`
	PendingBatches     int       `json:"pendingBatches"`
	RunningBatches     int       `json:"runningBatches"`
	BlockedBatches     int       `json:"blockedBatches"`
	OutstandingEvents  int       `json:"outstandingEvents"`
	CompletedReceipts  int       `json:"completedReceipts"`
	CompletedBatches   int       `json:"completedBatches"`
	RetiredReceipts    uint64    `json:"retiredReceipts"`
	ReplayBefore       time.Time `json:"replayBefore"`
	Remedies           []string  `json:"remedies"`
}

func queueCapacityOutput(c queue.Capacity) queueCapacityJSON {
	return queueCapacityJSON{
		PayloadBytes:       c.PayloadBytes,
		StateLimit:         c.StateLimit,
		EnqueueLimit:       c.EnqueueLimit,
		EnqueueHeadroom:    c.EnqueueHeadroom,
		OutstandingBatches: c.OutstandingBatches,
		BatchLimit:         c.BatchLimit,
		PendingBatches:     c.PendingBatches,
		RunningBatches:     c.RunningBatches,
		BlockedBatches:     c.BlockedBatches,
		OutstandingEvents:  c.OutstandingEvents,
		CompletedReceipts:  c.CompletedReceipts,
		CompletedBatches:   c.CompletedBatches,
		RetiredReceipts:    c.RetiredReceipts,
		ReplayBefore:       c.ReplayBefore,
		Remedies:           c.Remedies,
	}
}

// validateQueueSessionID requires one literal native transcript name, using the
// same separator/dot/pattern restrictions as the native session mover.
func validateQueueSessionID(id string) error {
	encoded, _ := json.Marshal(id)
	if !utf8.ValidString(id) || strings.TrimSpace(id) == "" || len(encoded) > 4098 || strings.ContainsAny(id, "\x00\r\n") {
		return fmt.Errorf("a bounded --session identifier is required")
	}
	if id != claudesession.CanonicalID(strings.TrimSpace(id)) {
		return fmt.Errorf("session identifier must be trimmed and lowercase")
	}
	if strings.ContainsAny(id, `/\*?[]`) || id == "." || id == ".." {
		return fmt.Errorf("session must name one transcript, without separators or patterns")
	}
	return nil
}
