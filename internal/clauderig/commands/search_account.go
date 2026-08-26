package commands

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
)

// minUUIDPrefix is the shortest accountUuid prefix --account will accept. Eight
// hex characters is the first dash-delimited group, which is how these uuids are
// written when they are abbreviated anywhere else, and it is long enough that a
// collision between two of a person's own accounts is not a practical concern.
const minUUIDPrefix = 8

// resolveAccountFilter turns what someone typed into an accountUuid to match
// ledger rows against.
//
// Three inputs are accepted, in this order: an accountUuid (or a prefix of one),
// an account alias or email from clauderig's own store, and — because a machine
// can hold sessions from an account it has never had a login for — an email
// recorded in the synced device registry.
//
// The uuid is the join key rather than the email because that is what the two
// sources of attribution actually carry: Desktop names the account by uuid in
// its sidecar path, and ~/.claude.json names it by uuid in oauthAccount. An
// email is only ever a label people can type.
//
// A value that resolves to nothing is an ERROR, not an empty result set: "no
// sessions for that account" and "there is no such account" are opposite
// answers, and only one of them means the search worked.
func resolveAccountFilter(input string, stagingDir string, known map[string]ledger.Entry) (string, error) {
	v := strings.TrimSpace(input)
	if v == "" {
		return "", nil
	}

	// Read the registry ONCE. Two scans repeat the I/O and, worse, can see
	// different snapshots if a sync lands between them — so the email branch and
	// the prefix branch could disagree about which accounts exist.
	candidatesByEmail := accountUUIDCandidates(stagingDir)
	byEmail := unambiguousByEmail(candidatesByEmail)

	// uuid or uuid prefix — matched against every account that IS known, which
	// is the ledger's attributions plus the device registry's. Registry-only
	// accounts matter: one that has synced but has no attributed sessions yet
	// is a real account with zero results, and answering "unknown account"
	// there would collapse the very distinction this resolver exists to keep.
	if isHexPrefix(v) && len(v) >= minUUIDPrefix {
		candidates := map[string]bool{}
		for _, e := range known {
			if c := canonicalUUID(e.Account); c != "" {
				candidates[c] = true
			}
		}
		// EVERY registry uuid, not the de-duplicated byEmail map. An email
		// shared by two accounts is dropped from byEmail on purpose — it cannot
		// resolve — but dropping both uuids from PREFIX matching too left those
		// accounts unreachable by any means when they exist only in the registry.
		for _, uuids := range candidatesByEmail {
			for uuid := range uuids {
				if c := canonicalUUID(uuid); c != "" {
					candidates[c] = true
				}
			}
		}
		// Accounts this machine tracks, whether or not any device has synced
		// under them since.
		if st, serr := account.DefaultStore(); serr == nil {
			if all, lerr := st.List(); lerr == nil {
				for _, a := range all {
					if a.AccountUUID != "" {
						candidates[a.AccountUUID] = true
					}
				}
			}
		}
		needle := strings.ToLower(v)
		var hit string
		for uuid := range candidates {
			if !strings.HasPrefix(uuid, needle) {
				continue
			}
			if hit != "" && hit != uuid {
				return "", fmt.Errorf("%q matches more than one account (%s, %s) — use more characters", v, hit, uuid)
			}
			hit = uuid
		}
		if hit != "" {
			return hit, nil
		}
	}

	// alias / email / id from clauderig's account store, mapped to a uuid via
	// the registry (the store keys accounts by email, not uuid).
	if st, serr := account.DefaultStore(); serr == nil {
		a, rerr := st.Resolve(v)
		// A fuzzy input matching several local accounts is an AMBIGUITY, not a
		// miss. Discarding that error reported the account as unknown, which
		// sends the user to check their spelling instead of to disambiguate.
		// Only a genuine no-match continues to the registry fallback.
		// Typed, not text-matched. A raw os.PathError from an unreadable store
		// can contain "not found", and reading that as a miss silently fell
		// through to the registry — answering with a different account, or a
		// generic "unknown account", instead of the storage failure.
		if rerr != nil && !errors.Is(rerr, account.ErrNoSuchAccount) && !errors.Is(rerr, account.ErrNoAccounts) {
			return "", rerr
		}
		if rerr == nil {
			// Store.Resolve returns the FIRST exact-email match, so two tracked
			// accounts sharing an email across organisations resolve silently to
			// one of them. The registry path already refuses an ambiguous email;
			// the store path has to as well, or which account you get depends on
			// listing order.
			if dupes := storeAccountsWithEmail(st, a.Email); dupes > 1 {
				return "", fmt.Errorf("%d accounts share the email %s — name one by alias, id, or accountUuid prefix", dupes, a.Email)
			}
			// The store's own uuid first: it is recorded at capture and stays
			// put. The registry holds only each device's LATEST account, so
			// once every device has synced under a different login it can no
			// longer resolve this one — the store can.
			if c := canonicalUUID(a.AccountUUID); c != "" {
				return c, nil
			}
			if a.Email != "" {
				if uuid := byEmail[strings.ToLower(a.Email)]; uuid != "" {
					return uuid, nil
				}
				return "", fmt.Errorf("account %s is known but its accountUuid has not been recorded — "+
					"run `clauderig account add` while logged in as it (or `clauderig sync` on a machine that is), or pass the uuid", a.Email)
			}
		}
	}

	// an email straight from the registry, for an account this machine has no
	// login for at all
	if uuid := byEmail[strings.ToLower(v)]; uuid != "" {
		return uuid, nil
	}

	return "", fmt.Errorf("unknown account %q — %s", v, knownAccountsHint(byEmail, known))
}

