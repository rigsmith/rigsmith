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
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/ghrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
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
	resolve   func() (service.SyncRequest, error)
	identity  func() (service.Identity, error)
	private   func(context.Context, string) error
	supervise func(context.Context) (context.Context, error)
}

// NewQueueCmd exposes an explicit foreground workflow. It installs no hook or
// service; ordinary sync remains synchronous.
func NewQueueCmd() *cobra.Command {
	return newQueueCmd(queueCommandDeps{
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
		private: ghrepo.EnsurePrivate,
		supervise: func(ctx context.Context) (context.Context, error) {
			executable, err := os.Executable()
			if err != nil {
				return nil, err
			}
			return process.WithSupervisor(ctx, executable, "__queue-supervisor"), nil
		},
	})
}

func newQueueCmd(deps queueCommandDeps) *cobra.Command {
	var dir string
	var profiles []string
	cmd := &cobra.Command{Use: "queue", Short: "Explicitly enqueue and run recoverable Claude syncs", Long: "Explicit queued sync workflow (v2 preview). Initialize after an ordinary sync,\nprepare a saved request, enqueue it, then run a foreground worker or drain.\nHooks and ordinary sync remain synchronous. Use the same --dir and --profile\nselection for every command; keep runtime and request files private.\nWorkers use existing Git/gh authentication and require an HTTPS private remote.\nStop producers before draining. No background service is installed.", Args: cobra.NoArgs}
	cmd.PersistentFlags().StringVar(&dir, "dir", "", "private runtime directory (default ~/.clauderig/queue-runtime)")
	_ = cmd.MarkPersistentFlagDirname("dir")
	cmd.PersistentFlags().StringArrayVar(&profiles, "profile", nil, "explicit Desktop profile to include (repeatable; default none)")
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
			if err := deps.private(ctx, req.Config.Remote); err != nil {
				return nil, err
			}
			if _, err := commitartifact.NewConfiguredGitTransport(commitartifact.GitTransportOptions{Remote: req.Config.Remote, Branch: "main"}); err != nil {
				return nil, fmt.Errorf("queued sync requires a supported HTTPS remote: %w", err)
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
		_, err = fmt.Fprintln(c.OutOrStdout(), "Queue initialized. Run queue prepare to save a request; hooks remain synchronous.")
		return err
	}})
	var session, output string
	var flush bool
	var unknown bool
	prepare := &cobra.Command{Use: "prepare", Short: "Save a new request and its current account attribution", Long: "Save one new request to an exclusive private file before enqueueing.\nThe file pins this runtime, a new event ID, the timestamp and account identity.\nRetry enqueue with this same file; never rerun prepare for an uncertain enqueue.\nNo transcript bytes are read. --flush requests all changed transcript tails.\nAn unavailable account requires an explicit --unknown-identity choice.", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		encodedSession, _ := json.Marshal(session)
		if !utf8.ValidString(session) || strings.TrimSpace(session) == "" || len(encodedSession) > 4098 || strings.ContainsAny(session, "\x00\r\n") {
			return fmt.Errorf("a bounded --session identifier is required")
		}
		if output == "" {
			return fmt.Errorf("--output is required")
		}
		r, err := open(c.Context(), false)
		if err != nil {
			return err
		}
		identity := service.Identity{}
		if !unknown {
			identity, err = deps.identity()
			if err != nil {
				return fmt.Errorf("read producer identity: %w", err)
			}
			if identity.AccountUUID == "" {
				return fmt.Errorf("no account identity; use --unknown-identity to record explicit unknown attribution")
			}
		}
		provenance, err := service.CaptureProvenance(identity)
		if err != nil {
			return err
		}
		mode := queue.Normal
		if flush {
			mode = queue.All
		}
		submission := queueSubmission{Version: 1, Scope: r.ScopeID(), At: time.Now().UTC(), Identity: identity, Request: queue.Request{EventID: rand.Text(), SessionID: session, ProvenanceID: provenance, Flush: queue.Flush{Mode: mode}}}
		submission.Checksum = queueRequestChecksum(submission)
		data, err := json.MarshalIndent(submission, "", "  ")
		if err != nil {
			return err
		}
		if err = writeQueueRequest(c.Context(), output, append(data, '\n')); err != nil {
			return err
		}
		_, err = fmt.Fprintln(c.OutOrStdout(), "Request saved. Enqueue this same file on every retry.")
		return err
	}}
	prepare.Flags().StringVar(&session, "session", "", "session identifier for this request")
	prepare.Flags().StringVarP(&output, "output", "o", "", "new private request file; never overwritten")
	prepare.Flags().BoolVar(&flush, "flush", false, "capture every changed transcript tail")
	prepare.Flags().BoolVar(&unknown, "unknown-identity", false, "explicitly record unknown account attribution")
	_ = prepare.MarkFlagFilename("output")
	cmd.AddCommand(prepare)
	cmd.AddCommand(&cobra.Command{Use: "enqueue <request-file>", Short: "Durably accept a saved request (safe to retry the same file)", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		submission, err := readQueueRequest(args[0])
		if err != nil {
			return err
		}
		r, err := open(c.Context(), false)
		if err != nil {
			return err
		}
		if submission.Scope != r.ScopeID() {
			return queue.ErrBinding
		}
		// Confirm the producer file's durability before any queue admission. A
		// successfully read file alone is not a durable retry record.
		if err = durable.Rewrite(c.Context(), args[0]); err != nil {
			return err
		}
		event, err := r.Enqueue(c.Context(), submission.Identity, submission.Request, submission.At)
		if err != nil {
			return fmt.Errorf("enqueue failed; retain and retry the same request file: %w", err)
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(struct{ Generation, Batch uint64 }{event.Generation, event.BatchID})
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
		type row struct {
			Batch       uint64
			Phase       queue.Phase
			Status      queue.Status
			Events      int
			Attempts    uint64
			NotBefore   time.Time
			FailureCode string
		}
		rows := make([]row, 0, len(jobs))
		for _, j := range jobs {
			rows = append(rows, row{j.ID, j.Phase, j.Status, len(j.Events), j.Attempts, j.NotBefore, j.FailureCode})
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(struct {
			Batches  []row
			Capacity queue.Capacity
		}{rows, capacity})
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
				if err = deps.private(ctx, req.Config.Remote); err != nil {
					return service.QueueRuntimeInputs{}, err
				}
				remote, err := commitartifact.NewConfiguredGitTransport(commitartifact.GitTransportOptions{Remote: req.Config.Remote, Branch: "main"})
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
			case "init", "status", "run", "drain":
				entries = append(entries, climenu.Entry{Label: child.Name(), Desc: child.Short, Cmd: child})
			}
		}
		return climenu.RunMenu(c, c.CommandPath(), "Prepare/enqueue/retry require arguments; use those commands directly.", entries)
	}
	return cmd
}

