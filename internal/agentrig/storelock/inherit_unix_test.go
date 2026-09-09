//go:build linux || darwin

package storelock

import (
	"context"
	"errors"
	"testing"
)

func TestInheritedLeaseOutlivesOwner(t *testing.T) {
	dir := t.TempDir()
	ctx, release, err := Acquire(t.Context(), dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	inherited, err := Inherit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer inherited.Close()
	release()
	if _, err := Inherit(ctx); err == nil {
		t.Fatal("expired context duplicated a lease")
	}
	if _, release2, err := Acquire(t.Context(), dir, 0); !errors.Is(err, ErrBusy) {
		if release2 != nil {
			release2()
		}
		t.Fatalf("inherited lease did not exclude another writer: %v", err)
	}
	if err := inherited.Close(); err != nil {
		t.Fatal(err)
	}
	_, release2, err := Acquire(t.Context(), dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	release2()
	if _, err := Inherit(context.Background()); err == nil {
		t.Fatal("accepted absent lease")
	}
}
