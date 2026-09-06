package adapter

import (
	"path"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

type Kind uint8

const (
	Other Kind = iota
	Settings
	Transcript
	Memory
	DesktopCodeSidecar
	DesktopCoworkSidecar
	ProfileMetadata
)

type Retention uint8

const (
	Keep Retention = iota
	ProjectAge
)

type Transform uint8

const (
	Copy Transform = iota
	JSON
	// ConversationText still requires the engine's content-based binary check
	// and the user's redactTranscripts setting before any rewriting.
	ConversationText
)

// File describes a slash-separated root-relative path. Classification does not
// authorize a path: capture must still use Root.Allowlist. Restore keeps its
// existing treatment of older staged files, independent of the current list.
// Kind is descriptive; the independent policy fields preserve the distinctions
// between capture, retention and merging instead of inferring them from Kind.
type File struct {
	RootID        string
	Rel           string
	Kind          Kind
	Retention     Retention
	Transform     Transform
	ChunkEligible bool
	KeepKeys      []string
	Merge         MergeRule
}

func (r Root) Classify(rel string) File {
	file := File{RootID: r.ID, Rel: rel, Merge: ClassifyMerge(r.ID + "/" + rel)}
	project := strings.HasPrefix(rel, "projects/")
	parts := strings.Split(rel, "/")
	memory := len(parts) > 3 && parts[0] == "projects" && parts[2] == "memory"
	// Capture has always recognized lowercase .jsonl under projects/, with
	// memory exempt. Merge intentionally has a wider, case-insensitive rule.
	file.ChunkEligible = project && strings.HasSuffix(rel, ".jsonl") && !memory
	if project && !memory {
		file.Retention = ProjectAge
	}
	switch {
	case strings.HasSuffix(rel, ".json"):
		file.Transform = JSON
		file.Kind = Settings
	case project:
		file.Transform = ConversationText
	}
	switch {
	case memory:
		file.Kind = Memory
	case file.ChunkEligible:
		file.Kind = Transcript
	}
	if r.Kind == DesktopRoot || r.Kind == DesktopProfileRoot {
		desktopRel := rel
		// Match the existing wrapper rule exactly: an empty profile name does
		// not strip data/, even though the allowlist recognizes desktop@.
		if ProfileNameOf(r.ID) != "" {
			desktopRel = strings.TrimPrefix(rel, "data/")
		}
		// Keep stable preferences only: account identity, oauth/dxt caches and
		// other machine-local Desktop state must not reach another machine.
		if desktopRel == "config.json" {
			file.KeepKeys = config.DesktopConfigKeepKeys()
		}
		switch {
		case IsDesktopCodeSidecar(desktopRel):
			file.Kind = DesktopCodeSidecar
		case isSidecar(desktopRel, "local-agent-mode-sessions/"):
			file.Kind = DesktopCoworkSidecar
		case r.Kind == DesktopProfileRoot && rel == "profile.json":
			file.Kind = ProfileMetadata
		}
	}
	return file
}

// Classify is useful for staged paths where no configured root is available.
func Classify(rootID, rel string) File {
	// No walk is involved, so avoid allocating an allowlist for every file.
	return (Root{Root: config.Root{ID: rootID}, Kind: rootKind(rootID)}).Classify(rel)
}

// IsDesktopCodeSidecar accepts the native Desktop layout, without a profile's
// data/ wrapper. The basename rule also preserves historical shallow layouts.
func IsDesktopCodeSidecar(rel string) bool {
	return isSidecar(rel, "claude-code-sessions/")
}

func isSidecar(rel, tree string) bool {
	if !strings.HasPrefix(rel, tree) {
		return false
	}
	base := path.Base(rel)
	return strings.HasPrefix(base, "local_") && strings.HasSuffix(base, ".json")
}
