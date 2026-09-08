//go:build ignore

// A test-only Git proxy: production errors intentionally omit raw diagnostics.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

type limitedDiagnostics struct{ bytes.Buffer }

func (b *limitedDiagnostics) Write(p []byte) (int, error) {
	n := len(p)
	if left := 16<<10 - b.Len(); left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func main() {
	cmd := exec.Command(os.Getenv("RIG_TEST_REAL_GIT"), os.Args[1:]...)
	var stderr limitedDiagnostics
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, &stderr
	if err := cmd.Run(); err != nil {
		if f, openErr := os.OpenFile(os.Getenv("RIG_TEST_GIT_DIAGNOSTICS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); openErr == nil {
			dir, _ := os.Getwd()
			_, _ = fmt.Fprintf(f, "dir=%s args=%q error=%v\n%s\n", dir, os.Args[1:], err, stderr.Bytes())
			_ = f.Close()
		}
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}
