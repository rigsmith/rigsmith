package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/durable"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
	claudesession "github.com/rigsmith/rigsmith/internal/clauderig/session"
	"github.com/spf13/cobra"
)

const hookInboxLimit = 1 << 20
const hookInboxEvents = 128

type hookInboxState struct {
	Version  int
	Scope    string
	Requests []queueSubmission
	Checksum string
}

type hookInbox struct {
	dir  string
	save func(context.Context, string, func(*os.File) error) error
}

func addQueueHookProducerCommands(parent *cobra.Command, deps queueCommandDeps, open func(context.Context, bool) (*service.QueueRuntime, error)) {
	for _, recoverOnly := range []bool{false, true} {
		var inbox string
		var unknown bool
		name, short := "hook", "Save and enqueue a Stop/SessionEnd request without publishing"
		if recoverOnly {
			name, short = "recover-hooks", "Retry saved hook requests using their original account attribution"
		}
		cmd := &cobra.Command{Use: name, Short: short, Long: short + ".\n\nUses a private, bounded inbox tied to one initialized queue runtime.\nEach hook invocation is a new event; after any failure, run recover-hooks\nwith the same --dir, --profile and --inbox instead of replaying stdin.\nHook input must finish within 2 seconds and 128 KiB; admission has a 10-second deadline.\nRequests are saved before admission and removed only after confirmed enqueue.\nRecovery never reads stdin or the current account. No worker or hook is installed.\nStop producers, recover this inbox, then drain the queue before rollback.\nWindows callers must provide a private directory ACL.", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			ctx := c.Context()
			var payload queueHookPayload
			if !recoverOnly {
				bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				ctx = bounded
				var err error
				payload, err = readQueueHook(ctx, c.InOrStdin(), 2*time.Second)
				if err != nil {
					return err
				}
				payload.SessionID = claudesession.CanonicalID(strings.TrimSpace(payload.SessionID))
				if err := validateQueueSessionID(payload.SessionID); err != nil {
					return err
				}
			}
			r, err := open(ctx, false)
			if err != nil {
				return err
			}
			root := inbox
			if root == "" {
				dir, err := config.Dir()
				if err != nil {
					return err
				}
				root = filepath.Join(dir, "hook-inbox")
			}
			if err := r.CheckRequestPath(root); err != nil {
				return err
			}
			var request *queueSubmission
			if !recoverOnly {
				if err := r.ValidateHookTranscript(payload.TranscriptPath, payload.SessionID); err != nil {
					return err
				}
				flush := queue.Flush{Mode: queue.Normal}
				if payload.Event == "SessionEnd" {
					flush = queue.Flush{Mode: queue.Selected, Paths: []string{payload.TranscriptPath}}
				}
				s, err := newQueueSubmission(r, payload.SessionID, flush, unknown, deps.identity)
				if err != nil {
					return err
				}
				request = &s
			}
			admit := func(ctx context.Context, identity service.Identity, request queue.Request, at time.Time) (queue.Event, error) {
				if request.Flush.Mode == queue.Selected {
					if err := r.ValidateHookTranscript(request.Flush.Paths[0], request.SessionID); err != nil {
						return queue.Event{}, err
					}
				}
				return r.Enqueue(ctx, identity, request, at)
			}
			count, err := (hookInbox{dir: root, save: durable.Write}).admit(ctx, r.ScopeID(), request, admit)
			if err != nil {
				return fmt.Errorf("hook admission incomplete; retain the inbox and run queue recover-hooks with the same runtime and inbox: %w", err)
			}
			_, err = fmt.Fprintf(c.ErrOrStderr(), "Hook inbox recovered; %d requests confirmed in queue. Publication requires a worker.\n", count)
			return err
		}}
		cmd.Flags().StringVar(&inbox, "inbox", "", "private hook inbox directory (default ~/.clauderig/hook-inbox)")
		_ = cmd.MarkFlagDirname("inbox")
		if !recoverOnly {
			cmd.Flags().BoolVar(&unknown, "unknown-identity", false, "explicitly record unknown account attribution")
		}
		parent.AddCommand(cmd)
	}
}

