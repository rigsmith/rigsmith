// A synthetic Git executable for process-ownership integration tests. It never
// accesses a real repository and all helper lifetimes have an upper bound.
package main

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func wait(path string) bool {
	until := time.Now().Add(15 * time.Second)
	for time.Now().Before(until) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func main() {
	// Exercise helpers that survive stdout rejection instead of exiting on SIGPIPE.
	signal.Ignore(syscall.SIGPIPE)
	marker := os.Getenv("RIG_RETAINED_HELPER_MARKER")
	if len(os.Args) > 1 && os.Args[1] == "leaf" {
		if os.WriteFile(marker, nil, 0600) != nil {
			os.Exit(3)
		}
		if wait(marker + ".released") {
			_ = os.WriteFile(marker+".late", nil, 0600)
		}
		return
	}
	mode := os.Getenv("RIG_RETAINED_HELPER_MODE")
	if strings.HasPrefix(mode, "head-") {
		args := strings.Join(os.Args[1:], " ")
		switch {
		case strings.Contains(args, "--git-path"):
			_, _ = io.WriteString(os.Stdout, "absent\n")
			return
		case strings.Contains(args, "ls-files"):
			return
		case strings.Contains(args, "symbolic-ref"):
			if strings.HasPrefix(mode, "head-symbolic-") {
				mode = strings.TrimPrefix(mode, "head-symbolic-")
			} else {
				_, _ = io.WriteString(os.Stdout, "refs/heads/main\n")
				return
			}
		case strings.Contains(args, "show-ref"):
			if strings.HasPrefix(mode, "head-show-") {
				mode = strings.TrimPrefix(mode, "head-show-")
			} else {
				os.Exit(1)
			}
		default:
			if mode == "head-overflow" {
				mode = "overflow"
			} else {
				os.Exit(128)
			}
		}
		if mode == "cancel" {
			mode = "wait"
		}
	}
	if mode == "exit" {
		os.Exit(1)
	}
	if mode == "copy" {
		if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
			os.Exit(4)
		}
		return
	}
	self, err := os.Executable()
	if err != nil {
		os.Exit(5)
	}
	child := exec.Command(self, "leaf")
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if child.Start() != nil {
		os.Exit(6)
	}
	if !wait(marker) {
		_ = child.Process.Kill()
		os.Exit(7)
	}
	switch mode {
	case "return":
		_, _ = io.WriteString(os.Stdout, "ready\n")
		return
	case "reject":
		_, _ = io.WriteString(os.Stdout, "malformed\n")
	case "overflow":
		block := strings.Repeat("x", 64<<10)
		for range 32 {
			_, _ = io.WriteString(os.Stdout, block)
		}
	case "wait":
	default:
		os.Exit(8)
	}
	time.Sleep(20 * time.Second)
}
