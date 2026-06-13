// Package ecosystem wires the built-in language adapters into a plugin.Registry.
// Each built-in (dotnet, node, gomod) implements plugin.Ecosystem in-process —
// the reference implementation of the same contract external plugins speak.
package ecosystem

import (
	"github.com/rigsmith/core/ecosystem/cargo"
	"github.com/rigsmith/core/ecosystem/dotnet"
	"github.com/rigsmith/core/ecosystem/gomod"
	"github.com/rigsmith/core/ecosystem/node"
	"github.com/rigsmith/core/ecosystem/regex"
	"github.com/rigsmith/core/plugin"
)

// Default returns a registry populated with the built-in adapters. The regex
// adapter is registered last so the native language adapters claim their files
// first; it only activates when the repo declares a "regex" config block.
func Default() *plugin.Registry {
	r := plugin.NewRegistry()
	r.Register(dotnet.New())
	r.Register(node.New())
	r.Register(gomod.New())
	r.Register(cargo.New())
	r.Register(regex.New())
	return r
}