// accountUUIDsByEmail maps a lowercased email to its accountUuid, from the
// synced device registry — which records both halves for every machine that has
// synced (see devices.Account).
func accountUUIDsByEmail(stagingDir string) map[string]string {
	return unambiguousByEmail(accountUUIDCandidates(stagingDir))
}

// unambiguousByEmail keeps only the emails that name exactly one account.
func unambiguousByEmail(all map[string]map[string]bool) map[string]string {
	out := make(map[string]string, len(all))
	for email, uuids := range all {
		// One candidate only. The same email can belong to two accounts in
		// different organisations, and keeping whichever device happened to be
		// iterated last would resolve the filter to the wrong one — silently,
		// and differently between runs, since map order is not stable.
		if len(uuids) == 1 {
			for u := range uuids {
				out[email] = u
			}
		}
	}
	return out
}

// accountUUIDCandidates maps a lowercased email to every accountUuid the synced
// registry has recorded for it.
func accountUUIDCandidates(stagingDir string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	if stagingDir == "" {
		return out
	}
	reg, err := devices.Load(stagingDir)
	if err != nil {
		return out
	}
	for _, d := range reg.Devices {
		if d.Account == nil || d.Account.Email == "" || d.Account.AccountUUID == "" {
			continue
		}
		c := canonicalUUID(d.Account.AccountUUID)
		if c == "" {
			continue // a malformed uuid is not an account this can select
		}
		email := strings.ToLower(d.Account.Email)
		if out[email] == nil {
			out[email] = map[string]bool{}
		}
		out[email][c] = true
	}
	return out
}

// knownAccountsHint lists what --account could have matched, so a failed lookup
// is actionable. It names emails where they are known and falls back to the
// uuids the ledger carries, which is all there is for an account no machine has
// synced under.
func knownAccountsHint(byEmail map[string]string, known map[string]ledger.Entry) string {
	labels := map[string]string{}
	for _, e := range known {
		if e.Account != "" {
			labels[e.Account] = shortID(e.Account)
		}
	}
	for email, uuid := range byEmail {
		labels[uuid] = email
	}
	if len(labels) == 0 {
		return "no session in the ledger records an account yet (attribution starts at the next sync)"
	}
	var out []string
	for _, l := range labels {
		out = append(out, l)
	}
	sort.Strings(out)
	return "known: " + strings.Join(out, ", ")
}

// canonicalUUID delegates to the account package, which owns the definition —
// the same normalisation has to apply where uuids are STORED as where they are
// compared, or one account becomes two.
func canonicalUUID(v string) string { return account.CanonicalUUID(v) }

func isHexPrefix(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F', r == '-':
		default:
			return false
		}
	}
	return s != ""
}

// storeAccountsWithEmail counts tracked accounts carrying this email.
//
// Store.Resolve returns the first exact-email match, so two accounts sharing an
// email across organisations resolve to whichever the listing happened to put
// first. The registry path already refuses an ambiguous email; without this the
// store path would answer one, confidently and arbitrarily.
func storeAccountsWithEmail(st *account.Store, email string) int {
	if email == "" {
		return 0
	}
	all, err := st.List()
	if err != nil {
		return 0
	}
	n := 0
	for _, a := range all {
		if strings.EqualFold(a.Email, email) {
			n++
		}
	}
	return n
}
