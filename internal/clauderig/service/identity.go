package service

import (
	"regexp"

	"github.com/rigsmith/rigsmith/internal/agentrig/redact"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
)

// Identity is the single live-account observation used for this capture.
type Identity struct{ AccountUUID, OrganizationUUID, Email string }

func (s Service) liveIdentity() (Identity, error) {
	if s.ReadIdentity != nil {
		return s.ReadIdentity()
	}
	acct, org, email, err := account.LiveIdentity()
	return Identity{AccountUUID: acct, OrganizationUUID: org, Email: email}, err
}

// emailShape is a deliberately conservative check: one @, no spaces or control
// characters, a dot in the domain. It is not RFC-complete and does not need to
// be — the question here is "could this be something other than an email", and
// anything unusual is worth refusing to publish.
var emailShape = regexp.MustCompile(`^[^\s@\x00-\x1f]{1,128}@[^\s@\x00-\x1f]{1,128}\.[^\s@.\x00-\x1f]{2,63}$`)

// scanIdentity validates the three identity values by SHAPE, returning the
// first that does not fit.
//
// Positive validation, not scanning, and that inversion is the point. Three
// separate findings arrived for three different ways a value slipped past
// redact.ScanFile — multiline values skip its entropy check, oversized ones
// exceed its content cap and return nothing at all, and binary bytes trip its
// binary guard, which returns clean BEFORE secret detection runs. Each was
// patched in turn and a fourth was always available, because asking "does this
// look like a secret" has an open-ended set of ways to answer no.
//
// A uuid and an email have exact shapes. Requiring them ends the class: a value
// that is not one of those two things is refused whatever it happens to be.
func scanIdentity(a *devices.Account) *redact.Finding {
	if a.AccountUUID != "" && account.CanonicalUUID(a.AccountUUID) == "" {
		return &redact.Finding{Path: "accountUuid", Kind: "not a uuid"}
	}
	if a.OrganizationUUID != "" && account.CanonicalUUID(a.OrganizationUUID) == "" {
		return &redact.Finding{Path: "organizationUuid", Kind: "not a uuid"}
	}
	if a.Email != "" && !emailShape.MatchString(a.Email) {
		return &redact.Finding{Path: "email", Kind: "not an email"}
	}
	// Shape-valid values still go through the content rules, so a uuid-shaped
	// string that somehow reads as a known token prefix is still caught.
	for _, f := range []struct{ name, value string }{
		{"accountUuid", a.AccountUUID},
		{"organizationUuid", a.OrganizationUUID},
		{"email", a.Email},
	} {
		if f.value == "" {
			continue
		}
		if found := redact.ScanFile(f.name, []byte(f.value)); len(found) > 0 {
			hit := found[0]
			hit.Path = f.name
			// ScanFile answers a question about a FILE, and it is borrowed here
			// to judge one scalar. Its File verdict does not survive the change
			// of subject: an identity value that looks like a token is a value,
			// and carrying the flag would have a refusal say a file is
			// credential material when no file was examined.
			hit.File = false
			return &hit
		}
	}
	return nil
}
