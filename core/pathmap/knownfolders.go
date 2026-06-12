package pathmap

import "strings"

// KnownFolders resolves a predefined token name ("HOME", …) to an absolute
// directory for the target machine. Predefined tokens are leaves — they never
// reference other tokens — so this is a flat, case-insensitive name→path lookup.
// name is the bare token without the leading '$'.
type KnownFolders interface {
	Resolve(name string) (string, bool)
}

// MapFolders is a static name→path table (case-insensitive). It is how pathmap
// resolves for an *arbitrary target* machine: build MapFolders{"HOME": the
// target's home} and pair it with that machine's OS token, and a portable
// template like "$HOME/Git/x" expands into the target's native path on any host.
//
//	MapFolders{"HOME": `C:\Users\John`}  + os "windows" → C:\Users\John\Git\x
//	MapFolders{"HOME": "/home/john"}     + os "linux"   → /home/john/Git/x
type MapFolders map[string]string

// Resolve looks name up case-insensitively.
func (m MapFolders) Resolve(name string) (string, bool) {
	for k, v := range m {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}
