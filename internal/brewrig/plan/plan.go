// Package plan turns the published machine inventories into the work this
// machine should do: what to install, what (if anything) to offer to remove,
// and where the two machines have drifted onto different versions.
//
// It is pure — no brew, no git, no clock beyond what it is handed — because
// this is the part that decides to uninstall software, and that decision has to
// be testable without a machine to break.
package plan

import (
	"sort"
	"time"

	"github.com/rigsmith/rigsmith/internal/brewrig/inventory"
)

// Install is one package to add to this machine.
type Install struct {
	Ref inventory.Ref
	// From names the machines that have it, so the prompt can say why this
	// appeared rather than just asserting that it should be installed.
	From []string
	// Tap is the non-default tap it comes from, which must be tapped first.
	Tap string
	// Adopt marks a package already installed here as a dependency. The
	// install still runs — that is what records it as wanted — but it is a
	// no-op download-wise, and saying so keeps the plan honest about its size.
	Adopt bool
}

// Remove is a proposed uninstall: the only kind brewrig ever proposes, and
// always behind a confirmation.
type Remove struct {
	Ref inventory.Ref
	// By is the machine that retired it, and At is when — both shown in the
	// prompt, because "something wants to uninstall this" is not enough
	// information to answer safely.
	By string
	At time.Time
}

// Skew is one package installed on several machines at different versions.
type Skew struct {
	Ref inventory.Ref
	// Versions maps machine name to the version installed there.
	Versions map[string]string
	// Behind names the machines not on the highest version present. It is
	// ordering by version string, which is not semver-correct in general, so
	// this is a prompt to run an update, never an automatic action.
	Behind []string
}

// Plan is the whole answer for one machine.
type Plan struct {
	Machine string
	Taps    []string
	Install []Install
	Remove  []Remove
	Skew    []Skew
	// OptedOut is what this machine is deliberately skipping, reported so a
	// missing package is never silently missing.
	OptedOut []inventory.Ref
}

// Empty reports whether there is nothing to do. Skew alone is not "work":
// it is resolved by `brewrig update`, not by `apply`.
func (p *Plan) Empty() bool { return len(p.Install) == 0 && len(p.Remove) == 0 && len(p.Taps) == 0 }

// Build computes the plan for `self` against every published machine (which
// includes self — a machine's own published file is part of the union, and
// leaving it out would make a fresh clone look like it should uninstall
// everything).
func Build(self *inventory.Machine, all []*inventory.Machine) *Plan {
	p := &Plan{Machine: self.Name}

	// Retirement is settled globally before anything else, because it decides
	// both halves: a retired package is not installed anywhere, and is offered
	// for removal wherever it survives.
	retired := retirements(all)

	type entry struct {
		from     []string
		tap      string
		versions map[string]string
	}
	union := map[inventory.Ref]*entry{}
	for _, m := range all {
		for _, r := range m.Installed() {
			e := union[r]
			if e == nil {
				e = &entry{versions: map[string]string{}}
				union[r] = e
			}
			e.from = append(e.from, m.Name)
			pkg, _ := m.Lookup(r)
			if pkg.Tap != "" {
				e.tap = pkg.Tap
			}
			if pkg.Version != "" {
				e.versions[m.Name] = pkg.Version
			}
		}
	}

	taps := map[string]bool{}
	for r, e := range union {
		switch {
		case retired[r].retired:
			// Retired wins over presence: skip the install, and if this
			// machine still has it, that is the one removal case.
			if self.Has(r) && retired[r].by != self.Name {
				p.Remove = append(p.Remove, Remove{Ref: r, By: retired[r].by, At: retired[r].at})
			}
		case self.OptedOut(r):
			p.OptedOut = append(p.OptedOut, r)
		case !self.Has(r):
			sort.Strings(e.from)
			p.Install = append(p.Install, Install{Ref: r, From: e.from, Tap: e.tap, Adopt: self.PresentAsDependency(r)})
			if e.tap != "" && !isDefaultTap(e.tap) {
				taps[e.tap] = true
			}
		}
		if s, ok := skewOf(r, e.versions); ok {
			p.Skew = append(p.Skew, s)
		}
	}

	for t := range taps {
		p.Taps = append(p.Taps, t)
	}
	sort.Strings(p.Taps)
	sort.Slice(p.Install, func(i, j int) bool { return less(p.Install[i].Ref, p.Install[j].Ref) })
	sort.Slice(p.Remove, func(i, j int) bool { return less(p.Remove[i].Ref, p.Remove[j].Ref) })
	sort.Slice(p.Skew, func(i, j int) bool { return less(p.Skew[i].Ref, p.Skew[j].Ref) })
	inventory.SortRefs(p.OptedOut)
	return p
}

type retirement struct {
	retired bool
	by      string
	at      time.Time
}

// retirements decides, for every package anyone mentions, whether the latest
// deliberate removal outranks the latest deliberate install.
//
// This comparison is the whole reason retire stamps and install times are
// recorded. Without it two machines deadlock: A uninstalls, B still has it, B
// reinstalls it on A, A uninstalls again — forever, with neither machine wrong.
func retirements(all []*inventory.Machine) map[inventory.Ref]retirement {
	out := map[inventory.Ref]retirement{}
	latestInstall := map[inventory.Ref]time.Time{}

	for _, m := range all {
		for _, r := range m.Installed() {
			pkg, _ := m.Lookup(r)
			if pkg.InstalledAt == 0 {
				continue
			}
			if t := time.Unix(pkg.InstalledAt, 0).UTC(); t.After(latestInstall[r]) {
				latestInstall[r] = t
			}
		}
	}
	for _, m := range all {
		for key, at := range m.Retired {
			r, err := inventory.ParseRef(key)
			if err != nil {
				// A malformed key must not be read as some other package;
				// dropping it only costs a re-proposed install.
				continue
			}
			if cur, ok := out[r]; ok && !at.After(cur.at) {
				continue
			}
			out[r] = retirement{retired: true, by: m.Name, at: at.UTC()}
		}
	}
	for r, ret := range out {
		// A reinstall strictly later than the retire stamp revokes it. Equal
		// timestamps keep the retirement: brew records install time in whole
		// seconds, so a tie is far more likely to be coarse resolution than a
		// genuine same-instant reinstall, and the safe reading of a tie is the
		// one that does not silently reinstall software someone removed.
		if latestInstall[r].After(ret.at) {
			delete(out, r)
		}
	}
	return out
}

func skewOf(r inventory.Ref, versions map[string]string) (Skew, bool) {
	if len(versions) < 2 {
		return Skew{}, false
	}
	best := ""
	for _, v := range versions {
		if v > best {
			best = v
		}
	}
	var behind []string
	for m, v := range versions {
		if v != best {
			behind = append(behind, m)
		}
	}
	if len(behind) == 0 {
		return Skew{}, false
	}
	sort.Strings(behind)
	cp := make(map[string]string, len(versions))
	for k, v := range versions {
		cp[k] = v
	}
	return Skew{Ref: r, Versions: cp, Behind: behind}, true
}

func less(a, b inventory.Ref) bool {
	if a.Kind != b.Kind {
		return a.Kind == inventory.Formula
	}
	return a.Name < b.Name
}

// isDefaultTap reports whether a tap is one brew has by default and so never
// needs `brew tap` run for it.
func isDefaultTap(t string) bool {
	return t == "homebrew/core" || t == "homebrew/cask"
}
