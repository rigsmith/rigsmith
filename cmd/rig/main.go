// Command rig is rigsmith's convention-first dev launcher: it wraps the everyday
// run/build/test/format loop across ecosystems (.NET, Node, Go) with project
// discovery and an interactive menu. It is the Go successor to the .NET/Node
// `rig` tools, sharing rigsmith/core's ecosystem detection with shiprig.
package main

import (
	"context"
	"os"

	"github.com/rigsmith/rigsmith/internal/rig/cli"
)

func main() {
	if err := cli.Execute(context.Background()); err != nil {
		os.Exit(1)
	}
}
