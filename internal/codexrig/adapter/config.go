package adapter

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/files"
	"github.com/rigsmith/rigsmith/internal/agentrig/secrets"
	"github.com/rigsmith/rigsmith/internal/codexrig/configcodec"
)

const (
	MaxConfigFiles              = 32
	MaxConfigBytes              = 8 << 20
	maxConfigDirectoryEntries   = 4096
	maxConfigReplacementEntries = MaxConfigFiles + 1 // One batch's scratch plus the persistent lock.
)

// ErrConfigSourceChanged shares identity with changes detected by the source reader.
var ErrConfigSourceChanged = files.ErrSourceChanged
var ErrConfigNames = errors.New("Codex configuration filenames are not portable across supported platforms")

// ConfigFile contains sanitized TOML for one native base/profile filename. Data
// includes path policy but still needs final publication auditing; this is not
// a runnable Codex config or a serialized backup format.
type ConfigFile struct {
	Path string
	Data []byte
}

// ConfigCapture is an all-or-error, in-memory capture of selected config files.
// Only codec output and relative names are retained. No root path or source
// fingerprint is added; codec output includes the conservative TOML path policy.
// Missing directories fail capture rather than becoming a deletion instruction.
type ConfigCapture struct{ Files []ConfigFile }

// CaptureConfig selects only config.toml and valid NAME.config.toml direct
// children. Other candidates (including hooks, instructions and skills) remain
// behind their own codecs. Limits cover all directory names, selected files and
// aggregate raw/sanitized bytes. Neither files nor tool state are written.
func CaptureConfig(ctx context.Context, root Root) (ConfigCapture, error) {
	if root.Kind != CodexHome {
		return ConfigCapture{}, errors.New("configuration capture requires a Codex home")
	}
	source, err := files.OpenSource(ctx, root.Path)
	if err != nil {
		return ConfigCapture{}, err
	}
	defer source.Close()
	return captureConfig(ctx, source)
}

type configSource interface {
	Names(context.Context, int) ([]string, error)
	Read(context.Context, string, int64) ([]byte, error)
	Check(context.Context) error
}

// configDirectoryNames reserves a bounded allowance for replacement artifacts.
// Recognizing a reserved name never establishes ownership or permits cleanup.
func configDirectoryNames(ctx context.Context, source configSource) ([]string, error) {
	names, err := source.Names(ctx, maxConfigDirectoryEntries+maxConfigReplacementEntries)
	if err != nil {
		return nil, err
	}
	var visible []string
	internal := 0
	for _, name := range names {
		if files.IsReplacementArtifact(name) {
			internal++
		} else {
			visible = append(visible, name)
		}
		if internal > maxConfigReplacementEntries || len(visible) > maxConfigDirectoryEntries {
			return nil, files.ErrSourceLimit
		}
	}
	return visible, nil
}

func configNames(names []string) ([]string, error) {
	var selected []string
	seen := map[string]bool{}
	for _, name := range names {
		candidate, ok := Classify(CodexHome, name)
		if ok && (candidate.Kind == "config" || candidate.Kind == "config-profile") {
			if secrets.Contains(name) {
				return nil, configcodec.ErrSecret
			}
			lower := strings.ToLower(name)
			if seen[lower] || reservedProfile(profileName(name)) {
				return nil, ErrConfigNames
			}
			seen[lower] = true
			selected = append(selected, name)
			if len(selected) > MaxConfigFiles {
				return nil, files.ErrSourceLimit
			}
		}
	}
	slices.Sort(selected)
	return selected, nil
}

func captureConfig(ctx context.Context, source configSource) (ConfigCapture, error) {
	names, err := configDirectoryNames(ctx, source)
	if err != nil {
		return ConfigCapture{}, err
	}
	names, err = configNames(names)
	if err != nil {
		return ConfigCapture{}, err
	}
	result := ConfigCapture{Files: make([]ConfigFile, 0, len(names))}
	fingerprints := make([][32]byte, 0, len(names))
	rawBytes, cleanBytes := 0, 0
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return ConfigCapture{}, err
		}
		raw, err := source.Read(ctx, name, configcodec.MaxBytes)
		if err != nil {
			return ConfigCapture{}, err
		}
		rawBytes += len(raw)
		if rawBytes > MaxConfigBytes {
			return ConfigCapture{}, files.ErrSourceLimit
		}
		clean, err := configcodec.Capture(raw)
		if err != nil {
			return ConfigCapture{}, err
		}
		cleanBytes += len(clean)
		if cleanBytes > MaxConfigBytes {
			return ConfigCapture{}, files.ErrSourceLimit
		}
		fingerprints = append(fingerprints, sha256.Sum256(raw))
		result.Files = append(result.Files, ConfigFile{Path: name, Data: clean})
	}
	// Re-read after all codecs finish to catch changes to already-processed files,
	// including same-size replacements whose timestamps were restored. Hashes are
	// ephemeral and never included in the result or diagnostics.
	for i, name := range names {
		raw, err := source.Read(ctx, name, configcodec.MaxBytes)
		if err != nil {
			return ConfigCapture{}, err
		}
		if sha256.Sum256(raw) != fingerprints[i] {
			return ConfigCapture{}, ErrConfigSourceChanged
		}
	}
	final, err := configDirectoryNames(ctx, source)
	if err != nil {
		return ConfigCapture{}, err
	}
	final, err = configNames(final)
	if err != nil {
		return ConfigCapture{}, err
	}
	if !slices.Equal(names, final) {
		return ConfigCapture{}, ErrConfigSourceChanged
	}
	if err := source.Check(ctx); err != nil {
		return ConfigCapture{}, err
	}
	if err := ctx.Err(); err != nil {
		return ConfigCapture{}, err
	}
	return result, nil
}

func reservedProfile(name string) bool {
	name = strings.ToUpper(name)
	switch name {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	return len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) && name[3] >= '1' && name[3] <= '9'
}
