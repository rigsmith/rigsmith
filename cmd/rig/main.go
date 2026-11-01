// Command rig is rigsmith's convention-first dev launcher: it wraps the everyday
// run/build/test/format loop across ecosystems (.NET, Node, Go) with project
// discovery and an interactive menu. It is the Go successor to the .NET/Node
// `rig` tools, sharing rigsmith/core's ecosystem detection with shiprig.
package main

import (
	"context"
	"errors"
	"os"

	"github.com/rigsmith/rigsmith/core/completion"
	"github.com/rigsmith/rigsmith/internal/rig/cli"
)

func main() {
	if err := completion.Run(os.Args[1:], "rig", os.Stdout, func() error {
		return cli.Execute(context.Background())
	}); err != nil {
		var code interface{ ExitCode() int }
		if errors.As(err, &code) {
			os.Exit(code.ExitCode())
		}
		os.Exit(1)
	}
}
