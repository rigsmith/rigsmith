package redact

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// ScanReader examines the complete text stream with bounded memory. Overlap
// covers token prefixes and JSON escapes split between reads. It deliberately
// uses credential signatures, not entropy guesses over conversation prose.
// Only the rule and path are returned; secret bytes never enter diagnostics.
func ScanReader(rel string, r io.Reader) (*Finding, error) {
	if ClassifyName(rel) == NameKeyMaterial {
		return &Finding{Path: rel, Kind: "key-material"}, nil
	}
	const block = 32 << 10
	const overlap = 4096
	buf := make([]byte, block+overlap)
	kept := 0
	first := true
	var small []byte
	var total int64
	var jwt jwtStream
	for {
		n, err := io.ReadFull(r, buf[kept:kept+block])
		data := buf[:kept+n]
		if first {
			first = false
			ext := strings.ToLower(path.Ext(rel))
			textFile := ext == ".jsonl" || ext == ".json" || ext == ".part" || ext == ".md" || ext == ".txt" || ext == ".toml" || ext == ".yaml" || ext == ".yml"
			if !textFile && isBinary(data) {
				if err == nil {
					_, err = io.Copy(io.Discard, r)
				}
				if err == io.EOF || err == io.ErrUnexpectedEOF {
					err = nil
				}
				return nil, err
			}
		}
		if total+int64(n) <= int64(ScanContentLimit()) {
			small = append(small, buf[kept:kept+n]...)
		} else {
			small = nil
		}
		total += int64(n)
		if jwt.feed(buf[kept : kept+n]) {
			return &Finding{Path: rel, Kind: "jwt"}, nil
		}
		if finding := scanText(rel, data); finding != nil {
			return finding, nil
		}
		if err != nil {
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				return nil, fmt.Errorf("scan %s: %w", rel, err)
			}
			// Retain the existing filename/auth-assignment and bare-token rules for
			// small files, in addition to embedded-token detection for all text.
			if total <= int64(ScanContentLimit()) && !strings.HasSuffix(rel, ".part") {
				if fs := ScanFile(rel, small); len(fs) > 0 {
					return &fs[0], nil
				}
			}
			return nil, nil
		}
		kept = min(overlap, len(data))
		copy(buf, data[len(data)-kept:])
	}
}

var asciiEscape = regexp.MustCompile(`\\u00[0-9a-fA-F]{2}`)

func scanText(rel string, data []byte) *Finding {
	// JSON can encode credential characters as Unicode escapes. Normalize only
	// for detection, never change the native transcript or serialized config.
	normalized := asciiEscape.ReplaceAllFunc(data, func(b []byte) []byte {
		n, _ := strconv.ParseUint(string(b[4:]), 16, 8)
		return []byte{byte(n)}
	})
	normalized = bytes.ReplaceAll(normalized, []byte(`\/`), []byte(`/`))
	if ClassifyName(rel) == NameAuthConfig && hasAuthAssignment(string(normalized)) {
		return &Finding{Path: rel, Kind: "auth-config"}
	}
	if HasPrivateKey(normalized) {
		return &Finding{Path: rel, Kind: "private-key"}
	}
	for _, loc := range textSecretRe.FindAllIndex(normalized, -1) {
		token := string(normalized[loc[0]:loc[1]])
		if !screamingRe.MatchString(token) {
			return &Finding{Path: rel, Kind: kindOf(token)}
		}
	}
	return nil
}

// jwtStream covers arbitrarily long raw JWT segments without retaining the
// token. A fixed overlap alone cannot cover a JWT payload larger than a block.
type jwtStream struct{ phase, count int }

func (s *jwtStream) feed(data []byte) bool {
	for _, c := range data {
		if s.phase == 0 {
			prefix := "eyJ"
			if c == prefix[s.count] {
				s.count++
				if s.count == 3 {
					s.phase = 1
				}
			} else if c == 'e' {
				s.count = 1
			} else {
				s.count = 0
			}
			continue
		}
		tokenChar := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-'
		if tokenChar {
			s.count++
			if s.phase == 3 && s.count >= 5 {
				return true
			}
			continue
		}
		minimum := 5
		if s.phase == 1 {
			minimum = 8
		}
		if c == '.' && s.phase < 3 && s.count >= minimum {
			s.phase++
			s.count = 0
		} else {
			s.phase = 0
			s.count = 0
		}
	}
	return false
}
