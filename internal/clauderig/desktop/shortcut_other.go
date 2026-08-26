//go:build !darwin && !windows

package desktop

// No Claude Desktop here, so there is nothing for a shortcut to open. The
// portable layer gates on ShortcutsSupported() before any of this is reached;
// these exist so the package builds and so a listing on this platform is an
// honest empty rather than an error.

func destLabel(Dest) string { return "" }

func shortcutDir(Dest) (string, error) { return "", ErrUnsupported }

func installShortcutIn(string, ShortcutSpec) (Shortcut, error) { return Shortcut{}, ErrUnsupported }

func listShortcutsIn(string) ([]Shortcut, error) { return nil, nil }

func removeShortcutAt(string) error { return ErrUnsupported }
