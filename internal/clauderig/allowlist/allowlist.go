// Package allowlist supplies Claude's inclusion policies to the shared walker.
package allowlist

import shared "github.com/rigsmith/rigsmith/internal/agentrig/allowlist"

type Action = shared.Action
type Rule = shared.Rule
type List = shared.List
type Link = shared.Link

const (
	Exclude  = shared.Exclude
	Include  = shared.Include
	anyDepth = "**/"
)

func inc(p string) Rule                                  { return Rule{Pattern: p, Action: Include} }
func exc(p string) Rule                                  { return Rule{Pattern: p, Action: Exclude} }
func Walk(root string, l List) ([]string, []Link, error) { return shared.Walk(root, l) }
