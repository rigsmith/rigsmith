//go:build !darwin && !linux

package files

import "os"

func openSourceFile(root *os.Root, name string) (*os.File, error) {
	// os.Root confines resolution; Source.Read rechecks regular-file identity and
	// the named non-link before reading bytes. Direct-child names exclude device
	// namespaces. This is not a claim of hostile-writer transaction isolation.
	return root.Open(name)
}
