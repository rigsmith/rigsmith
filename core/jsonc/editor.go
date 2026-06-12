// Comment-preserving JSONC editor: sets a single property (top-level, or one
// level deep — e.g. coverage.license) while preserving comments, formatting,
// and key order. Re-serializing through a DOM would drop comments, so instead
// the document is tokenized (over a Strip'd copy, whose byte offsets match the
// original exactly) to locate the exact byte span, and the raw bytes are
// spliced — leaving everything else byte-for-byte intact. The value is
// supplied pre-serialized (a raw JSON literal: `"foo"`, `true`, `80`), so the
// same machinery handles strings, bools, and numbers.
//
// This is a port of rig's .NET JsoncEditor.
package jsonc

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SetTopLevelString sets a top-level string property, preserving comments.
// Back-compat entry point mirroring the .NET TrySetTopLevelString.
func SetTopLevelString(text, property, value string) (string, bool) {
	raw, err := json.Marshal(value)
	if err != nil {
		return text, false
	}
	return Set(text, []string{property}, string(raw))
}

// Set sets path (depth 1 or 2) to rawValue, a pre-serialized JSON literal.
// It returns the edited document and true on success; on failure (malformed
// input, unsupported depth, non-object root, or a non-object parent it
// refuses to clobber) it returns the input unchanged and false.
func Set(text string, path []string, rawValue string) (string, bool) {
	original := text
	text = strings.TrimPrefix(text, "\uFEFF") // tolerate a leading BOM
	switch len(path) {
	case 1:
		return setTopLevel(text, path[0], rawValue)
	case 2:
		return setNested(text, path[0], path[1], rawValue)
	default:
		return original, false // only depths 1–2 are supported (all rig keys fit)
	}
}

func setTopLevel(text, property, rawValue string) (string, bool) {
	orig := []byte(text)
	stripped := Strip(orig)
	if !json.Valid(stripped) {
		return text, false // malformed — let the caller fall back
	}

	depth := 0
	awaitingValue := false
	valueStart, valueEnd := -1, -1
	afterRootBrace, firstMemberStart := -1, -1

	s := scanner{data: stripped}
	for {
		t, ok := s.next()
		if !ok {
			break
		}

		if awaitingValue {
			valueStart = t.start
			valueEnd = t.end
			if t.kind == tokStartObject || t.kind == tokStartArray {
				valueEnd = s.skipContainer()
			}
			break
		}

		switch t.kind {
		case tokStartObject, tokStartArray:
			if t.kind == tokStartObject && depth == 0 {
				afterRootBrace = t.start + 1
			}
			depth++
		case tokEndObject, tokEndArray:
			depth--
		case tokPropertyName:
			if depth == 1 {
				if firstMemberStart < 0 {
					firstMemberStart = t.start
				}
				if propertyNameEquals(t.raw, property) {
					awaitingValue = true
				}
			}
		}
	}

	if valueStart >= 0 {
		return splice(orig, valueStart, valueEnd, rawValue), true
	}

	// Property absent — insert it right after the opening brace, *before* any
	// leading comment, so existing comments stay attached to their own member.
	if firstMemberStart >= 0 && afterRootBrace >= 0 {
		indent := indentBefore(orig, firstMemberStart)
		ins := fmt.Sprintf("\n%s\"%s\": %s,", indent, property, rawValue)
		return splice(orig, afterRootBrace, afterRootBrace, ins), true
	}
	if afterRootBrace >= 0 { // empty object {}
		ins := fmt.Sprintf("\n  \"%s\": %s\n", property, rawValue)
		return splice(orig, afterRootBrace, afterRootBrace, ins), true
	}

	return text, false // not a JSON object
}

