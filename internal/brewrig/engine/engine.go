// Package engine joins the three halves — what brew reports, what this machine
// published last time, and what the other machines published — into the two
// operations that change something: snapshot (publish this machine) and apply
// (bring this machine up to the union).
package engine

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/rigsmith/rigsmith/internal/brewrig/brew"
	"github.com/rigsmith/rigsmith/internal/brewrig/inventory"
	"github.com/rigsmith/rigsmith/internal/brewrig/plan"
)

// Snapshot builds the inventory this machine should publish now.
//
// prev is what it published last time, or nil on the first sync. Carrying it
// forward is not an optimisation — it is where a deliberate uninstall is
// detected. Without the previous file a removed package is indistinguishable
// from one that was never installed, and the intent to remove it is lost.
func Snapshot(ctx context.Context, c *brew.Client, machine, osName string, prev *inventory.Machine, now time.Time) (*inventory.Machine, []inventory.Ref, error) {
	cur, err := c.Inventory(ctx, machine, osName)
	if err != nil {
		return nil, nil, err
	}

	var retired []inventory.Ref
	if prev != nil {
		cur.OptOut = prev.OptOut
		for k, v := range prev.Acknowledged {
			r, err := inventory.ParseRef(k)
			if err != nil {
				continue
			}
			// Only worth carrying while the package is still here to remove.
			// Once brew no longer has it the question is moot, and keeping the
			// record would silence a genuine future offer.
			if cur.BrewHas(r) {
				cur.Acknowledge(r, v)
			}
		}
		for k, v := range prev.Retired {
			r, err := inventory.ParseRef(k)
			if err != nil {
				// Drop it rather than guess. Coercing "wget" into
				// "formula:wget" would invent a retirement for a real package
				// and offer to uninstall it on the other machine.
				continue
			}
			cur.Retire(r, v)
		}
		for _, r := range prev.Installed() {
			// Absent from the published inventory is NOT the same as removed.
			// A package stops being installed_on_request as soon as something
			// else depends on it, and brew still has it. Retirement is the
			// only thing that makes brewrig offer an uninstall, so it has to
			// mean "brew does not have this any more" and nothing looser.
			if !cur.BrewHas(r) {
				cur.Retire(r, now)
				retired = append(retired, r)
			}
		}
	}

	// Anything installed here now is, by definition, not retired here. This
	// also covers the deliberate change of mind: reinstalling a package this
	// machine previously dropped clears its own stale record, and the fresh
	// install time outranks any other machine's retire stamp.
	for _, r := range cur.Installed() {
		cur.Unretire(r)
	}

	// Opting out and having it installed contradict each other; the install is
	// the more recent, more explicit act, so it wins and the stale opt-out goes.
	cur.OptOut.Formulae = keepNotInstalled(cur, inventory.Formula, cur.OptOut.Formulae)
	cur.OptOut.Casks = keepNotInstalled(cur, inventory.Cask, cur.OptOut.Casks)

	cur.ChangedAt = now.UTC()
	cur.Normalize()

	// If nothing else moved, keep the previous timestamp. Otherwise syncedAt
	// alone makes every snapshot a new file, every sync a commit, and the
	// "an unchanged inventory produces no commit" property a fiction — two
	// machines would publish timestamp churn at each other forever. Verified
	// the hard way: two no-op syncs produced two commits whose only diff was
	// this field.
	//
	// It therefore means "when this inventory last changed", which is the more
	// useful of the two readings anyway; how recently a machine checked in is
	// the git history's job, and `status` reads it from there.
	// Never freeze onto a zero time. A published file written before this
	// field existed — or hand-edited without it — unmarshals as zero, and
	// carrying that forward pins the machine at year 1 for good. That is not
	// hypothetical: renaming the key stranded the old value under the old
	// name, every later sync then froze the zero back in, and `status` showed
	// "31 Dec 19:03" until this guard went in.
	if prev != nil && !prev.ChangedAt.IsZero() && sameExceptChangedAt(prev, cur) {
		cur.ChangedAt = prev.ChangedAt
	}
	inventory.SortRefs(retired)
	return cur, retired, nil
}

