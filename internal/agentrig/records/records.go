// Package records coordinates session summaries without reading native files or
// choosing a persisted schema. Each store belongs to one vendor; IDs are opaque.
package records

import "time"

// Summary is an in-memory view, never a replacement ledger or wire format.
// Project is a vendor's project identifier; Cwd is the recorded/resolved path
// supplied by that reader. Provenance describes evidence, not inferred ownership.
type Summary struct {
	Vendor, ID, Title, Project, Cwd     string
	LastPrompt, Branch, Client, Profile string
	Activity                            time.Time
	Approximate                         bool
	Bytes                               int64
	Sources                             []string
	RecordedBy                          string
	Seen                                time.Time
	Account, AccountSource              string
	AccountSince                        time.Time
}

// Store retains its vendor's serialization, union and attribution policies.
// One instance serves one vendor's ID namespace; shared code never parses IDs.
type Store interface {
	Fresh(id string, activity time.Time, bytes int64) bool
	Note(Summary) bool
	Revoke(id string) bool
	Count() int
	Save() error
}

// Candidate supplies cheap identity/fingerprint facts before optional full reads.
// Refresh is selected by vendor policy (for example better attribution evidence).
// Revoke skips hydration and recording even when no stored attribution changes.
// Read enriches a summary only when stale or explicitly selected for refresh.
type Candidate struct {
	Summary         Summary
	Refresh, Revoke bool
	Read            func(*Summary) error
}

type RecordResult struct{ Added, Total int }

// Record visits candidates in reader order and saves only after a successful
// enumeration. Prior in-memory mutations are reflected in counts on an error;
// the caller's store determines write atomicity. Fresh records need no hydration.
func Record(store Store, visit func(func(Candidate) error) error) (RecordResult, error) {
	var result RecordResult
	err := visit(func(c Candidate) error {
		if c.Revoke {
			if store.Revoke(c.Summary.ID) {
				result.Added++
			}
			return nil
		}
		if !c.Refresh && store.Fresh(c.Summary.ID, c.Summary.Activity, c.Summary.Bytes) {
			return nil
		}
		if c.Read != nil {
			if err := c.Read(&c.Summary); err != nil {
				return err
			}
		}
		if store.Note(c.Summary) {
			result.Added++
		}
		return nil
	})
	result.Total = store.Count()
	if err != nil {
		return result, err
	}
	return result, store.Save()
}
