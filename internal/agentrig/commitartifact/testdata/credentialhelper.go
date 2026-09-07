// Synthetic Git credential helper. Only fixture files are read or written.
package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type fixture struct {
	Input, Output, Mode, Marker, Report string
}

func wait(path string) bool {
	until := time.Now().Add(20 * time.Second)
	for time.Now().Before(until) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func main() {
	signal.Ignore(syscall.SIGPIPE)
	if len(os.Args) == 3 && os.Args[1] == "leaf" {
		marker := os.Args[2]
		if os.WriteFile(marker, nil, 0600) != nil {
			os.Exit(3)
		}
		if wait(marker + ".released") {
			_ = os.WriteFile(marker+".late", nil, 0600)
		}
		return
	}
	if len(os.Args) != 3 || os.Args[2] != "get" {
		os.Exit(4)
	}
	data, err := os.ReadFile(os.Args[1])
	var f fixture
	if err != nil || json.Unmarshal(data, &f) != nil {
		os.Exit(5)
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, 16384))
	if err != nil || string(input) != f.Input {
		os.Exit(6)
	}
	dir, _ := os.Getwd()
	report, _ := json.Marshal(struct {
		Dir       string
		Env, Args []string
	}{dir, os.Environ(), os.Args})
	if os.WriteFile(f.Report, report, 0600) != nil {
		os.Exit(7)
	}
	if f.Mode == "exit" {
		_, _ = io.WriteString(os.Stdout, f.Output)
		_, _ = io.WriteString(os.Stderr, f.Output)
		os.Exit(8)
	}
	if f.Mode == "return" || f.Mode == "wait" || f.Mode == "overflow" {
		self, _ := os.Executable()
		child := exec.Command(self, "leaf", f.Marker)
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if child.Start() != nil || !wait(f.Marker) {
			os.Exit(9)
		}
	}
	if f.Mode == "overflow" {
		for range 32 {
			_, _ = io.WriteString(os.Stdout, strings.Repeat("x", 64<<10))
		}
	} else if f.Mode != "wait" {
		_, _ = io.WriteString(os.Stdout, f.Output)
	}
	if f.Mode == "wait" || f.Mode == "overflow" {
		time.Sleep(25 * time.Second)
	}
}
