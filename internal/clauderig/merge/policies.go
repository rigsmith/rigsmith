package merge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	devicesFile  = "clauderig-devices.json"
	manifestFile = "clauderig-manifest.json"
)

// mergeDevices unions the device registry per machine, keeping whichever side
// has the newer lastSync for each.
//
// Note this deliberately reads the spec's "newest timestamp wins" as per-entry,
// not per-file. Each machine only ever touches its own entry (devices.Touch),
// so a whole-file pick would silently delete the other machine's row — the
// exact class of loss the package doc warns about. Per-entry is also the only
// well-defined reading: the file has no single top-level timestamp to compare.
func mergeDevices(s Sides) ([]byte, string, error) {
	var ours, theirs deviceRegistry
	if err := json.Unmarshal(s.Ours, &ours); err != nil {
		return nil, "", err
	}
	if err := json.Unmarshal(s.Theirs, &theirs); err != nil {
		return nil, "", err
	}

	// The common ancestor is what separates "the other side never had this" from
	// "this side removed it". Without it every union re-adds whatever `device
	// remove` just took out, on the next merge, for ever. An unreadable or
	// absent base (an add/add with no ancestor) reads as empty, which is the
	// old union behaviour and the safe direction: keep too much.
	var base deviceRegistry
	if len(s.Base) > 0 {
		_ = json.Unmarshal(s.Base, &base)
	}
	inBaseUnchanged := func(name string, side map[string]json.RawMessage) bool {
		b, ok := base.Devices[name]
		if !ok {
			return false
		}
		cur, ok := side[name]
		return ok && bytes.Equal(normalizeJSON(b), normalizeJSON(cur))
	}

	out := deviceRegistry{Schema: max(ours.Schema, theirs.Schema), Devices: map[string]json.RawMessage{}}
	var added, refreshed, removed int
	for name, d := range ours.Devices {
		// They dropped it and we left it alone: their removal stands.
		if _, still := theirs.Devices[name]; !still && inBaseUnchanged(name, ours.Devices) {
			removed++
			continue
		}
		out.Devices[name] = d
	}
	for name, theirDev := range theirs.Devices {
		ourDev, have := ours.Devices[name]
		if !have {
			// We dropped it. It comes back only if they have since changed it —
			// that machine synced after the removal, so it is a live machine
			// again rather than the row we meant to clear out.
			if inBaseUnchanged(name, theirs.Devices) {
				removed++
				continue
			}
			out.Devices[name] = theirDev
			added++
			continue
		}
		if lastSyncOf(theirDev).After(lastSyncOf(ourDev)) {
			out.Devices[name] = theirDev
			refreshed++
		}
	}

	body, err := marshalIndent(out)
	if err != nil {
		return nil, "", err
	}
	detail := fmt.Sprintf("%d device(s) kept, %d added from remote, %d refreshed to a newer sync",
		len(out.Devices), added, refreshed)
	if removed > 0 {
		detail += fmt.Sprintf(", %d left removed", removed)
	}
	return body, detail, nil
}

// normalizeJSON re-encodes a raw entry so two spellings of the same object —
// different key order or indentation, which is all a re-serialisation changes —
// compare equal. An unparseable entry falls back to its bytes.
func normalizeJSON(raw json.RawMessage) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return b
}

// deviceRegistry mirrors clauderig-devices.json loosely: entries stay raw so an
// unknown field added by a newer clauderig survives the merge untouched.
type deviceRegistry struct {
	Schema  int                        `json:"schema"`
	Devices map[string]json.RawMessage `json:"devices"`
}

func lastSyncOf(raw json.RawMessage) time.Time {
	var d struct {
		LastSync time.Time `json:"lastSync"`
	}
	_ = json.Unmarshal(raw, &d)
	return d.LastSync
}

// mergeManifest merges the project index per key. The conflict is typically
// only claudeVersion — either side is fine — while the projects map has
// auto-merged entries from both machines that a whole-file pick would drop.
func mergeManifest(s Sides) ([]byte, string, error) {
	var ours, theirs manifestDoc
	if err := json.Unmarshal(s.Ours, &ours); err != nil {
		return nil, "", err
	}
	if err := json.Unmarshal(s.Theirs, &theirs); err != nil {
		return nil, "", err
	}

	out := manifestDoc{
		Schema:   max(ours.Schema, theirs.Schema),
		SourceOS: ours.SourceOS,
		Projects: map[string]json.RawMessage{},
	}
	if out.SourceOS == "" {
		out.SourceOS = theirs.SourceOS
	}
	// Either version is acceptable; prefer the higher so the record doesn't go
	// backwards on a machine that happens to lag.
	out.ClaudeVersion = ours.ClaudeVersion
	if versionLess(out.ClaudeVersion, theirs.ClaudeVersion) {
		out.ClaudeVersion = theirs.ClaudeVersion
	}

	for slug, p := range ours.Projects {
		out.Projects[slug] = p
	}
	added := 0
	for slug, p := range theirs.Projects {
		if _, have := out.Projects[slug]; !have {
			out.Projects[slug] = p
			added++
		}
	}

	body, err := marshalIndent(out)
	if err != nil {
		return nil, "", err
	}
	return body, fmt.Sprintf("%d project(s) kept, %d added from remote, claudeVersion %s",
		len(ours.Projects), added, orNone(out.ClaudeVersion)), nil
}

