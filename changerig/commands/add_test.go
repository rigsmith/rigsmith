package commands

import (
	"reflect"
	"testing"
)

func TestResolveEditor(t *testing.T) {
	cases := []struct {
		name           string
		visual, editor string
		goos           string
		want           []string
	}{
		{"VISUAL wins", "code -w", "vim", "darwin", []string{"code", "-w"}},
		{"EDITOR fallback", "", "nvim", "linux", []string{"nvim"}},
		{"blank VISUAL skipped", "  ", "nano", "linux", []string{"nano"}},
		{"default unix", "", "", "darwin", []string{"vi"}},
		{"default windows", "", "", "windows", []string{"notepad"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveEditor(c.visual, c.editor, c.goos); !reflect.DeepEqual(got, c.want) {
				t.Errorf("resolveEditor(%q,%q,%q) = %v, want %v", c.visual, c.editor, c.goos, got, c.want)
			}
		})
	}
}