func writeQueueRequest(ctx context.Context, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
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
	return durable.Rewrite(ctx, path)
}

func queueRequestChecksum(s queueSubmission) string {
	s.Checksum = ""
	data, _ := json.Marshal(s)
	return artifact.Key(data)
}

func readQueueRequest(path string) (queueSubmission, error) {
	var s queueSubmission
	st, err := os.Lstat(path)
	if err != nil {
		return s, err
	}
	if !st.Mode().IsRegular() || st.Size() > queueRequestLimit {
		return s, fmt.Errorf("request must be a regular file at most 128 KiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, queueRequestLimit+1))
	if err != nil {
		return s, err
	}
	if len(data) > queueRequestLimit {
		return s, fmt.Errorf("request exceeds 128 KiB")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&s); err != nil {
		return s, err
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return s, fmt.Errorf("request must contain exactly one JSON object")
	}
	if s.Checksum != queueRequestChecksum(s) {
		return s, fmt.Errorf("saved request checksum mismatch")
	}
	if s.Version != 1 || s.Scope == "" || s.At.IsZero() || s.Request.EventID == "" || s.Request.SessionID == "" {
		return s, fmt.Errorf("invalid saved request")
	}
	provenance, err := service.CaptureProvenance(s.Identity)
	if err != nil {
		return s, err
	}
	if s.Request.ProvenanceID != provenance {
		return s, queue.ErrBinding
	}
	return s, nil
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