type manifestDoc struct {
	Schema        int                        `json:"schema"`
	ClaudeVersion string                     `json:"claudeVersion,omitempty"`
	SourceOS      string                     `json:"sourceOS"`
	Projects      map[string]json.RawMessage `json:"projects"`
}

// mergeJSONL takes the superset of an append-only transcript: our lines in
// order, then any line the remote has that we don't.
//
// A session resumed on both machines yields a shared prefix and two divergent
// tails; the union keeps both, local side first. Exact-line identity is the
// right key because these files are append-only — a line is never rewritten, so
// two identical lines are the same event.
func mergeJSONL(s Sides) ([]byte, string, error) {
	ourLines := splitLines(s.Ours)
	theirLines := splitLines(s.Theirs)

	seen := make(map[string]bool, len(ourLines))
	out := make([]string, 0, len(ourLines)+len(theirLines))
	for _, l := range ourLines {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	added := 0
	for _, l := range theirLines {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
			added++
		}
	}

	return []byte(strings.Join(out, "\n") + "\n"),
		fmt.Sprintf("%d line(s) kept, %d appended from remote", len(ourLines), added), nil
}

// mergeNewest picks the whole side whose lastUpdated is newer. This is the one
// policy that legitimately takes a side wholesale: these files are caches
// refetched as a unit (the extension blocklist, a Desktop skills manifest), so
// half of one and half of another would be incoherent.
//
// It reads lastUpdated at the top level or one level into an array, and accepts
// both shapes seen in the wild: epoch milliseconds and an RFC3339 string.
func mergeNewest(s Sides) ([]byte, string, error) {
	ourT, ok1 := newestTimestamp(s.Ours)
	theirT, ok2 := newestTimestamp(s.Theirs)
	if !ok1 || !ok2 {
		// No timestamp to compare — this policy has nothing to go on, so the
		// file falls through to a human rather than being decided by a coin.
		return nil, "", fmt.Errorf("merge: no lastUpdated in %s", s.Path)
	}

	if theirT.After(ourT) {
		return s.Theirs, fmt.Sprintf("took the remote copy (%s newer than %s)",
			theirT.UTC().Format(time.RFC3339), ourT.UTC().Format(time.RFC3339)), nil
	}
	return s.Ours, fmt.Sprintf("kept the local copy (%s at least as new as %s)",
		ourT.UTC().Format(time.RFC3339), theirT.UTC().Format(time.RFC3339)), nil
}

// newestTimestamp finds the largest lastUpdated in doc.
func newestTimestamp(doc []byte) (time.Time, bool) {
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return time.Time{}, false
	}

	var best time.Time
	var found bool
	consider := func(m map[string]any) {
		raw, ok := m["lastUpdated"]
		if !ok {
			return
		}
		if t, ok := parseTimestamp(raw); ok && t.After(best) {
			best, found = t, true
		}
	}

	switch typed := v.(type) {
	case map[string]any:
		consider(typed)
	case []any:
		for _, el := range typed {
			if m, ok := el.(map[string]any); ok {
				consider(m)
			}
		}
	}
	return best, found
}

// parseTimestamp accepts epoch milliseconds (Desktop writes a number) or an
// RFC3339 string (the blocklist cache writes one).
func parseTimestamp(raw any) (time.Time, bool) {
	switch t := raw.(type) {
	case float64:
		return time.UnixMilli(int64(t)), true
	case string:
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// marshalIndent writes JSON the way the rest of clauderig does: two-space
// indent, trailing newline, so a merged file is byte-comparable with a
// freshly-written one and doesn't show up as a diff on the next sync.
func marshalIndent(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// splitLines splits on newlines, dropping the trailing empty element and any
// blank lines (a JSONL file has none meaningfully).
func splitLines(b []byte) []string {
	raw := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// versionLess compares dotted versions numerically where it can, falling back
// to string order. Only used to pick the higher claudeVersion, where either
// side is acceptable and this just avoids going backwards.
func versionLess(a, b string) bool {
	if a == "" {
		return b != ""
	}
	if b == "" {
		return false
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := atoi(as[i])
		bn, berr := atoi(bs[i])
		if aerr || berr {
			return a < b
		}
		if an != bn {
			return an < bn
		}
	}
	return len(as) < len(bs)
}

func atoi(s string) (int, bool) {
	n := 0
	if s == "" {
		return 0, true
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, true
		}
		n = n*10 + int(r-'0')
	}
	return n, false
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
