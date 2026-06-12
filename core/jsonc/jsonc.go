// Package jsonc reads JSON-with-comments: `//` line and `/* */` block
// comments plus trailing commas, the dialect used by .changeset/release.jsonc
// and .rig.json. Strip replaces comment bytes with spaces (and trailing commas
// with a space), so byte offsets — and therefore json error positions — are
// preserved. A comment-preserving editor layers on later (rig Phase 6).
package jsonc

import "encoding/json"

// Unmarshal parses JSONC data into v.
func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(Strip(data), v)
}

// Strip returns data with comments and trailing commas blanked to spaces.
// String literals (including escaped quotes) are left untouched. Newlines
// inside block comments are preserved so line numbers stay accurate.
func Strip(data []byte) []byte {
	out := make([]byte, len(data))
	copy(out, data)

	const (
		code = iota
		inString
		lineComment
		blockComment
	)
	state := code
	// lastComma tracks the most recent comma at code level that has seen only
	// whitespace/comments since — if the next code byte closes a container,
	// the comma was trailing.
	lastComma := -1

	for i := 0; i < len(out); i++ {
		c := out[i]
		switch state {
		case code:
			switch {
			case c == '"':
				state = inString
				lastComma = -1
			case c == '/' && i+1 < len(out) && out[i+1] == '/':
				state = lineComment
				out[i], out[i+1] = ' ', ' '
				i++
			case c == '/' && i+1 < len(out) && out[i+1] == '*':
				state = blockComment
				out[i], out[i+1] = ' ', ' '
				i++
			case c == ',':
				lastComma = i
			case c == '}' || c == ']':
				if lastComma >= 0 {
					out[lastComma] = ' '
				}
				lastComma = -1
			case c == ' ' || c == '\t' || c == '\n' || c == '\r':
				// whitespace keeps a pending trailing comma pending
			default:
				lastComma = -1
			}
		case inString:
			switch c {
			case '\\':
				i++ // skip the escaped byte
			case '"':
				state = code
			}
		case lineComment:
			if c == '\n' {
				state = code
			} else if c != '\r' {
				out[i] = ' '
			}
		case blockComment:
			if c == '*' && i+1 < len(out) && out[i+1] == '/' {
				out[i], out[i+1] = ' ', ' '
				i++
				state = code
			} else if c != '\n' && c != '\r' {
				out[i] = ' '
			}
		}
	}
	return out
}
