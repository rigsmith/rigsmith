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
	// A byte-order mark is set aside for the edit and put back after: the
	// tokenizer does not expect it, and "everything else byte-for-byte"
	// includes it.
	bom := ""
	if strings.HasPrefix(text, "\uFEFF") {
		bom, text = "\uFEFF", strings.TrimPrefix(text, "\uFEFF")
	}
	if len(path) == 0 {
		return original, false
	}
	orig := []byte(text)
	// A block comment left open swallows the rest of the document, and the
	// stripped copy of that can still read as valid JSON: the edit would then
	// be made around structure that is not really there.
	stripped, unterminated := strip(orig)
	if unterminated || !json.Valid(stripped) {
		return original, false
	}
	nameStart, valueEnd, found, ok := locateMember(stripped, path)
	if !ok {
		return original, false
	}
	if !found {
		return original, true
	}
	out := orig
	for _, sp := range memberSpans(orig, stripped, nameStart, valueEnd) {
		out = append(append([]byte{}, out[:sp[0]]...), out[sp[1]:]...)
	}
	return bom + string(out), true
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
	// A comma after the value on its line is this member's — whether a
	// separator the stripped copy shows, or a trailing comma only the
	// original does.
	i := skipInline(orig, end)
	comma := i < len(orig) && orig[i] == ','
	if !comma {
		i = skipInline(stripped, end)
		comma = i < len(stripped) && stripped[i] == ','
	}
	if comma {
		end = i + 1
		j := skipInline(stripped, end)
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
	// A separator on a later line — comma-first style — is this member's as
	// well. A member whose own comma heads its line goes with that line, and
	// the member after it keeps the comma it has; the first member, which has
	// no comma before it, takes the one after — separately, with the spaces
	// after it, so the member it introduced keeps its indentation, or with
	// its whole line when it had one to itself.
	if c := skipSpace(stripped, end); c < len(stripped) && stripped[c] == ',' {
		if k := commaBeforeOnLine(stripped, start); k >= 0 {
			if ls := lineStartIfBlankBefore(stripped, k); ls == 0 || stripped[ls-1] == '\n' {
				return [][2]int{{absorbCommentLinesAbove(orig, stripped, ls), lineEndAfter(stripped, end)}}
			}
			return [][2]int{{k, trailingCommentEnd(orig, stripped, end)}}
		}
		commaStart, commaEnd := c, skipInline(stripped, c+1)
		if ls := lineStartIfBlankBefore(stripped, c); (ls == 0 || stripped[ls-1] == '\n') && commaEnd < len(stripped) && stripped[commaEnd] == '\n' {
			commaStart, commaEnd = ls, commaEnd+1
		}
		lineStart := absorbCommentLinesAbove(orig, stripped, lineStartIfBlankBefore(stripped, start))
		return [][2]int{{commaStart, commaEnd}, {lineStart, lineEndAfter(stripped, end)}}
	}
	// Last member: the comma before it is the one that goes. On its own line
	// the member's line is cut whole and the comma alone from the line above;
	// on one line with the previous member, everything from the comma on —
	// its trailing comment included, which would otherwise be left reading
	// as the previous member's.
	k := start - 1
	for k >= 0 && isSpace(stripped[k]) {
		k--
	}
	if k >= 0 && stripped[k] == ',' {
		if hasNewline(stripped, k, start) {
			lineStart := absorbCommentLinesAbove(orig, stripped, lineStartIfBlankBefore(stripped, start))
			return [][2]int{{lineStart, lineEndAfter(stripped, end)}, {k, k + 1}}
		}
		return [][2]int{{k, trailingCommentEnd(orig, stripped, end)}}
	}
	// Only member: leave the braces with whatever whitespace framed it. Its
	// trailing comment goes with it, and so does its line ending when the
	// member had the line to itself.
	start = lineStartIfBlankBefore(stripped, start)
	start = absorbCommentLinesAbove(orig, stripped, start)
	j := skipInline(stripped, end)
	// Blank in the stripped copy but not in the original: a comment.
	comment := strings.TrimSpace(string(orig[end:j])) != ""
	switch {
	case j < len(stripped) && stripped[j] == '\n':
		end = j
		if start == 0 || stripped[start-1] == '\n' {
			end++
		}
	case comment:
		end = j
	}
	return [][2]int{{start, end}}
}

// commaBeforeOnLine is the index of a comma that precedes pos on its own
// line with only whitespace or comments between, or -1.
func commaBeforeOnLine(stripped []byte, pos int) int {
	k := pos - 1
	for k >= 0 && isSpace(stripped[k]) && stripped[k] != '\n' {
		k--
	}
	if k >= 0 && stripped[k] == ',' {
		return k
	}
	return -1
}

// lineEndAfter is the index just past the line break that ends the line
// holding end, or past whatever whitespace follows end when there is none.
func lineEndAfter(stripped []byte, end int) int {
	j := skipInline(stripped, end)
	if j < len(stripped) && stripped[j] == '\n' {
		j++
	}
	return j
}

// trailingCommentEnd extends end over a comment that finishes the line — blank
// in the stripped copy, not in the original — and leaves it alone when
// anything else follows on the line.
func trailingCommentEnd(orig, stripped []byte, end int) int {
	j := skipInline(stripped, end)
	if (j >= len(stripped) || stripped[j] == '\n') && strings.TrimSpace(string(orig[end:j])) != "" {
		return j
	}
	return end
}

// skipInline advances i over spaces and tabs, stopping at a line break.
func skipInline(b []byte, i int) int {
	for i < len(b) && isSpace(b[i]) && b[i] != '\n' {
		i++
	}
	return i
}

// skipSpace advances i over whitespace, line breaks included.
func skipSpace(b []byte, i int) int {
	for i < len(b) && isSpace(b[i]) {
		i++
	}
	return i
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
