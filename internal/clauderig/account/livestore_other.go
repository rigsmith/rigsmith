//go:build !darwin

package account

// ReadLive returns the file-based live credential (~/.claude/.credentials.json),
// or ErrNoLive when the machine isn't logged in. Off macOS there's no Keychain —
// Claude Code's credential is a file.
func ReadLive() ([]byte, error) {
	raw, found, err := readLiveFile()
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNoLive
	}
	return raw, nil
}

// WriteLive sets the machine-wide live credential (~/.claude/.credentials.json).
func WriteLive(raw []byte) error { return writeLiveFile(raw) }
