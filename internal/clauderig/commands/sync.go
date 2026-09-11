package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
	"github.com/spf13/cobra"
)

// hookTranscripts reads the Claude Code hook payload from stdin, if one is
// there, and returns the transcript it names. Every hook event carries
// `transcript_path`; SessionEnd is the one that runs `sync --flush`.
//
// Three outcomes, because the caller does something different with each: a
// path, to flush that transcript alone; nothing, when there was no payload to
// read (a terminal, or an empty stream such as /dev/null), which the caller
// treats as a flush of everything; and an error, when something arrived but
// named no transcript — malformed, the wrong shape, or a stream that stayed
// silent past the wait. That last one is not a flush of everything: one bad
// payload would otherwise restage every long session's transcript mid-chunk,
// the very growth the throttle exists to stop.
func hookTranscripts(in io.Reader) ([]string, error) {
	// A Cygwin/MSYS terminal on Windows is a pipe to the OS and a terminal
	// to the person; without the second check a by-hand run there would
	// wait out the timeout and flush nothing.
	if f, ok := in.(*os.File); ok && (isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())) {
		return nil, nil
	}
	// Decoded as a stream, not read to EOF: the payload is one JSON object,
	// and a hook runner that writes it and keeps the pipe open would
	// otherwise leave the read waiting for a close that never comes. The
	// wait is bounded too, so a stream with nothing on it cannot hold the
	// sync hostage; a real payload is a few hundred bytes and arrives at
	// once.
	type payload struct {
		TranscriptPath string `json:"transcript_path"`
	}
	type result struct {
		p   payload
		err error
	}
	ch := make(chan result, 1)
	// Counted, so a stream that carried only whitespace — a broken hook
	// printing a newline — is told apart from one that carried nothing: the
	// decoder reports EOF for both, and only the second means "no payload".
	counted := &countingReader{r: io.LimitReader(in, 1<<20)}
	go func() {
		var r result
		r.err = json.NewDecoder(counted).Decode(&r.p)
		ch <- r
	}()
	select {
	case r := <-ch:
		if errors.Is(r.err, io.EOF) {
			if counted.n > 0 {
				return nil, errors.New("hook payload on stdin is blank")
			}
			return nil, nil // nothing on the stream at all
		}
		if r.err != nil {
			return nil, fmt.Errorf("hook payload on stdin is not a JSON object: %w", r.err)
		}
		if strings.TrimSpace(r.p.TranscriptPath) == "" {
			return nil, errors.New("hook payload on stdin names no transcript_path")
		}
		return []string{r.p.TranscriptPath}, nil
	case <-time.After(2 * time.Second):
		// The read is abandoned by closing what it reads from — nothing in
		// sync reads stdin after this — so the goroutine ends rather than
		// sitting on the stream for the rest of the process.
		if c, ok := in.(io.Closer); ok {
			_ = c.Close()
		}
		return nil, errors.New("nothing arrived on stdin within 2s")
	}
}

// countingReader counts the bytes an io.Reader handed out.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// NewSyncCmd builds the `sync` command — walk → redact → manifest → tripwire into
// the staging repo, then commit (empty-guarded) and push. Streams the report so
// redaction is visible, not magic. The tripwire fails the sync loudly if a secret
// slips past redaction; nothing is pushed in that case.
func NewSyncCmd() *cobra.Command {
	return newSyncCmd(defaultQueueCommandDeps())
}

