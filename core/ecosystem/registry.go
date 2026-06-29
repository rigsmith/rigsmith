// Package ecosystem wires the built-in language adapters into a plugin.Registry.
// Each built-in (dotnet, node, gomod) implements plugin.Ecosystem in-process —
// the reference implementation of the same contract external plugins speak.
package ecosystem

import (
	"github.com/rigsmith/rigsmith/core/ecosystem/cargo"
	"github.com/rigsmith/rigsmith/core/ecosystem/dotnet"
	"github.com/rigsmith/rigsmith/core/ecosystem/electron"
	"github.com/rigsmith/rigsmith/core/ecosystem/gomod"
	"github.com/rigsmith/rigsmith/core/ecosystem/node"
	"github.com/rigsmith/rigsmith/core/ecosystem/regex"
	"github.com/rigsmith/rigsmith/core/ecosystem/tauri"
	"github.com/rigsmith/rigsmith/core/ecosystem/velopack"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// Default returns a registry populated with the built-in adapters. Overlay
// adapters (tauri over cargo, velopack over dotnet) are registered after their
// base; the regex adapter is registered last so the native language adapters
// claim their files first (it only activates when the repo declares a "regex"
// config block). Discovery reconciliation (commands.Workspace.Discover) is
// order-independent, so this ordering is for readability, not correctness.
func Default() *plugin.Registry {
	r := plugin.NewRegistry()
	r.Register(dotnet.New())
	r.Register(node.New())
	r.Register(gomod.New())
	r.Register(cargo.New())
	r.Register(electron.New())
	r.Register(tauri.New())
	r.Register(velopack.New())
	r.Register(regex.New())
	return r
}
