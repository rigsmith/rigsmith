package gitrepo

import (
	"context"
	"testing"
)

func TestLastCommit(t *testing.T) {
	ctx := context.Background()
	r, _ := Init(ctx, t.TempDir())
	write(t, r.Dir, "a", "x")
	if _, err := r.Commit(ctx, "my subject line"); err != nil {
		t.Fatal(err)
	}
	hash, subject, when, err := r.LastCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) < 6 || subject != "my subject line" || when == "" {
		t.Fatalf("hash=%q subject=%q when=%q", hash, subject, when)
	}
}
