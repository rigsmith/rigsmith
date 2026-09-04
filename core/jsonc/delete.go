package jsonc

import (
	"encoding/json"
	"strings"
)

// Delete removes the member at path (any depth) from the document,
// preserving everything else byte-for-byte: comments, formatting, key order.
// The member's own line goes with it — including a trailing comment on that
// line and any full-line `//` comments directly above it, which Set treats as
// attached to their member and which would otherwise be left describing
// nothing. A blank line stops that absorption.
//
// It returns the edited document and true on success. A path that is not
// present is a success that changes nothing. False means the document could
// not be edited safely — malformed input, an empty path, a root or parent that
// is not an object — and the input is returned unchanged.
func Delete(text string, path []string) (string, bool) {
	original := text
	text = strings.TrimPrefix(text, "\uFEFF")
	if len(path) == 0 {
		return original, false
	}
	orig := []byte(text)
	stripped := Strip(orig)
	if !json.Valid(stripped) {
		return original, false
	}
	nameStart, valueEnd, found, ok := locateMember(stripped, path)
	if !ok {
		return original, false
	}
	if !found {
		return text, true
	}
	out := orig
	for _, sp := range memberSpans(orig, stripped, nameStart, valueEnd) {
		out = append(append([]byte{}, out[:sp[0]]...), out[sp[1]:]...)
	}
	return string(out), true
}

// locateMember finds the byte span of a member's name and value at any
// depth. found is false when the path is absent; ok is false when the
// document's shape makes the question unanswerable (a non-object root, or a
// path segment whose value is not an object).
//
// depth counts open containers, so the root object is depth 1. matched is how
// many leading path segments are currently open: inside the object that is
// path[k-1]'s value, matched == k, and that object sits at depth k+1. A
// property is therefore a candidate only at depth matched+1, which keeps a
// same-named key inside some unrelated nested object from matching.
func locateMember(stripped []byte, path []string) (nameStart, valueEnd int, found, ok bool) {
	depth, matched := 0, 0
	awaitingParent, awaitingValue := false, false
	rootIsObject := false
	pendingName := -1
	s := scanner{data: stripped}
	for {
		t, tok := s.next()
		if !tok {
			break
		}
		if awaitingValue {
			end := t.end
			if t.kind == tokStartObject || t.kind == tokStartArray {
				end = s.skipContainer()
			}
			return pendingName, end, true, true
		}
		if awaitingParent {
			awaitingParent = false
			if t.kind != tokStartObject {
				return 0, 0, false, false // a segment's value is not an object
			}
			depth++
			matched++
			continue
		}
		switch t.kind {
		case tokStartObject:
			if depth == 0 {
				rootIsObject = true
			}
			depth++
		case tokStartArray:
			depth++
		case tokEndObject, tokEndArray:
			depth--
			// Leaving the innermost matched object drops it from the chain.
			if depth <= matched {
				matched = depth - 1
				if matched < 0 {
					matched = 0
				}
			}
		case tokPropertyName:
			if depth == matched+1 && propertyNameEquals(t.raw, path[matched]) {
				if matched+1 == len(path) {
					pendingName = t.start
					awaitingValue = true
				} else {
					awaitingParent = true
				}
			}
		}
	}
	if !rootIsObject {
		return 0, 0, false, false
	}
	return 0, 0, false, true
}

// memberSpans is the byte ranges that leave with the member, in descending
// order so the caller can cut them one after another: the member's own line
// (its value, a trailing comment on the same line, and the comment lines
// attached above it) and, for a last member, the comma that separated it from
// the one before — cut separately, so a trailing comment on THAT member's line
// is kept. It reads the stripped copy, where comments are blanks, so "only
// whitespace" there means "whitespace or comment" in the original; the one
// exception is a legal JSONC trailing comma after the value, which Strip has
// blanked too and only the original still shows.
func memberSpans(orig, stripped []byte, nameStart, valueEnd int) [][2]int {
	start, end := nameStart, valueEnd
	// A comma after the value is this member's — whether a separator the
	// stripped copy shows, or a trailing comma only the original does.
	i := end
	for i < len(orig) && isSpace(orig[i]) && orig[i] != '\n' {
		i++
	}
	comma := i < len(orig) && orig[i] == ','
	if !comma {
		i = end
		for i < len(stripped) && isSpace(stripped[i]) && stripped[i] != '\n' {
			i++
		}
		comma = i < len(stripped) && stripped[i] == ','
	}
	if comma {
		end = i + 1
		j := end
		for j < len(stripped) && isSpace(stripped[j]) && stripped[j] != '\n' {
			j++
		}
		switch {
		case j < len(stripped) && stripped[j] == '\n':
			end = j + 1
			start = lineStartIfBlankBefore(stripped, start)
			start = absorbCommentLinesAbove(orig, stripped, start)
		default:
			end = j // single-line object: eat the spaces up to the next member
		}
		return [][2]int{{start, end}}
	}
	// Last member: the comma before it is the one that goes. On its own line
	// the member's line is cut whole and the comma alone from the line above;
	// on one line with the previous member, everything from the comma on.
	k := start - 1
	for k >= 0 && isSpace(stripped[k]) {
		k--
	}
	if k >= 0 && stripped[k] == ',' {
		if hasNewline(stripped, k, start) {
			lineStart := absorbCommentLinesAbove(orig, stripped, lineStartIfBlankBefore(stripped, start))
			lineEnd := end
			for lineEnd < len(stripped) && isSpace(stripped[lineEnd]) && stripped[lineEnd] != '\n' {
				lineEnd++
			}
			if lineEnd < len(stripped) && stripped[lineEnd] == '\n' {
				lineEnd++
			}
			return [][2]int{{lineStart, lineEnd}, {k, k + 1}}
		}
		return [][2]int{{k, end}}
	}
	// Only member: leave the braces with whatever whitespace framed it.
	start = lineStartIfBlankBefore(stripped, start)
	start = absorbCommentLinesAbove(orig, stripped, start)
	return [][2]int{{start, end}}
}

// lineStartIfBlankBefore moves pos back to the start of its line when nothing
// but whitespace precedes it there, so the whole line is removed rather than a
// hole left in it.
func lineStartIfBlankBefore(stripped []byte, pos int) int {
	i := pos
	for i > 0 && stripped[i-1] != '\n' {
		if !isSpace(stripped[i-1]) {
			return pos
		}
		i--
	}
	return i
}

// absorbCommentLinesAbove extends lineStart upward over lines that are comments
// in the original and blank in the stripped copy. A truly blank line ends the
// run: it is a paragraph break, not part of the member.
func absorbCommentLinesAbove(orig, stripped []byte, lineStart int) int {
	for lineStart > 0 {
		prevEnd := lineStart - 1 // the '\n' ending the previous line
		if prevEnd < 0 || stripped[prevEnd] != '\n' {
			return lineStart
		}
		prevStart := prevEnd
		for prevStart > 0 && stripped[prevStart-1] != '\n' {
			prevStart--
		}
		line := stripped[prevStart:prevEnd]
		if strings.TrimSpace(string(line)) != "" {
			return lineStart // real content above
		}
		if strings.TrimSpace(string(orig[prevStart:prevEnd])) == "" {
			return lineStart // genuinely blank
		}
		if !strings.HasPrefix(strings.TrimSpace(string(orig[prevStart:prevEnd])), "//") {
			return lineStart // a block comment; leave it
		}
		lineStart = prevStart
	}
	return lineStart
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }
