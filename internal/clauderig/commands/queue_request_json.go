package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"unicode/utf8"
)

// The saved format is deliberately exact and versioned. Validate members before
// decoding: encoding/json otherwise accepts duplicate and case-aliased fields,
// so its decoded-value checksum alone cannot reject ambiguous input documents.
var queueRequestFields = map[string][]string{
	"":              {"Checksum", "Version", "Scope", "At", "Identity", "Request"},
	"Identity":      {"AccountUUID", "OrganizationUUID", "Email"},
	"Request":       {"EventID", "SessionID", "ProvenanceID", "Flush"},
	"Request.Flush": {"Mode", "Paths"},
}

func validateQueueRequestJSON(data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("request JSON must be valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	var walk func(string, int) error
	walk = func(path string, depth int) error {
		if depth > 32 {
			return fmt.Errorf("request JSON nesting exceeds limit")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, container := token.(json.Delim)
		if !container {
			return nil
		}
		switch delim {
		case '{':
			allowed, ok := queueRequestFields[path]
			if !ok {
				return fmt.Errorf("unexpected request object")
			}
			seen := map[string]bool{}
			for d.More() {
				token, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := token.(string)
				if !ok || seen[name] || !slices.Contains(allowed, name) {
					return fmt.Errorf("duplicate, aliased or unknown request field")
				}
				seen[name] = true
				child := name
				if path != "" {
					child = path + "." + name
				}
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil {
				return err
			}
			if end != json.Delim('}') {
				return fmt.Errorf("invalid request object")
			}
		case '[':
			if path != "Request.Flush.Paths" {
				return fmt.Errorf("unexpected request array")
			}
			for d.More() {
				if err := walk(path+"[]", depth+1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil {
				return err
			}
			if end != json.Delim(']') {
				return fmt.Errorf("invalid request array")
			}
		default:
			return fmt.Errorf("invalid request JSON delimiter")
		}
		return nil
	}
	if err := walk("", 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("request must contain exactly one JSON object")
	}
	return nil
}
