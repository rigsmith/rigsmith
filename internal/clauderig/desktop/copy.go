package desktop

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// sameDir reports whether two paths name the same directory, following the
// links and case-folding the OS applies — a string compare is not enough when
// one side came from a config file and the other from the platform default.
func sameDir(a, b string) bool {
	if a == b {
		return true
	}
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// copyFile copies src to dst without ever overwriting an existing file,
// reporting whether it created one.
//
// O_EXCL makes "never overwrite" a filesystem guarantee rather than a check a
// concurrent writer could race past: seeding runs against a directory Claude
// Desktop may already have started writing into.
func copyFile(src, dst string, mode os.FileMode) (created bool, err error) {
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, err
	}
	if _, cerr := io.Copy(out, in); cerr != nil {
		out.Close()
		_ = os.Remove(dst)
		return false, cerr
	}
	// A delayed write error surfaces at Close; leaving the partial file behind
	// would make it look seeded on the next run.
	if cerr := out.Close(); cerr != nil {
		_ = os.Remove(dst)
		return false, cerr
	}
	return true, nil
}
