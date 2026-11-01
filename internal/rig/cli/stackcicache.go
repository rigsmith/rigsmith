package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Cache only the filtered upstream graph, never a fused stackspace commit.
// Restored bundles are trusted inputs: Git object integrity is not proof that
// the filtered graph corresponds to the upstream cursor encoded in the key.
func stackCIFilteredCache(ctx context.Context, dir, source, cursor, name, version string, fetch func() (string, error)) (string, error) {
	cache := os.Getenv("RIG_STACK_CACHE")
	if cache == "" {
		sha, err := fetch()
		if err == nil {
			stackCICacheResult(name, "fetched", sha)
		}
		return sha, err
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(source+"\n"+cursor+"\n"+stackPrefixFilter(name)+"\n"+version)))
	path := filepath.Join(cache, key+".bundle")
	if _, err := os.Stat(path); err == nil {
		if _, err = stackCIGit(ctx, dir, "fetch", path, "refs/rigsmith/cache"); err == nil {
			b, err := stackCIGit(ctx, dir, "rev-parse", "FETCH_HEAD")
			if err == nil {
				stackCICacheResult(name, "cache", strings.TrimSpace(string(b)))
				return strings.TrimSpace(string(b)), nil
			}
		}
	}
	sha, err := fetch()
	if err != nil {
		return "", err
	}
	stackCICacheResult(name, "fetched", sha)
	// Cache persistence is optional once the filtered history is available.
	save := func() error {
		defer func() {
			if _, e := stackCIGit(context.WithoutCancel(ctx), dir, "update-ref", "-d", "refs/rigsmith/cache"); e != nil {
				fmt.Fprintf(os.Stderr, "warning: cleaning filtered history cache ref for %s: %v\n", name, e)
			}
		}()
		if e := os.MkdirAll(cache, 0700); e != nil {
			return e
		}
		if _, e := stackCIGit(ctx, dir, "update-ref", "refs/rigsmith/cache", sha); e != nil {
			return e
		}
		_, e := stackCIGit(ctx, dir, "bundle", "create", path, "refs/rigsmith/cache")
		return e
	}
	if e := save(); e != nil {
		fmt.Fprintf(os.Stderr, "warning: caching filtered history for %s: %v\n", name, e)
	}
	return sha, nil
}
func stackCICacheResult(name, source, sha string) {
	if p := os.Getenv("RIG_STACK_CACHE_REPORT"); p != "" {
		if f, e := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); e == nil {
			fmt.Fprintf(f, "%s\t%s\t%s\n", name, source, sha)
			f.Close()
		}
	}
}
