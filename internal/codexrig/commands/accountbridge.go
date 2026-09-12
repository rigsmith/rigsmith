package commands

import (
	"github.com/rigsmith/rigsmith/internal/codexrig/account"
	"github.com/rigsmith/rigsmith/internal/codexrig/appserver"
)

// The command layer reaches the account store through these two names rather
// than calling the package directly, so a test can pin what "the live login is"
// without a real ~/.codex. Deliberately tiny: anything more would be a seam
// pretending to be an abstraction.
var (
	accountReadLive   = account.ReadLive
	accountIdentityOf = account.IdentityOf
	// appserverAvailable gates the few places codexrig asks Codex a question
	// rather than working the answer out, so a machine without the binary gets
	// a quieter report instead of an error about a tool it does not have.
	appserverAvailable = appserver.Available
)
