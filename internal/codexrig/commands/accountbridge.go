package commands

import "github.com/rigsmith/rigsmith/internal/codexrig/account"

// The command layer reaches the account store through these two names rather
// than calling the package directly, so a test can pin what "the live login is"
// without a real ~/.codex. Deliberately tiny: anything more would be a seam
// pretending to be an abstraction.
var (
	accountReadLive   = account.ReadLive
	accountIdentityOf = account.IdentityOf
)
