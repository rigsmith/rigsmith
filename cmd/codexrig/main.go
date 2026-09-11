// Command codexrig provides the independent Codex frontend (v2 preview).
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/rigsmith/rigsmith/internal/codexrig/commands"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := commands.NewRootCmd(version).ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