func setNested(text, parent, child, rawValue string) (string, bool) {
	orig := []byte(text)
	stripped := Strip(orig)
	if !json.Valid(stripped) {
		return text, false
	}

	depth := 0
	awaitingParentValue := false
	awaitingChildValue := false
	insideParent := false
	parentMatched := false
	parentIsObject := false

	afterRootBrace, rootFirstMember := -1, -1
	parentContentStart, parentFirstMember := -1, -1
	childValueStart, childValueEnd := -1, -1

	s := scanner{data: stripped}
	for {
		t, ok := s.next()
		if !ok {
			break
		}

		if awaitingChildValue {
			childValueStart = t.start
			childValueEnd = t.end
			if t.kind == tokStartObject || t.kind == tokStartArray {
				childValueEnd = s.skipContainer()
			}
			awaitingChildValue = false
			continue
		}

		if awaitingParentValue {
			awaitingParentValue = false
			if t.kind == tokStartObject {
				parentIsObject = true
				parentContentStart = t.start + 1
				insideParent = true
				depth++ // entering the parent object; count it ourselves
				continue
			}
			// Parent exists but isn't an object — refuse to clobber it.
			if t.kind == tokStartArray {
				s.skipContainer()
			}
			continue
		}

		switch t.kind {
		case tokStartObject, tokStartArray:
			if t.kind == tokStartObject && depth == 0 {
				afterRootBrace = t.start + 1
			}
			depth++
		case tokEndObject, tokEndArray:
			depth--
			if insideParent && depth == 1 {
				insideParent = false
			}
		case tokPropertyName:
			switch {
			case depth == 1:
				if rootFirstMember < 0 {
					rootFirstMember = t.start
				}
				if propertyNameEquals(t.raw, parent) {
					parentMatched = true
					awaitingParentValue = true
				}
			case depth == 2 && insideParent:
				if parentFirstMember < 0 {
					parentFirstMember = t.start
				}
				if propertyNameEquals(t.raw, child) {
					awaitingChildValue = true
				}
			}
		}
	}

	// Child already present → replace its value span.
	if childValueStart >= 0 {
		return splice(orig, childValueStart, childValueEnd, rawValue), true
	}

	// Parent object present, child absent → insert the child member.
	if parentMatched && parentIsObject && parentContentStart >= 0 {
		var ins string
		switch {
		// A single-line parent (e.g. the init template's `"coverage": { ... }`)
		// gets an inline insert; a multi-line one matches its members' indent.
		case parentFirstMember >= 0 && hasNewline(orig, parentContentStart, parentFirstMember):
			indent := indentBefore(orig, parentFirstMember)
			ins = fmt.Sprintf("\n%s\"%s\": %s,", indent, child, rawValue)
		case parentFirstMember >= 0: // single-line object with members
			ins = fmt.Sprintf(" \"%s\": %s,", child, rawValue)
		default: // empty object: "coverage": {}
			ins = fmt.Sprintf(" \"%s\": %s ", child, rawValue)
		}
		return splice(orig, parentContentStart, parentContentStart, ins), true
	}

	// Parent present but not an object — don't risk clobbering it.
	if parentMatched {
		return text, false
	}

	// Parent absent → insert a fresh "parent": { "child": value } at top level.
	if rootFirstMember >= 0 && afterRootBrace >= 0 {
		indent := indentBefore(orig, rootFirstMember)
		ins := fmt.Sprintf("\n%s\"%s\": { \"%s\": %s },", indent, parent, child, rawValue)
		return splice(orig, afterRootBrace, afterRootBrace, ins), true
	}
	if afterRootBrace >= 0 { // empty root {}
		ins := fmt.Sprintf("\n  \"%s\": { \"%s\": %s }\n", parent, child, rawValue)
		return splice(orig, afterRootBrace, afterRootBrace, ins), true
	}

	return text, false
}

// ---- tokenizer ----
//
// The scanner runs over Strip'd bytes (comments blanked to spaces, offsets
// preserved), and the input has already passed json.Valid, so it can be a
// trusting tokenizer rather than a validating parser. Offsets it reports are
// valid in the original bytes too.

type tokKind int

const (
	tokStartObject tokKind = iota
	tokEndObject
	tokStartArray
	tokEndArray
	tokPropertyName
	tokValue // primitive: string, number, true, false, null
)

type token struct {
	kind  tokKind
	start int    // byte offset of the token's first byte
	end   int    // byte offset just past the token
	raw   []byte // raw token bytes (quotes included for strings)
}

