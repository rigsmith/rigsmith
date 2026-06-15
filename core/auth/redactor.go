package auth

import (
	"sort"
	"strings"
)

// RedactPlaceholder replaces every registered secret in redacted text.
const RedactPlaceholder = "***"

// Redactor accumulates resolved secret values and replaces them in any text the
// caller is about to print (an error wrapping a registry's stderr, a dry-run
// line). Longest values are replaced first so a secret that contains another is
// fully covered. It implements Masker, so it can be passed straight to Resolve.
//
// This is the standalone counterpart to the pipeline's SecretMasker, living in
// core so the publish command (which does not run the pipeline) can redact too.
type Redactor struct {
	secrets []string
}

// NewRedactor returns an empty Redactor.
func NewRedactor() *Redactor { return &Redactor{} }

// Add registers a value to redact. Empty/whitespace values and duplicates are
// ignored.
func (r *Redactor) Add(value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	for _, s := range r.secrets {
		if s == value {
			return
		}
	}
	r.secrets = append(r.secrets, value)
	sort.SliceStable(r.secrets, func(i, j int) bool {
		return len(r.secrets[i]) > len(r.secrets[j])
	})
}

// Redact returns text with every registered secret replaced by the placeholder.
func (r *Redactor) Redact(text string) string {
	for _, s := range r.secrets {
		text = strings.ReplaceAll(text, s, RedactPlaceholder)
	}
	return text
}
