package commands

import (
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

	byEmail := accountUUIDsByEmail(stagingDir)

	// uuid or uuid prefix — matched against every account that IS known, which
	// is the ledger's attributions plus the device registry's. Registry-only
	// accounts matter: one that has synced but has no attributed sessions yet
	// is a real account with zero results, and answering "unknown account"
	// there would collapse the very distinction this resolver exists to keep.
	if isHexPrefix(v) && len(v) >= minUUIDPrefix {
		candidates := map[string]bool{}
		for _, e := range known {
			if e.Account != "" {
				candidates[e.Account] = true
			}
		}
		for _, uuid := range byEmail {
			candidates[uuid] = true
		}
		var hit string
		for uuid := range candidates {
			if !strings.HasPrefix(strings.ToLower(uuid), strings.ToLower(v)) {
				continue
			}
			if hit != "" && !strings.EqualFold(hit, uuid) {
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
		if a, rerr := st.Resolve(v); rerr == nil && a.Email != "" {
			if uuid := byEmail[strings.ToLower(a.Email)]; uuid != "" {
				return uuid, nil
			}
			return "", fmt.Errorf("account %s is known but no synced machine has recorded its accountUuid yet — "+
				"run `clauderig sync` on a machine logged in as it, or pass the uuid", a.Email)
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
	out := map[string]string{}
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
		out[strings.ToLower(d.Account.Email)] = d.Account.AccountUUID
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
