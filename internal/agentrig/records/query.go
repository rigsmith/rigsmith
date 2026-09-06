package records

import (
	"path/filepath"
	"strings"
)

// TitleMatches is a literal title query. Empty queries or titles never match, and casing
// follows the caller's choice. Native content search remains outside this layer.
func TitleMatches(s Summary, query string, caseSensitive bool) bool {
	if s.Title == "" || query == "" {
		return false
	}
	title := s.Title
	if !caseSensitive {
		title, query = strings.ToLower(title), strings.ToLower(query)
	}
	return strings.Contains(title, query)
}

// MatchesText filters the visible session facts. Blank input matches all rows;
// otherwise casing and native path separators are normalized on both sides.
func MatchesText(s Summary, text string) bool {
	text = filepath.ToSlash(strings.ToLower(strings.TrimSpace(text)))
	if text == "" {
		return true
	}
	for _, field := range []string{s.Title, s.LastPrompt, s.Cwd, s.Branch, s.ID, s.Client} {
		if field != "" && strings.Contains(filepath.ToSlash(strings.ToLower(field)), text) {
			return true
		}
	}
	return false
}
