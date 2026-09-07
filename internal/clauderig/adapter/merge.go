package adapter

import (
	"path"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/allowlist"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

type MergeStrategy uint8

const (
	NewestSnapshot MergeStrategy = iota
	UnionManifest
	UnionDevices
	UnionText
)

// MergeRule selects policy; the existing merge implementation still handles
// payloads, chunk-index refusal, failed unions and delete/edit fallbacks.
type MergeRule struct {
	Strategy           MergeStrategy
	CheckChunkIndex    bool
	DeduplicateRecords bool
}

// ClassifyMerge accepts a path relative to the backup repository. These are
// Claude's existing rules, including unioning .jsonl outside projects/. They
// must not become an extension-based default for another vendor's adapter.
func ClassifyMerge(p string) MergeRule {
	rule := MergeRule{
		CheckChunkIndex:    strings.HasSuffix(p, ".jsonl"),
		DeduplicateRecords: strings.EqualFold(path.Ext(p), ".jsonl"),
	}
	switch {
	case p == manifest.FileName:
		rule.Strategy = UnionManifest
	case p == devices.FileName:
		rule.Strategy = UnionDevices
	case rule.DeduplicateRecords:
		rule.Strategy = UnionText
	default:
		switch strings.ToLower(path.Ext(p)) {
		case ".md", ".markdown", ".txt":
			for _, segment := range strings.Split(path.Dir(p), "/") {
				if segment == "memory" {
					rule.Strategy = UnionText
					break
				}
			}
		}
	}
	return rule
}

// RetainedSnapshot selects ordinary files with native whole-snapshot semantics.
// Root metadata, excluded files and failed transcript/memory unions must never
// fall through to this policy. Unknown/custom roots need an explicit policy.
func RetainedSnapshot(p string) bool {
	root, rel, ok := strings.Cut(p, "/")
	if !ok || strings.EqualFold(path.Base(rel), ".gitattributes") || (root != "cli" && root != DesktopRootID && ProfileNameOf(root) == "") {
		return false
	}
	file := Classify(root, rel)
	return file.Merge.Strategy == NewestSnapshot && file.Kind != Memory &&
		!transcript.IsPartPath(rel) && allowlist.For(root).Match(rel)
}
