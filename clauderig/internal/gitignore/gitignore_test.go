package gitignore

import "testing"

func TestEnsureLine(t *testing.T) {
	const e = ".claude/settings.local.json"
	tests := []struct {
		name        string
		in          string
		wantChanged bool
		wantHas     bool
	}{
		{"empty file", "", true, true},
		{"no trailing newline", "node_modules", true, true},
		{"trailing newline", "node_modules\n", true, true},
		{"already present", "a\n.claude/settings.local.json\nb\n", false, true},
		{"present with spaces", "  .claude/settings.local.json  \n", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, changed := EnsureLine(tt.in, e)
			if changed != tt.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}
			has := false
			for _, line := range splitLines(out) {
				if line == e {
					has = true
				}
			}
			if has != tt.wantHas {
				t.Errorf("entry present = %v, want %v (out=%q)", has, tt.wantHas, out)
			}
			// Idempotent: a second pass never changes.
			if _, c2 := EnsureLine(out, e); c2 {
				t.Error("second EnsureLine changed an already-ensured file")
			}
		})
	}
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, trim(cur))
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, trim(cur))
	return out
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
