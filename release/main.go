// Command relrig is rigsmith's release tool: a uniform changeset → version →
// publish workflow that runs the same engine across every ecosystem and
// delegates only the ecosystem-specific parts (publish, workspace-graph) to the
// native package managers. It is the Go successor to net-changesets.
package main

import (
	"context"
	"os"

	"github.com/rigsmith/release/internal/cli"
)

func main() {
	if err := cli.Execute(context.Background()); err != nil {
		os.Exit(1)
	}
}
