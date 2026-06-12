// Package pathmap expands raw path templates ($HOME/Git, ~/code, $DROPBOX/x)
// into absolute paths for a chosen OS, and is the engine behind clauderig's
// cross-machine path correction. Ported from halyard's Favorites path system
// (PathResolution / PathCascade / PathTokenResolver / IKnownFolders), with one
// deliberate change: halyard always resolved for the *host* OS, whereas pathmap
// resolves for an *arbitrary target* OS so a template captured on macOS can be
// expanded into a Windows path (and vice-versa) on any host — the whole point of
// translating a synced session from one machine's layout to another's.
package pathmap

// Status is the outcome kind of a template expansion.
type Status int

const (
	// StatusResolved means the template fully expanded to a rooted path.
	StatusResolved Status = iota
	// StatusUnconfigured means an entry or a referenced token has no value for
	// this OS. When MissingToken is set, that token needs a path; when empty, the
	// entry itself is unconfigured here.
	StatusUnconfigured
	// StatusCycle means a token reference forms a loop (Token names a member).
	StatusCycle
	// StatusInvalid means the template references an undefined token (Token) or
	// expands to a non-rooted path.
	StatusInvalid
)

// Resolution is the immutable result of expanding a raw template. It carries
// just enough to drive use (the Path) and to surface unconfigured/error states
// (which token is missing or cyclic).
type Resolution struct {
	Status       Status
	Path         string
	MissingToken string
	Token        string
}

// IsResolved reports whether the template fully expanded.
func (r Resolution) IsResolved() bool { return r.Status == StatusResolved }

// Resolved builds a successful resolution.
func Resolved(path string) Resolution { return Resolution{Status: StatusResolved, Path: path} }

// Unconfigured builds an unconfigured resolution; missingToken may be empty when
// the entry itself (not a referenced token) is the thing that's unset here.
func Unconfigured(missingToken string) Resolution {
	return Resolution{Status: StatusUnconfigured, MissingToken: missingToken}
}

// Cycle builds a cyclic-reference resolution naming a member of the loop.
func Cycle(token string) Resolution { return Resolution{Status: StatusCycle, Token: token} }

// Invalid builds an invalid resolution; token may name the undefined reference.
func Invalid(token string) Resolution { return Resolution{Status: StatusInvalid, Token: token} }
