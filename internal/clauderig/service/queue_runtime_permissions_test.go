//go:build linux || darwin

package service

import (
	"errors"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"os"
	"path/filepath"
	"testing"
)

func TestQueueRuntimeRefusesPermissiveReopen(t *testing.T) {
	for _, name := range []string{".", "runtime.json", "queue"} {
		t.Run(name, func(t *testing.T) {
			r, req := runtimeFixture(t)
			path := filepath.Join(r.dir, name)
			if err := os.Chmod(path, 0777); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenQueueRuntime(t.Context(), r.dir, req, nil); !errors.Is(err, queue.ErrBinding) {
				t.Fatal(err)
			}
			if _, err := CreateQueueRuntime(t.Context(), r.dir, req, nil); !errors.Is(err, queue.ErrBinding) {
				t.Fatal(err)
			}
		})
	}
}