type frame struct{ isObject, expectKey bool }

type scanner struct {
	data  []byte
	pos   int
	stack []frame
}

func (s *scanner) top() *frame {
	if len(s.stack) == 0 {
		return nil
	}
	return &s.stack[len(s.stack)-1]
}

func (s *scanner) next() (token, bool) {
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case ' ', '\t', '\n', '\r', ',', ':':
			s.pos++
			continue
		}
		break
	}
	if s.pos >= len(s.data) {
		return token{}, false
	}

	start := s.pos
	switch c := s.data[s.pos]; c {
	case '{', '[':
		// A container opening in value position re-arms its parent's key state.
		if top := s.top(); top != nil && top.isObject && !top.expectKey {
			top.expectKey = true
		}
		s.stack = append(s.stack, frame{isObject: c == '{', expectKey: c == '{'})
		s.pos++
		kind := tokStartArray
		if c == '{' {
			kind = tokStartObject
		}
		return token{kind: kind, start: start, end: s.pos}, true
	case '}', ']':
		if len(s.stack) > 0 {
			s.stack = s.stack[:len(s.stack)-1]
		}
		s.pos++
		kind := tokEndArray
		if c == '}' {
			kind = tokEndObject
		}
		return token{kind: kind, start: start, end: s.pos}, true
	case '"':
		s.pos++
		for s.pos < len(s.data) {
			if s.data[s.pos] == '\\' {
				s.pos += 2
				continue
			}
			if s.data[s.pos] == '"' {
				s.pos++
				break
			}
			s.pos++
		}
		t := token{kind: tokValue, start: start, end: s.pos, raw: s.data[start:s.pos]}
		if top := s.top(); top != nil && top.isObject {
			if top.expectKey {
				top.expectKey = false
				t.kind = tokPropertyName
			} else {
				top.expectKey = true
			}
		}
		return t, true
	default: // number, true, false, null
		for s.pos < len(s.data) {
			switch s.data[s.pos] {
			case ' ', '\t', '\n', '\r', ',', ':', '}', ']':
				return s.finishLiteral(start)
			}
			s.pos++
		}
		return s.finishLiteral(start)
	}
}

func (s *scanner) finishLiteral(start int) (token, bool) {
	if top := s.top(); top != nil && top.isObject {
		top.expectKey = true
	}
	return token{kind: tokValue, start: start, end: s.pos, raw: s.data[start:s.pos]}, true
}

// skipContainer consumes tokens through the matching close of a container
// whose start token was just returned, and reports the offset just past the
// closing brace/bracket (the .NET reader.Skip + BytesConsumed equivalent).
func (s *scanner) skipContainer() int {
	depth, end := 1, -1
	for depth > 0 {
		t, ok := s.next()
		if !ok {
			break
		}
		switch t.kind {
		case tokStartObject, tokStartArray:
			depth++
		case tokEndObject, tokEndArray:
			depth--
		}
		end = t.end
	}
	return end
}

// propertyNameEquals compares a raw (still-escaped) name token against name,
// matching the .NET ValueTextEquals semantics (unescaped comparison).
func propertyNameEquals(raw []byte, name string) bool {
	var decoded string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false
	}
	return decoded == name
}

func hasNewline(b []byte, start, end int) bool {
	for i := start; i < end && i < len(b); i++ {
		if b[i] == '\n' {
			return true
		}
	}
	return false
}

func splice(src []byte, start, end int, insert string) string {
	out := make([]byte, 0, start+len(insert)+len(src)-end)
	out = append(out, src[:start]...)
	out = append(out, insert...)
	out = append(out, src[end:]...)
	return string(out)
}

// indentBefore reports the run of spaces/tabs immediately preceding an offset
// (the member's indent), defaulting to two spaces when there is none.
func indentBefore(b []byte, offset int) string {
	i := offset - 1
	end := i
	for i >= 0 && (b[i] == ' ' || b[i] == '\t') {
		i--
	}
	if end-i <= 0 {
		return "  "
	}
	return string(b[i+1 : end+1])
}
