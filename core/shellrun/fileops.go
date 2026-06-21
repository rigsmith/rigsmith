// Package shellrun provides cross-platform command execution: a direct exec
// runner, an in-process portable shell (mvdan.cc/sh) so the same shell script
// runs identically on Linux, macOS, and Windows, and the pure-Go cp/mv/rm/mkdir
// file operations both of those (and the Tengo script builtins in core/script)
// share.
package shellrun

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"mvdan.cc/sh/v3/interp"
)

// portableFileOps is an mvdan.cc/sh ExecHandlers middleware that implements the
// common file commands (cp, mv, rm, mkdir) in pure Go, so they behave
// identically on Linux, macOS, and Windows without a Unix userland. Anything
// else falls through to the default exec handler (git, npm, gh, …).
//
// Supported flags: cp -r/-R, rm -r/-R/-f, mkdir -p. Unknown flags are accepted
// and ignored (so e.g. `cp -p` copies without preserving timestamps); a caller
// that needs exact coreutils semantics can opt into the system shell.
func portableFileOps(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	ops := map[string]func(string, []string) error{
		"cp":    Cp,
		"mv":    Mv,
		"rm":    Rm,
		"mkdir": Mkdir,
	}
	return func(ctx context.Context, args []string) error {
		if len(args) > 0 {
			if op, ok := ops[args[0]]; ok {
				hc := interp.HandlerCtx(ctx)
				if err := op(hc.Dir, args[1:]); err != nil {
					fmt.Fprintf(hc.Stderr, "%s: %v\n", args[0], err)
					return interp.NewExitStatus(1)
				}
				return nil
			}
		}
		return next(ctx, args)
	}
}

// parseFlags splits leading "-rf"-style flags from operands. A bare "--" ends
// flag parsing.
func parseFlags(args []string) (flags map[rune]bool, operands []string) {
	flags = map[rune]bool{}
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if len(a) >= 2 && a[0] == '-' {
			for _, r := range a[1:] {
				flags[r] = true
			}
			continue
		}
		break
	}
	return flags, args[i:]
}

// resolve makes a (possibly relative) operand absolute against the shell's
// current directory, so file ops honour cd/Dir like a real shell.
func resolve(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// Mkdir creates directories under dir (mkdir -p creates parents). args is the
// coreutils-style operand list (flags then paths).
func Mkdir(dir string, args []string) error {
	flags, ops := parseFlags(args)
	if len(ops) == 0 {
		return fmt.Errorf("missing operand")
	}
	for _, p := range ops {
		path := resolve(dir, p)
		var err error
		if flags['p'] {
			err = os.MkdirAll(path, 0o755)
		} else {
			err = os.Mkdir(path, 0o755)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Rm removes paths under dir (rm -r recurses, rm -f ignores missing).
func Rm(dir string, args []string) error {
	flags, ops := parseFlags(args)
	recursive := flags['r'] || flags['R']
	force := flags['f']
	if len(ops) == 0 {
		if force {
			return nil
		}
		return fmt.Errorf("missing operand")
	}
	for _, p := range ops {
		path := resolve(dir, p)
		// os.RemoveAll is a no-op on a missing path, which would make a typo'd
		// `rm -r missing` succeed silently. Without -f, require the path to exist.
		if recursive && !force {
			if _, err := os.Lstat(path); err != nil {
				return err
			}
		}
		var err error
		if recursive {
			err = os.RemoveAll(path)
		} else {
			err = os.Remove(path)
		}
		if err != nil {
			if force && os.IsNotExist(err) {
				continue
			}
			return err
		}
	}
	return nil
}

// Mv moves sources to a destination under dir. A cross-device rename falls back
// to copy+remove.
func Mv(dir string, args []string) error {
	_, ops := parseFlags(args)
	if len(ops) < 2 {
		return fmt.Errorf("need a source and a destination")
	}
	srcs, dst, dstIsDir, err := sourcesAndDest(dir, ops)
	if err != nil {
		return err
	}
	for _, src := range srcs {
		target := dst
		if dstIsDir {
			target = filepath.Join(dst, filepath.Base(src))
		}
		if err := movePath(src, target); err != nil {
			return err
		}
	}
	return nil
}

// Cp copies sources to a destination under dir (cp -r/-R recurses into
// directories).
func Cp(dir string, args []string) error {
	flags, ops := parseFlags(args)
	recursive := flags['r'] || flags['R']
	if len(ops) < 2 {
		return fmt.Errorf("need a source and a destination")
	}
	srcs, dst, dstIsDir, err := sourcesAndDest(dir, ops)
	if err != nil {
		return err
	}
	for _, src := range srcs {
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		target := dst
		if dstIsDir {
			target = filepath.Join(dst, filepath.Base(src))
		}
		if info.IsDir() {
			if !recursive {
				return fmt.Errorf("%q is a directory (use -r)", src)
			}
			if err := copyTree(src, target); err != nil {
				return err
			}
		} else if err := copyFile(src, target, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

// sourcesAndDest resolves the trailing operand as the destination and the rest
// as sources, all relative to dir. Multiple sources require a directory dest.
func sourcesAndDest(dir string, ops []string) (srcs []string, dst string, dstIsDir bool, err error) {
	dst = resolve(dir, ops[len(ops)-1])
	for _, s := range ops[:len(ops)-1] {
		srcs = append(srcs, resolve(dir, s))
	}
	dstIsDir = isDir(dst)
	if len(srcs) > 1 && !dstIsDir {
		return nil, "", false, fmt.Errorf("target %q is not a directory", ops[len(ops)-1])
	}
	return srcs, dst, dstIsDir, nil
}

// movePath renames src to dst. If os.Rename fails for any reason — most usefully
// a cross-device move, where rename is not possible — it falls back to
// copy+remove; a genuine error (e.g. a missing destination parent) then
// resurfaces from the copy.
func movePath(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyPath(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyTree(src, dst)
	}
	return copyFile(src, dst, info.Mode())
}

// copyTree copies the directory src to dst. Like coreutils, it creates dst
// itself but NOT a missing destination *parent* — so `cp -r src missing/dst`
// fails loudly instead of materializing directories.
func copyTree(src, dst string) error {
	if !isDir(filepath.Dir(dst)) {
		return fmt.Errorf("cannot create directory %q: parent does not exist", dst)
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		}
		return copyFile(path, target, info.Mode())
	})
}

// copyFile copies a single file. Like coreutils, it does NOT create the
// destination's parent directory — the caller (or `mkdir -p`) must. copyTree
// creates the directories it needs as it walks.
func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
