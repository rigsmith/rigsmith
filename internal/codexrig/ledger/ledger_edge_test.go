package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// "a/b" and "a?b" both sanitised to a-b.jsonl and shared one ledger, merging
// rows and overwriting each other's RecordedBy. A hostname that needed no
// sanitising keeps the file it always had.
func TestSanitisedDeviceNamesDoNotCollide(t *testing.T) {
	if fileName("a/b") == fileName("a?b") {
		t.Errorf("two different devices map to %s", fileName("a/b"))
	}
	if fileName("mbp") != "mbp.jsonl" {
		t.Errorf("an ordinary name moved: %s", fileName("mbp"))
	}
	if strings.HasPrefix(fileName(""), ".") || fileName("") == fileName("???") {
		t.Errorf("empty and punctuation-only names collide or hide: %s %s", fileName(""), fileName("???"))
	}
}

// One line past the scanner's cap ended the scan with ErrTooLong, and the
// rows before it — parsed fine — were thrown away with the file. For an
// aged-out session that row is the only record left.
func TestAnOversizeLineDoesNotLoseTheRowsBeforeIt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.jsonl")
	good := `{"id":"aaaaaaaa-1111-2222-3333-444444444444","bytes":1}` + "\n"
	huge := `{"id":"bbbbbbbb-1111-2222-3333-444444444444","title":"` + strings.Repeat("x", 5<<20) + `"}` + "\n"
	if err := os.WriteFile(p, []byte(good+huge), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, err := readFile(p)
	if err != nil {
		t.Fatalf("readFile returned an error instead of the rows it had: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "aaaaaaaa-1111-2222-3333-444444444444" {
		t.Errorf("rows = %+v, want the one before the oversize line", rows)
	}
}
