package account

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
)

// SwitchOptions configure a machine-wide credential swap.
type SwitchOptions struct {
	// Force swaps even while Codex is running. The live sessions keep the
	// credential they started with in memory and will fail on their next
	// refresh; that is the cost, and it is stated rather than hidden.
	Force bool
	// Kill ends the running Codex processes first, which is the tidy version
	// of Force.
	Kill bool
	// DryRun reports what would happen and changes nothing.
	DryRun bool
}

// SwitchResult is the outcome of a swap.
type SwitchResult struct {
	From     string
	To       string
	ToEmail  string
	Backup   string
	Switched bool
	// Blocking names the live sessions that stopped the swap. Non-empty with
	// Switched false and a nil error is a REFUSAL, not a success — a caller
	// that ignores it will report "switched" for a swap that never happened.
	Blocking []Instance
}

// Switch makes account a the machine's live Codex login.
//
// The order is deliberate and each step exists because skipping it loses
// something:
//
//  1. take codexrig's swap lock, so two of these cannot interleave;
//  2. scan for running Codex processes, and refuse (or kill) — a swap under a
//     live session leaves it holding a credential for a different account;
//  3. read the live credential AFTER the lock, never before, so two racing
//     swaps cannot each act on a snapshot taken before the other;
//  4. check the target can authenticate BEFORE displacing anything;
//  5. back up what is about to be displaced;
//  6. store the displaced credential back under its own account, so its tokens
//     stay current;
//  7. write the new credential, then move the pointer.
func (s *Store) Switch(a Account, opts SwitchOptions) (SwitchResult, error) {
	res := SwitchResult{To: a.ID, ToEmail: a.Email}

	machineHome, err := codexhome.Default()
	if err != nil {
		return res, err
	}
	// Refuse while the environment points somewhere else: a swap is a change to
	// the MACHINE's login, and a shell with CODEX_HOME set is looking at a
	// different Codex than the one about to change.
	if env, set := codexhome.Env(); set && !codexhome.SameDir(env, machineHome) {
		return res, fmt.Errorf("%s points at %s, not the machine's Codex home — `switch` changes the machine-wide login; unset it, or use `codexrig account run` for an isolated session", codexhome.EnvHome, env)
	}

	lock, err := s.AcquireSwap(5 * time.Second)
	if err != nil {
		return res, err
	}
	defer lock.Release()

	insts, scanErr := s.scanner()(machineHome)
	if scanErr != nil && !opts.Force {
		return res, fmt.Errorf("%w — nothing was changed; retry, or use --force if you accept swapping under a live session", ErrProcessScan)
	}
	if len(insts) > 0 {
		switch {
		case opts.DryRun:
			res.Blocking = insts
		case opts.Kill:
			if failed := KillInstances(insts, 3*time.Second); len(failed) > 0 {
				res.Blocking = failed
				return res, fmt.Errorf("%d Codex process(es) would not end", len(failed))
			}
		case !opts.Force:
			res.Blocking = insts
			// A short summary, because the caller has already rendered the
			// list and the options; repeating the whole refusal under an
			// ERROR heading reads as two different problems.
			return res, fmt.Errorf("%w — %d live session(s)", ErrCodexBusy, len(insts))
		}
	}

	target, err := s.Credential(a.ID)
	if err != nil {
		return res, fmt.Errorf("%s has no stored credential: %w", a.Title(), err)
	}
	if !HasTokens(target) {
		return res, fmt.Errorf("%w: %s — run `codexrig account run %s`, log in, then `codexrig account add --from-home %s`", ErrStoredNoTokens, a.Title(), a.ID, a.ID)
	}

	live, liveErr := ReadLive()
	if liveErr != nil && !errors.Is(liveErr, ErrNoLive) {
		return res, liveErr
	}
	if from, ok := s.matchLive(live); ok {
		res.From = from
	}
	if res.From == a.ID {
		// Already live. Still worth re-pointing, since an out-of-date pointer
		// is how `list` shows the wrong arrow.
		if !opts.DryRun {
			_ = s.SetActive(a.ID)
		}
		return res, nil
	}

	if opts.DryRun {
		return res, nil
	}

	if len(live) > 0 {
		backup, err := s.BackupLive(live)
		if err != nil {
			return res, fmt.Errorf("backing up the live credential: %w", err)
		}
		res.Backup = backup
		// Round-trip the displaced login so its refreshed tokens survive. A
		// failure here is not fatal — the backup above already holds the bytes
		// — but it must not be silent, so it is returned once the swap is done.
		if res.From != "" && HasTokens(live) {
			if err := s.SaveCredential(res.From, live); err != nil {
				if werr := WriteLive(target); werr != nil {
					return res, fmt.Errorf("could not store the displaced credential (%v) and could not complete the swap (%v) — the displaced copy is at %s", err, werr, backup)
				}
				_ = s.SetActive(a.ID)
				res.Switched = true
				return res, fmt.Errorf("switched, but the displaced credential could not be stored back under %s: %w (a copy is at %s)", res.From, err, backup)
			}
		}
	}

	if err := WriteLive(target); err != nil {
		return res, err
	}
	if err := s.SetActive(a.ID); err != nil {
		return res, fmt.Errorf("switched the credential but could not record it as active: %w", err)
	}
	res.Switched = true
	return res, nil
}

// matchLive reports which tracked account the live credential belongs to.
//
// Matched on the account id first and the email second, and never on a token:
// the refresh token rotates, so a token comparison would answer "none of them"
// within minutes of any real use.
func (s *Store) matchLive(live []byte) (string, bool) {
	if len(live) == 0 {
		return "", false
	}
	id := IdentityOf(live)
	all, err := s.List()
	if err != nil {
		return "", false
	}
	if id.AccountID != "" {
		for _, a := range all {
			if a.AccountID == id.AccountID {
				return a.ID, true
			}
		}
	}
	if id.Email != "" {
		var hits []Account
		for _, a := range all {
			if strings.EqualFold(a.Email, id.Email) {
				hits = append(hits, a)
			}
		}
		if len(hits) == 1 {
			return hits[0].ID, true
		}
	}
	return "", false
}

// LiveAccount reports which tracked account the machine is currently logged in
// as, preferring the credential's own identity over the recorded pointer.
//
// The pointer is what codexrig believes; the credential is what the server will
// act on. When they disagree the credential wins, because everything the user
// sees should name the account their requests actually authenticate as.
func (s *Store) LiveAccount() (id string, fromCredential bool) {
	if live, err := ReadLive(); err == nil {
		if got, ok := s.matchLive(live); ok {
			return got, true
		}
	}
	ptr, _ := s.Active()
	return ptr, false
}
