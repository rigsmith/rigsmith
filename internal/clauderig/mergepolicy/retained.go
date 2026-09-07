package mergepolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
)

// ResolveMetadata uses the existing native unions for retained publication.
// Only schema-1 manifest/device documents are accepted. Unknown/duplicate fields,
// unsupported paths and malformed sides remain conflicts, never newest-side
// fallbacks. A device or link removed on one side stays removed when the other
// side's entry is unchanged from base; a changed entry can return. No base unions.
// The publisher bounds inputs/outputs and audits the complete candidate.
func ResolveMetadata(ctx context.Context, path string, base, ours, theirs []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var merged any
	switch path {
	case manifest.FileName:
		var a, b manifest.Manifest
		if !decodeRetainedMetadata(ours, &a) || !decodeRetainedMetadata(theirs, &b) || a.Schema != 1 || b.Schema != 1 {
			return nil, commitartifact.ErrConflict
		}
		if !validManifestEntries(a) || !validManifestEntries(b) {
			return nil, commitartifact.ErrConflict
		}
		var ancestor manifest.Manifest
		if base != nil {
			if !decodeRetainedMetadata(base, &ancestor) || ancestor.Schema != 1 || !validManifestEntries(ancestor) {
				return nil, commitartifact.ErrConflict
			}
		}
		// Sync treats a missing link in an owned project as a removal. Do not
		// reintroduce it from an unchanged copy; a retargeted link can return.
		for link, previous := range ancestor.Links {
			ourTarget, haveOurs := a.Links[link]
			theirTarget, haveTheirs := b.Links[link]
			if haveOurs && !haveTheirs && ourTarget == previous {
				delete(a.Links, link)
			}
			if haveTheirs && !haveOurs && theirTarget == previous {
				delete(b.Links, link)
			}
		}
		merged = mergeManifest(a, b)
	case devices.FileName:
		var a, b devices.Registry
		if !decodeRetainedMetadata(ours, &a) || !decodeRetainedMetadata(theirs, &b) || a.Schema != 1 || b.Schema != 1 {
			return nil, commitartifact.ErrConflict
		}
		if !validDeviceEntries(a) || !validDeviceEntries(b) {
			return nil, commitartifact.ErrConflict
		}
		var ancestor devices.Registry
		if base != nil {
			if !decodeRetainedMetadata(base, &ancestor) || ancestor.Schema != 1 || !validDeviceEntries(ancestor) {
				return nil, commitartifact.ErrConflict
			}
		}
		// Compare before unioning: mergeDevices mutates b's map and may fill
		// missing account provenance from the other side.
		for name, previous := range ancestor.Devices {
			ourDevice, haveOurs := a.Devices[name]
			theirDevice, haveTheirs := b.Devices[name]
			if haveOurs && !haveTheirs && sameDevice(ourDevice, previous) {
				delete(a.Devices, name)
			}
			if haveTheirs && !haveOurs && sameDevice(theirDevice, previous) {
				delete(b.Devices, name)
			}
		}
		merged = mergeDevices(a, b)
	default:
		return nil, commitartifact.ErrConflict
	}
	b, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func sameDevice(a, b devices.Device) bool {
	if !a.LastSync.Equal(b.LastSync) {
		return false
	}
	// Location/offset spelling is not evidence that a removed machine synced.
	a.LastSync = b.LastSync
	return reflect.DeepEqual(a, b)
}

func validDeviceEntries(r devices.Registry) bool {
	for name, d := range r.Devices {
		if strings.TrimSpace(name) == "" || d.Name != name {
			return false
		}
	}
	return true
}

func validManifestEntries(m manifest.Manifest) bool {
	for slug, p := range m.Projects {
		if strings.TrimSpace(slug) == "" || strings.TrimSpace(p.Cwd) == "" {
			return false
		}
	}
	for link, target := range m.Links {
		for _, endpoint := range []string{link, target} {
			// Links are slash-relative to the Claude root on every platform.
			if endpoint == "." || !fs.ValidPath(endpoint) || strings.ContainsAny(endpoint, "\\:\x00") {
				return false
			}
		}
	}
	return true
}

func decodeRetainedMetadata(raw []byte, out any) bool {
	if !utf8.Valid(raw) {
		return false
	}
	// DisallowUnknownFields does not reject duplicate fields. Validate tokens
	// first so a duplicate (including a case alias) cannot silently lose a value.
	tokens := json.NewDecoder(bytes.NewReader(raw))
	if !uniqueJSON(tokens, reflect.TypeOf(out), 0) {
		return false
	}
	if _, err := tokens.Token(); err != io.EOF {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(out) == nil
}

// These metadata types use direct tagged fields, string-keyed maps and pointers.
// Follow their types so struct aliases collide but map identifiers stay exact.
func uniqueJSON(d *json.Decoder, typ reflect.Type, depth int) bool {
	if depth > 32 {
		return false
	}
	token, err := d.Token()
	if err != nil {
		return false
	}
	delim, container := token.(json.Delim)
	if !container {
		return true
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch delim {
	case '{':
		if typ.Kind() != reflect.Struct && typ.Kind() != reflect.Map {
			return false
		}
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok {
				return false
			}
			var child reflect.Type
			if typ.Kind() == reflect.Map {
				child = typ.Elem()
			} else {
				name = foldedJSONName(name)
				for i := 0; i < typ.NumField(); i++ {
					field := typ.Field(i)
					tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
					if field.IsExported() && tag != "" && tag != "-" && foldedJSONName(tag) == name {
						child = field.Type
						break
					}
				}
				if child == nil {
					return false
				}
			}
			if seen[name] {
				return false
			}
			seen[name] = true
			if !uniqueJSON(d, child, depth+1) {
				return false
			}
		}
	case '[':
		if typ.Kind() != reflect.Slice && typ.Kind() != reflect.Array {
			return false
		}
		for d.More() {
			if !uniqueJSON(d, typ.Elem(), depth+1) {
				return false
			}
		}
	default:
		return false
	}
	end, err := d.Token()
	return err == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
}

// Match encoding/json's Unicode simple-fold equivalence, not just lowercase.
// Canonicalizing each fold cycle keeps duplicate lookup linear in input size.
func foldedJSONName(name string) string {
	return strings.Map(func(r rune) rune {
		smallest := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < smallest {
				smallest = next
			}
		}
		return smallest
	}, name)
}
