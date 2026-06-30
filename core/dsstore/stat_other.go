//go:build !darwin

package dsstore

import "errors"

// statInfo is darwin-only: generating a dmg .DS_Store requires reading HFS+/APFS
// file metadata, and Write is only ever called while building on macOS.
func statInfo(path string) (ino uint32, birth int64, err error) {
	return 0, 0, errors.New("dsstore: .DS_Store generation is only supported on darwin")
}
