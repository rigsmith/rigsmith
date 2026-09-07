package process

import (
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"strconv"
	"strings"
)

func waitExited(pid int) error {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err != unix.EINTR {
			return err
		}
	}
}

// Linux exposes group membership in /proc. Stream directory entries and bound
// each stat record; zombies have stopped executing even if their new parent has
// not reaped them. The direct child remains waitable until this check completes.
func groupAlive(pid int) (bool, error) {
	dir, err := os.Open("/proc")
	if err != nil {
		return false, err
	}
	defer dir.Close()
	for {
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			if _, err := strconv.Atoi(entry.Name()); err != nil {
				continue
			}
			f, err := os.Open("/proc/" + entry.Name() + "/stat")
			if os.IsNotExist(err) || errors.Is(err, unix.ESRCH) {
				continue
			}
			if err != nil {
				return false, err
			}
			data, err := io.ReadAll(io.LimitReader(f, 4097))
			f.Close()
			if os.IsNotExist(err) || errors.Is(err, unix.ESRCH) {
				continue
			}
			if err != nil {
				return false, err
			}
			if len(data) > 4096 {
				return false, errors.New("oversized process stat")
			}
			end := strings.LastIndexByte(string(data), ')')
			if end < 0 {
				return false, errors.New("invalid process stat")
			}
			fields := strings.Fields(string(data[end+1:]))
			if len(fields) < 3 {
				return false, errors.New("invalid process stat")
			}
			group, err := strconv.Atoi(fields[2])
			if err != nil {
				return false, err
			}
			if group == pid && fields[0] != "Z" && fields[0] != "X" {
				return true, nil
			}
		}
		if readErr == io.EOF {
			return false, nil
		}
		if readErr != nil {
			return false, readErr
		}
	}
}
