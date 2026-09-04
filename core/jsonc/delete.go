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
	start, end := memberSpan(orig, stripped, nameStart, valueEnd)
	return string(orig[:start]) + string(orig[end:]), true
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

// memberSpan widens [nameStart, valueEnd) to the bytes that should leave with
// the member: its separating comma, the rest of its line, and the comment
// lines attached above it. It works on the stripped copy, where comments are
// blanks, so "only whitespace" there means "whitespace or comment" in the
// original.
func memberSpan(orig, stripped []byte, nameStart, valueEnd int) (start, end int) {
	start, end = nameStart, valueEnd
	// A following comma is this member's; take it and the rest of its line.
	i := end
	for i < len(stripped) && isSpace(stripped[i]) && stripped[i] != '\n' {
		i++
	}
	if i < len(stripped) && stripped[i] == ',' {
		end = i + 1
		j := end
		for j < len(stripped) && isSpace(stripped[j]) && stripped[j] != '\n' {
			j++
		}
		if j < len(stripped) && stripped[j] == '\n' {
			end = j + 1
			start = lineStartIfBlankBefore(stripped, start)
			start = absorbCommentLinesAbove(orig, stripped, start)
		} else if j == len(stripped) {
			end = j
		} else {
			end = j // single-line object: eat the spaces up to the next member
		}
		return start, end
	}
	// Last member: the comma before it is the one that goes.
	k := start - 1
	for k >= 0 && isSpace(stripped[k]) {
		k--
	}
	if k >= 0 && stripped[k] == ',' {
		start = k
		// Keep a trailing comment on the previous member's line — the comma
		// sits before it, so only the comma itself is removed from that line.
		return start, end
	}
	// Only member: leave the braces with whatever whitespace framed it.
	start = lineStartIfBlankBefore(stripped, start)
	start = absorbCommentLinesAbove(orig, stripped, start)
	return start, end
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
