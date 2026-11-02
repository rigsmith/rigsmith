package cli

import (
	"context"
	"slices"
	"strings"
)

type stackReleaseAnnotation func(string, string) string

// All cache state belongs to one report. Discovery happens once, lazily after
// a merged proposal has been established; repeated topics reuse annotations.
func stackReleaseAnnotations(ctx context.Context, m *stackManifest, a stackDepsAPI, feed string, discover func(context.Context) ([]stackLink, error)) stackReleaseAnnotation {
	scanned := false
	var links []stackLink
	cache := map[string]string{}
	return func(member, sibling string) string {
		key := member + "\x00" + sibling
		if v, ok := cache[key]; ok {
			return v
		}
		cache[key] = ""
		if ctx.Err() != nil {
			return ""
		}
		if !scanned {
			scanned = true
			var err error
			links, err = discover(ctx)
			if err != nil {
				return ""
			}
		}
		var labels []string
		for _, l := range links {
			if l.To != sibling || !slices.Contains(l.From, member) {
				continue
			}
			v, err := stackReleaseVersion(ctx, a, m, sibling, l.Package, "", feed)
			if err == nil {
				labels = append(labels, l.Package+" released in "+v)
			}
		}
		if len(labels) > 0 {
			cache[key] = " (" + strings.Join(labels, "; ") + ")"
		}
		return cache[key]
	}
}