// One inbox lease spans durable intent, admission and removal. Runtime producers
// acquire their own locks using the original context; no path holds a runtime
// lock while waiting for the inbox. Stable private roots are required throughout.
func (in hookInbox) admit(ctx context.Context, scope string, fresh *queueSubmission, enqueue func(context.Context, service.Identity, queue.Request, time.Time) (queue.Event, error)) (int, error) {
	if scope == "" {
		return 0, queue.ErrBinding
	}
	// Recovery is read-existing-only until validation has succeeded.
	if _, err := os.Stat(filepath.Dir(in.dir)); err != nil {
		return 0, err
	}
	if info, err := os.Lstat(in.dir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !queueInboxPrivate(info) {
			return 0, fmt.Errorf("hook inbox must be a private directory")
		}
	} else if !os.IsNotExist(err) || fresh == nil {
		return 0, err
	}
	_, release, err := storelock.Acquire(ctx, in.dir, service.StoreWait)
	if err != nil {
		return 0, err
	}
	defer release()
	s, err := in.load(scope)
	if os.IsNotExist(err) && fresh != nil {
		// Only a newly created directory permits initialization. An existing inbox
		// missing its journal must not silently forget prior producer intent.
		if err = os.Mkdir(in.dir, 0700); err != nil {
			return 0, fmt.Errorf("incomplete hook inbox; restore its journal before retrying: %w", err)
		}
		s = hookInboxState{Version: 1, Scope: scope, Requests: []queueSubmission{}}
	} else if err != nil {
		return 0, err
	}
	if fresh != nil {
		s.Requests = append(s.Requests, *fresh)
	}
	// This reflushes recovered intent before enqueue, including after uncertain
	// writes. A readable journal is not by itself proof of durable intent.
	if err := in.persist(ctx, s); err != nil {
		return 0, err
	}
	count := 0
	for len(s.Requests) != 0 {
		next := s.Requests[0]
		if _, err := enqueue(ctx, next.Identity, next.Request, next.At); err != nil {
			return count, err
		}
		// A failed removal leaves either the accepted request or the smaller journal.
		// Both replay safely because queue admission uses the saved event identity.
		s.Requests = s.Requests[1:]
		if err := in.persist(ctx, s); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (in hookInbox) load(scope string) (hookInboxState, error) {
	var s hookInboxState
	info, err := os.Lstat(in.dir)
	if err != nil {
		return s, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !queueInboxPrivate(info) {
		return s, fmt.Errorf("hook inbox must be a private directory")
	}
	path := filepath.Join(in.dir, "requests.json")
	info, err = os.Lstat(path)
	if err != nil {
		return s, err
	}
	if !info.Mode().IsRegular() || !queueInboxPrivate(info) || info.Size() > hookInboxLimit {
		return s, fmt.Errorf("invalid hook inbox journal")
	}
	f, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return s, err
	}
	if !os.SameFile(info, opened) {
		return s, fmt.Errorf("hook inbox changed during read")
	}
	if err = validateQueueRequestSingleLink(f); err != nil {
		return s, err
	}
	data, err := io.ReadAll(io.LimitReader(f, hookInboxLimit+1))
	if err != nil {
		return s, err
	}
	if len(data) > hookInboxLimit {
		return s, fmt.Errorf("hook inbox exceeds 1 MiB")
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("invalid hook inbox JSON")
	}
	// Exact canonical encoding rejects unknown/duplicate/case-aliased keys, null
	// scalar fields, invalid UTF-8 and unpaired surrogates without echoing input.
	canonical, err := json.Marshal(s)
	if err != nil || !bytes.Equal(append(canonical, '\n'), data) {
		return s, fmt.Errorf("noncanonical hook inbox JSON")
	}
	if s.Scope != scope {
		return s, queue.ErrBinding
	}
	if err := validateHookInbox(s); err != nil {
		return s, err
	}
	if s.Checksum != hookInboxChecksum(s) {
		return s, fmt.Errorf("hook inbox checksum mismatch")
	}
	return s, nil
}

func hookInboxChecksum(s hookInboxState) string {
	s.Checksum = ""
	data, _ := json.Marshal(s)
	return artifact.Key(data)
}

func validateHookInbox(s hookInboxState) error {
	if s.Version != 1 || s.Scope == "" || s.Requests == nil || len(s.Requests) > hookInboxEvents {
		return fmt.Errorf("invalid or full hook inbox (maximum 128 pending requests)")
	}
	seen := map[string]bool{}
	for _, request := range s.Requests {
		if request.Scope != s.Scope || seen[request.Request.EventID] {
			return queue.ErrBinding
		}
		seen[request.Request.EventID] = true
		if err := validateQueueSubmission(request); err != nil {
			return err
		}
		data, err := json.Marshal(request)
		if err != nil || len(data) > queueRequestLimit {
			return fmt.Errorf("hook request exceeds 128 KiB")
		}
		switch request.Request.Flush.Mode {
		case queue.Normal:
			if len(request.Request.Flush.Paths) != 0 {
				return fmt.Errorf("invalid normal hook request")
			}
		case queue.Selected:
			if len(request.Request.Flush.Paths) != 1 {
				return fmt.Errorf("invalid selected hook request")
			}
		default:
			return fmt.Errorf("invalid hook flush intent")
		}
	}
	return nil
}

func (in hookInbox) persist(ctx context.Context, s hookInboxState) error {
	if err := validateHookInbox(s); err != nil {
		return err
	}
	s.Checksum = hookInboxChecksum(s)
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > hookInboxLimit {
		return fmt.Errorf("hook inbox full (maximum 1 MiB); recover existing requests before sending new events")
	}
	if err := in.save(ctx, filepath.Join(in.dir, "requests.json"), func(out *os.File) error { _, err := out.Write(data); return err }); err != nil {
		return errors.Join(fmt.Errorf("hook inbox write failed"), err)
	}
	return nil
}
