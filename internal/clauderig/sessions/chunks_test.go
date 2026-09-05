package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

func TestDeleteChunkedSessionRemovesItsParts(t *testing.T) {
	p := filepath.Join(t.TempDir(), delID+".jsonl")
	if err := transcript.Write(p, strings.NewReader("body\n"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := removeSessionPath(p, delID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{p, p + transcript.Suffix} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("left behind %s", path)
		}
	}
}
