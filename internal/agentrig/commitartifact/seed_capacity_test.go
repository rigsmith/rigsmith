package commitartifact

import (
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

func TestSeedStoreCapacityPolicy(t *testing.T) {
	captures := artifact.Store{Dir: filepath.Join(t.TempDir(), "captures"), MaxBytes: 1024, MaxStoredBytes: 8192}
	seed := SeedStore(captures)
	if seed.Dir != filepath.Join(captures.Dir, "seeds") || seed.MaxBytes != 1024 || seed.MaxStoredBytes != 8192 {
		t.Fatal(seed)
	}
}
