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

// Off macOS a session profile's tokens live in its .credentials.json file —
// there is no per-profile Keychain entry to read or update.
func platformSessionKeychainRead(string) ([]byte, bool, error) { return nil, false, nil }
func platformSessionKeychainWrite(string, []byte) (bool, error) {
	return false, nil
}
