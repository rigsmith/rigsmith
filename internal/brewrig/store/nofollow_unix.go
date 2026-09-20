//go:build unix

package store

import "syscall"

// syscallNoFollow makes the final open fail rather than follow a symbolic link.
// brewrig ships for darwin and linux only, so this is the only variant needed;
// the build tag keeps `go vet ./...` honest on other platforms rather than
// silently compiling a version with no protection.
const syscallNoFollow = syscall.O_NOFOLLOW
