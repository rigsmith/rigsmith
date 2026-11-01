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
func stackCIFilteredCache(ctx context.Context, dir, source, cursor, name, version string, fetch func() (string, error)) (string, error) {
	cache := os.Getenv("RIG_STACK_CACHE")
	if cache == "" {
		return fetch()
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(source+"\n"+cursor+"\n"+stackPrefixFilter(name)+"\n"+version)))
	path := filepath.Join(cache, key+".bundle")
	if _, err := os.Stat(path); err == nil {
		if _, err = stackCIGit(ctx, dir, "fetch", path, "refs/rigsmith/cache"); err == nil {
			b, err := stackCIGit(ctx, dir, "rev-parse", "FETCH_HEAD")
			if err == nil {
				stackCICacheResult("hit")
				return strings.TrimSpace(string(b)), nil
			}
		}
	}
	sha, err := fetch()
	if err != nil {
		return "", err
	}
	stackCICacheResult("miss")
	if err = os.MkdirAll(cache, 0700); err != nil {
		return "", err
	}
	if _, err = stackCIGit(ctx, dir, "update-ref", "refs/rigsmith/cache", sha); err != nil {
		return "", err
	}
	defer stackCIGit(ctx, dir, "update-ref", "-d", "refs/rigsmith/cache")
	_, err = stackCIGit(ctx, dir, "bundle", "create", path, "refs/rigsmith/cache")
	return sha, err
}
func stackCICacheResult(result string) {
	if p := os.Getenv("RIG_STACK_CACHE_REPORT"); p != "" {
		if f, e := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); e == nil {
			fmt.Fprintln(f, result)
			f.Close()
		}
	}
}
