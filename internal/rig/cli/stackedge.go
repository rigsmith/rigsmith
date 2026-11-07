package cli

import (
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dustin/go-humanize"
)

// stackEdge is input only: edge configuration is never rewritten by rig.
type stackEdge struct {
	Feed          string             `json:"feed"`
	Label         string             `json:"label,omitempty"`
	Exclude       []string           `json:"exclude,omitempty"`
	MaxSize       string             `json:"maxSize,omitempty"`
	PublishFrom   string             `json:"publishFrom,omitempty"`
	PushKeySecret string             `json:"pushKeySecret,omitempty"`
	Adopted       []stackEdgeAdopted `json:"adopted,omitempty"`
}

type stackEdgeAdopted struct {
	PR     string `json:"pr"`
	Commit string `json:"commit"`
}

// effectiveMaxSize returns bytes. MB is decimal; MiB is binary. An explicit
// positive value overrides the 100 MB default in either direction.
func (e stackEdge) effectiveMaxSize() (uint64, error) {
	if e.MaxSize == "" {
		return 100_000_000, nil
	}
	n, err := humanize.ParseBytes(strings.TrimSpace(e.MaxSize))
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, fmt.Errorf("maxSize must be positive")
	}
	return n, nil
}

var stackEdgeSecretName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func stackEdgeValidSecretName(name string) bool {
	if !stackEdgeSecretName.MatchString(name) {
		return false
	}
	lower := strings.ToLower(name)
	// These token prefixes also happen to satisfy the name grammar. Do not
	// accept a common pasted credential, or echo it in a validation error.
	for _, prefix := range []string{"github_", "ghp_", "gho_", "ghu_", "ghs_", "ghr_"} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	return true
}

// Accept the design's host/owner/repo/pull/n form and a browser's https URL.
// Query strings, fragments and subpaths cannot identify a different PR here.
func stackEdgePRUpstream(raw, upstream string) bool {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(u.Path, "/"), "/")
	if len(parts) != 5 || parts[3] != "pull" || !strings.EqualFold(u.Host+"/"+parts[1]+"/"+parts[2], upstream) {
		return false
	}
	for _, c := range parts[4] {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(parts[4], 10, 64)
	return err == nil && n > 0
}

func (m *stackManifest) validateEdge() error {
	for _, name := range slices.Sorted(maps.Keys(m.Edge)) {
		e := m.Edge[name]
		r := m.Repos[name]
		if r == nil {
			return fmt.Errorf("stack edge %q must name a configured member", name)
		}
		if e == nil {
			return fmt.Errorf("stack edge %q must be an object", name)
		}
		if strings.TrimSpace(r.TrackBranch) == "" {
			return fmt.Errorf("stack edge %q needs trackBranch on its member", name)
		}
		if r.PublishPrefix == "" && len(r.PublishesAs) == 0 {
			return fmt.Errorf("stack edge %q needs publishPrefix or publishesAs on its member", name)
		}
		if strings.TrimSpace(e.Feed) == "" {
			return fmt.Errorf("stack edge %q needs a feed", name)
		}
		if e.PublishFrom != "" {
			if err := stackValidRepoSpec(e.PublishFrom); err != nil {
				return fmt.Errorf("stack edge %q: publishFrom: %w", name, err)
			}
		}
		if err := stackEdgeValidateLabel(e.Label); err != nil {
			return fmt.Errorf("stack edge %q: %w", name, err)
		}
		if e.PushKeySecret != "" && !stackEdgeValidSecretName(e.PushKeySecret) {
			return fmt.Errorf("stack edge %q: pushKeySecret must be a secret NAME (letters, digits and underscores, starting with a letter or underscore), never a credential value", name)
		}
		if _, err := e.effectiveMaxSize(); err != nil {
			return fmt.Errorf("stack edge %q: maxSize must be a byte size such as 50MB: %w", name, err)
		}
		for _, a := range e.Adopted {
			if !stackIsSHA(strings.ToLower(a.Commit)) {
				return fmt.Errorf("stack edge %q: adopted commit must be a full 40-character hex SHA", name)
			}
			if !stackEdgePRUpstream(a.PR, r.Upstream) {
				return fmt.Errorf("stack edge %q: adopted pr must be a PR URL on the member's upstream %s", name, r.Upstream)
			}
		}
	}
	return nil
}

// excludeNotes reports typos against the member's complete discovered original
// ids. Call only after successful discovery, never with a partial scan's ids.
// This seam is also available to edge pack, which will enforce id coverage.
func (e stackEdge) excludeNotes(member string, produced []string) []string {
	var notes []string
	for _, id := range slices.Sorted(slices.Values(e.Exclude)) {
		if !slices.Contains(produced, id) {
			notes = append(notes, fmt.Sprintf("%s excludes %s, but produces no package called %s", member, id, id))
		}
	}
	return slices.Compact(notes)
}
