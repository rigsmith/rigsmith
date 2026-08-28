package journal

import (
	"fmt"

	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
)

// Summary renders the record as one human line. It lives here rather than in
// either front end so `clauderig status` and the UI's activity feed can never
// describe the same record differently.
//
// Failures lead with what went wrong rather than with counts: the counts from a
// run that didn't finish are noise, and burying the reason is how this class of
// problem stayed invisible in the first place.
func (r Record) Summary() string {
	switch r.Outcome {
	case OutcomeRefused:
		// Say what was caught and that nothing was pushed. "Refused" alone
		// reads as a malfunction rather than as the tripwire doing its job.
		return fmt.Sprintf("Refused to push — %s look like credentials", plural(len(r.Leaks), "value", "values"))
	case OutcomeFailed:
		if r.Error != "" {
			return r.Op.verb() + " failed: " + r.Error
		}
		return r.Op.verb() + " failed"
	}

	switch r.Op {
	case OpRestore:
		return fmt.Sprintf("Restored %s", plural(r.Files, "file", "files"))
	case OpPull:
		return "Pulled from remote"
	case OpMerge:
		if r.Files > 0 {
			return fmt.Sprintf("Merged the remote — %s resolved by policy", plural(r.Files, "file", "files"))
		}
		return "Merged the remote cleanly"
	default:
		// Pruning and size refusals are events in their own right — they happen
		// to the staged copy whether or not anything new was written — so they
		// are reported on quiet runs too. Redactions are not: the redactor runs
		// over every JSON file on every pass, so its count is a property of the
		// tree rather than of this run, and repeating it on a run that wrote
		// nothing is the noise that hid the interesting lines.
		var extra string
		if r.AgedOut > 0 {
			extra += fmt.Sprintf(", %d aged out", r.AgedOut)
		}
		if r.Oversize > 0 {
			extra += fmt.Sprintf(", %s too large", plural(r.Oversize, "file", "files"))
		}

		// Most syncs change nothing — they run every few minutes against a tree
		// that only moves when someone is actually working. Saying so plainly
		// keeps the runs that did something legible instead of burying them
		// under identical lines.
		if r.Files == 0 {
			if r.Unchanged > 0 {
				return fmt.Sprintf("No changes — %s already current", plural(r.Unchanged, "file", "files")) + extra
			}
			return "No changes" + extra
		}

		s := fmt.Sprintf("Synced %s", plural(r.Files, "file", "files"))
		if r.Redactions > 0 {
			s += fmt.Sprintf(", %s redacted", plural(r.Redactions, "secret", "secrets"))
		}
		return s + extra
	}
}

// verb names the operation at the start of a sentence.
func (o Op) verb() string {
	switch o {
	case OpPull:
		return "Pull"
	case OpMerge:
		return "Merge"
	case OpRestore:
		return "Restore"
	default:
		return "Sync"
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// FromSync turns an engine sync report into a record. serr is the error Sync
// returned, if any.
//
// A tripwire hit becomes OutcomeRefused rather than OutcomeFailed: nothing
// broke, clauderig declined to push something that looked like a credential.
// The activity feed renders the two differently, and conflating them would
// train people to ignore the one that means "a secret nearly leaked".
func FromSync(machine string, rep *engine.Report, serr error) Record {
	rec := Record{Machine: machine, Op: OpSync, Outcome: OutcomeOK}

	if rep != nil {
		rec.Projects = rep.ManifestProjects
		rec.AgedOut = rep.RetentionPruned
		for _, r := range rep.Roots {
			if r.Skipped {
				continue
			}
			rec.Files += r.Files
			rec.Unchanged += r.Unchanged
			rec.Redactions += r.Redactions
			rec.AgedOut += r.RetentionByAge
			rec.Skipped += r.SkippedFiles
			rec.Oversize += len(r.Oversize)
		}
		for _, f := range rep.Findings {
			rec.Leaks = append(rec.Leaks, Leak{Path: f.Path, Kind: f.Kind})
		}
	}

	if serr != nil {
		rec.Error = serr.Error()
		rec.Outcome = OutcomeFailed
		if len(rec.Leaks) > 0 {
			rec.Outcome = OutcomeRefused
		}
	}
	return rec
}

// FromRestore turns a restore report into a record.
func FromRestore(machine string, rep *engine.RestoreReport, rerr error) Record {
	rec := Record{Machine: machine, Op: OpRestore, Outcome: OutcomeOK}

	if rep != nil {
		for _, r := range rep.Roots {
			rec.Files += r.Files
		}
	}
	if rerr != nil {
		rec.Error = rerr.Error()
		rec.Outcome = OutcomeFailed
	}
	return rec
}

// Failed builds a bare failure record for a phase with no report to summarise —
// a rejected push, a failed fetch. This is the case the whole package exists
// for: before it, these died in hook stderr.
func Failed(machine string, op Op, err error) Record {
	rec := Record{Machine: machine, Op: op, Outcome: OutcomeFailed}
	if err != nil {
		rec.Error = err.Error()
	}
	return rec
}

// Succeeded builds a bare success record for a phase with nothing to count.
func Succeeded(machine string, op Op) Record {
	return Record{Machine: machine, Op: op, Outcome: OutcomeOK}
}
