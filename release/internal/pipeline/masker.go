package pipeline

import (
	"sort"
	"strings"
)

// MaskPlaceholder replaces every registered secret in masked text.
const MaskPlaceholder = "***"

// SecretMasker accumulates captured secret values (e.g. an OTP from vars) and
// redacts them from any text the pipeline shows — command lines and captured
// output — so a secret injected into a step never appears in logs or the
// dry-run plan. Longest values are masked first so a secret that contains
// another is fully covered.
type SecretMasker struct {
	secrets []string
}

// NewSecretMasker returns an empty masker.
func NewSecretMasker() *SecretMasker { return &SecretMasker{} }

// Add registers a value to redact. Empty/whitespace values are ignored
// (nothing to hide), as are duplicates.
func (m *SecretMasker) Add(value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	for _, secret := range m.secrets {
		if secret == value {
			return
		}
	}
	m.secrets = append(m.secrets, value)
	sort.SliceStable(m.secrets, func(i, j int) bool {
		return len(m.secrets[i]) > len(m.secrets[j])
	})
}

// Mask returns text with every registered secret replaced by the placeholder.
func (m *SecretMasker) Mask(text string) string {
	for _, secret := range m.secrets {
		text = strings.ReplaceAll(text, secret, MaskPlaceholder)
	}
	return text
}
