package queue

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func TestReceiptCompactionRootAlias(t *testing.T) {
	q := fixture(t)
	link := filepath.Join(t.TempDir(), "queue-link")
	if err := os.Symlink(q.dir, link); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	alias, err := Open(t.Context(), link, fixtureBinding)
	if err != nil {
		t.Fatal(err)
	}
	enqueue(t, alias, request("done"))
	w := worker(t, q)
	finish(t, w, next(t, w))
	cutoff := fixtureTime.Add(time.Minute)
	if _, err := alias.CompactReceipts(t.Context(), cutoff); !errors.Is(err, storelock.ErrBusy) {
		t.Fatal("alias bypassed canonical worker", err)
	}
	w.Close()
	result, err := alias.CompactReceipts(t.Context(), cutoff)
	if err != nil || result.RemovedReceipts != 1 {
		t.Fatal(result, err)
	}
	if _, err := q.Enqueue(t.Context(), request("done"), fixtureTime); !errors.Is(err, ErrExpired) {
		t.Fatal("alias compacted a different queue", err)
	}
	if _, err := alias.Enqueue(t.Context(), request("fresh"), cutoff); err != nil {
		t.Fatal(err)
	}
	w = worker(t, q)
	finish(t, w, next(t, w))
	w.Close()
	if _, err := q.CompactReceipts(t.Context(), cutoff.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	c, err := alias.Capacity(t.Context())
	if err != nil || c.CompletedReceipts != 0 || c.RetiredReceipts != 2 {
		t.Fatal(c, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "absent"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := alias.CompactReceipts(t.Context(), cutoff); !os.IsNotExist(err) {
		t.Fatal("dangling alias initialized queue", err)
	}
}