func newSyncCmd(deps queueCommandDeps) *cobra.Command {
	var dryRun, flush, hook bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Snapshot, redact, rewrite, and push your Claude Code setup",
		Long: "Walks the sync roots, redacts secret-bearing fields, rewrites machine\n" +
			"paths into a portable form, commits, and pushes.\n\n" +
			"With queue enable-hooks, Stop/SessionEnd hooks save queued requests locally;\n" +
			"manual sync uses queue sync and its supervision/coverage checks. --dry-run\n" +
			"never admits hooks or acknowledges requests. SessionStart pull is unchanged.\n\n" +
			"Coordinates with other staging operations: ordinary hooks skip a busy store;\n" +
			"manual sync and --flush wait up to 15 seconds before asking you to retry.\n\n" +
			"Complete staged-text scanning refuses recognized credentials before publication.\n" +
			"Set redactTranscripts true to scrub supported signatures from staged transcripts.\n\n" +
			"Chunking defaults on in new configs; omitted keys in existing configs mean auto.\n" +
			"Set chunkTranscripts true to migrate transcripts larger than 8 MiB to reusable\n" +
			"4 MiB chunks on the next sync; restore reconstructs native JSONL. Upgrade every\n" +
			"participating client first. Set false to convert back, or auto to follow the repo.\n" +
			"Chunk mode captures every changed tail and bypasses the large-file throttle.\n\n" +
			"With plain storage, a transcript over retention.largeFileBytes is restaged once it has\n" +
			"grown by half that again, or once it has gone quiet for 30 minutes, so the\n" +
			"Stop hook does not re-commit a 50 MB file every turn. The SessionEnd hook\n" +
			"runs `sync --flush`, which restages the ended session's transcript (the\n" +
			"hook names it on stdin) regardless, so its last turn never waits for the\n" +
			"next session; other sessions' transcripts keep their throttle. Run by\n" +
			"hand, `--flush` restages every changed transcript. A payload on stdin\n" +
			"that names no transcript flushes nothing, and says so.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if handled, err := routeQueuedSync(cmd, deps, dryRun, flush, hook); handled || err != nil {
				return err
			}
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}

			// Every sync holds the staging lock: chunk cleanup and publication must
			// not race another writer. Manual runs wait and are never debounced.
			automated := hook || !Interactive()
			wait := time.Duration(0)
			if flush || !automated {
				wait = flushLockWait
			}
			lockDeadline := time.Now().Add(wait)
			ctx, release, lerr := storelock.Acquire(ctx, staging, wait)
			if errors.Is(lerr, storelock.ErrBusy) && automated && !flush {
				fmt.Fprintln(out, DimStyle.Render("  another operation is using the staging store — skipping"))
				return nil
			}
			if lerr != nil {
				return lerr
			}
			defer release()
			lock, got, lerr := acquireSyncLockWaitContext(ctx, staging, max(0, time.Until(lockDeadline)))
			if lerr != nil {
				return lerr
			}
			if !got {
				if !automated || flush {
					return fmt.Errorf("another sync is still running; retry after it finishes")
				}
				fmt.Fprintln(out, DimStyle.Render("  another sync is running — skipping"))
				return nil
			}
			defer lock.Release()

			storedChunkMode, err := transcript.Enabled(staging)
			if err != nil {
				return err
			}
			migrationPending := cfg.ChunkTranscripts != nil && *cfg.ChunkTranscripts != storedChunkMode

			// Legacy hooks invoke bare `sync` without a terminal. Keep recognizing
			// those. Flushes and explicit storage-mode changes bypass the debounce.
			if automated {
				if iv := cfg.HookInterval(); iv > 0 && !flush && !migrationPending {
					if last, ok := lastSuccessfulSync(staging, me.Name); ok {
						if since := time.Since(last); since < iv {
							fmt.Fprintf(out, "  %s\n", DimStyle.Render(fmt.Sprintf(
								"synced %s ago — next in %s (hookIntervalMinutes)",
								since.Round(time.Second), (iv-since).Round(time.Second))))
							return nil
						}
					}
				}
			}

			request := service.SyncRequest{
				Config: cfg, Machine: me, StagingDir: staging,
				DryRun: dryRun, AllowMergeTool: interactive(),
			}
			if flush {
				request.ResolveFlush = func() service.FlushIntent {
					paths, herr := hookTranscripts(cmd.InOrStdin())
					switch {
					case herr != nil:
						fmt.Fprintf(out, "%s\n", WarnStyle.Render(fmt.Sprintf(
							"⚠ --flush: %s — no transcript flushed; the large-file throttle stays on", herr)))
						return service.FlushIntent{}
					case len(paths) == 0:
						return service.FlushIntent{Mode: service.FlushAll}
					default:
						return service.FlushIntent{Mode: service.FlushSelected, Paths: paths}
					}
				}
			}
			_, err = applicationService(out).Sync(ctx, request)
			return err
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "stage and scan, but don't commit or push")
	cmd.Flags().BoolVar(&flush, "flush", false, "restage the ended session's transcript (from the hook payload on stdin), or every changed transcript when run by hand, past the large-file throttle")
	cmd.Flags().BoolVar(&hook, "hook", false,
		"force the debounce on even with a terminal attached (it is automatic without one)")
	return cmd
}

// machineName returns this host's configured machine name. It identifies the
// local machine by its stable path identity (OS token + home directory) rather
// than picking an arbitrary map entry, so a config that registers more than one
// machine resolves deterministically to the right one instead of flipping with
// Go's randomized map iteration. Falls back to the OS hostname, then "this",
// when no registered machine matches this host.
func machineName(cfg *config.Config) string { return config.ResolveName(cfg) }