// sameExceptChangedAt compares two inventories ignoring the timestamp, by the
// same marshalling the store writes — so "the same" here means exactly "would
// produce an identical file", rather than a field list that goes stale the
// next time one is added.
func sameExceptChangedAt(a, b *inventory.Machine) bool {
	ac, bc := *a, *b
	ac.ChangedAt = time.Time{}
	bc.ChangedAt = time.Time{}
	ab, aerr := inventory.Marshal(&ac)
	bb, berr := inventory.Marshal(&bc)
	if aerr != nil || berr != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

func keepNotInstalled(m *inventory.Machine, k inventory.Kind, names []string) []string {
	var out []string
	for _, n := range names {
		if !m.Has(inventory.Ref{Kind: k, Name: n}) {
			out = append(out, n)
		}
	}
	return out
}

// Result records what an apply actually did, including what it could not do.
type Result struct {
	Tapped    []string
	Installed []inventory.Ref
	Removed   []inventory.Ref
	// Failed carries one entry per package that errored. brew cannot roll
	// back, and stopping at the first failure would leave the rest of a long
	// run undone with no explanation, so apply continues and reports.
	Failed []Failure
}

// Failure is one package that did not apply.
type Failure struct {
	Ref inventory.Ref
	// Tap is set instead of Ref when it was the tap that failed.
	Tap string
	Err error
}

// Any reports whether the apply changed anything.
func (r *Result) Any() bool {
	return len(r.Tapped) > 0 || len(r.Installed) > 0 || len(r.Removed) > 0
}

// Progress is called before each step so a caller can show what is happening;
// brew installs are slow and silence reads as a hang.
type Progress func(action, name string)

// Apply installs the plan's additions on this machine. It never removes
// anything: removals go through ApplyRemovals, which the caller reaches only
// after an interactive confirmation.
func Apply(ctx context.Context, c *brew.Client, p *plan.Plan, onStep Progress) *Result {
	res := &Result{}
	step := func(action, name string) {
		if onStep != nil {
			onStep(action, name)
		}
	}

	// Taps first: a package from a third-party tap cannot install until its
	// tap is present, and a failed tap explains every install that follows it.
	failedTaps := map[string]bool{}
	for _, t := range p.Taps {
		step("tap", t)
		if err := c.Tap(ctx, t); err != nil {
			failedTaps[t] = true
			res.Failed = append(res.Failed, Failure{Tap: t, Err: err})
			continue
		}
		res.Tapped = append(res.Tapped, t)
	}

	for _, in := range p.Install {
		if in.Tap != "" && failedTaps[in.Tap] {
			res.Failed = append(res.Failed, Failure{Ref: in.Ref,
				Err: fmt.Errorf("skipped: its tap %s could not be added", in.Tap)})
			continue
		}
		step("install", in.Ref.Label())
		if err := c.Install(ctx, in.Ref); err != nil {
			res.Failed = append(res.Failed, Failure{Ref: in.Ref, Err: err})
			continue
		}
		res.Installed = append(res.Installed, in.Ref)
	}
	return res
}

// ApplyRemovals uninstalls packages the caller has already confirmed, one by
// one. Nothing in brewrig calls this without an interactive yes per package.
func ApplyRemovals(ctx context.Context, c *brew.Client, refs []inventory.Ref, onStep Progress) *Result {
	res := &Result{}
	for _, r := range refs {
		if onStep != nil {
			onStep("uninstall", r.Label())
		}
		if err := c.Uninstall(ctx, r); err != nil {
			res.Failed = append(res.Failed, Failure{Ref: r, Err: err})
			continue
		}
		res.Removed = append(res.Removed, r)
	}
	return res
}
