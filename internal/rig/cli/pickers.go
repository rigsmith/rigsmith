package cli

import "strings"

// Shared bits for the project pickers (the `run` picker and the `rig ui` project
// list): the sort mode (path by default, ecosystem on toggle) and the name
// filter, so both surfaces order and narrow rows identically.

type sortMode int

const (
	sortByPath sortMode = iota // default: group nothing, order by repo path
	sortByEco                  // group by ecosystem, path as the tiebreak
)

func (s sortMode) String() string {
	if s == sortByEco {
		return "ecosystem"
	}
	return "path"
}

// toggle flips between the two sort modes (the `e` key).
func (s sortMode) toggle() sortMode {
	if s == sortByPath {
		return sortByEco
	}
	return sortByPath
}

// rowLess orders two rows under mode: by ecosystem first when sorting by
// ecosystem, then always by path, then name — a total order for a stable list.
func rowLess(mode sortMode, ecoA, pathA, nameA, ecoB, pathB, nameB string) bool {
	if mode == sortByEco && ecoA != ecoB {
		return ecoA < ecoB
	}
	if pathA != pathB {
		return pathA < pathB
	}
	return nameA < nameB
}

// nameMatches reports whether name passes the (case-insensitive substring) name
// filter; an empty query matches everything.
func nameMatches(query, name string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(name), strings.ToLower(query))
}
