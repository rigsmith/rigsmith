package adapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/files"
	"github.com/rigsmith/rigsmith/internal/codexrig/configcodec"
)

var (
	ErrConfigRestoreInput = errors.New("invalid Codex configuration restore input")
	ErrConfigValidation   = errors.New("Codex configuration restore validation failed")
	ErrConfigPlanClosed   = errors.New("Codex configuration restore plan is closed")
)

// ConfigRestoreValidator must validate the complete proposed base/profile set
// for the caller's supported Codex version and destination. It receives private
// destination configuration, including credentials, in detached copies. It must
// not publish it or execute configured helpers. Returning nil approves these
// bytes only; it does not authorize file replacement. A validator is required.
type ConfigRestoreValidator func(context.Context, []ConfigFile) error

// ConfigRestoreChange describes an incoming file without exposing its contents.
// Action is create, update, or unchanged. Files absent from the backup are kept.
type ConfigRestoreChange struct {
	Path   string
	Action string
}

// ConfigRestorePlan pins a destination while an entire restore is prepared.
// Private bytes and fingerprints are deliberately unexported and never appear
// in its formatted representation. Close every plan. Methods are sequential;
// Check detects observed changes, not concurrent writers after the last check.
// Preparation does not write files; Apply consumes the plan for replacement.
// The caller supplies the supported-version validator.
type ConfigRestorePlan struct {
	source      configSource
	closeSource func() error
	closed      bool
	original    map[string][32]byte
	incoming    []string
	proposed    []ConfigFile
	changes     []ConfigRestoreChange
}

func (p ConfigRestorePlan) String() string   { return "Codex configuration restore plan (private)" }
func (p ConfigRestorePlan) GoString() string { return p.String() }

// Changes returns a detached, sorted summary of incoming files. It contains no
// destination bytes, fingerprints or root path. A closed plan has no changes.
func (p *ConfigRestorePlan) Changes() []ConfigRestoreChange {
	return slices.Clone(p.changes)
}

// Close releases the pinned directory and drops references to private bytes.
// It is idempotent. This is not a guarantee of memory zeroization by the runtime.
func (p *ConfigRestorePlan) Close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	p.proposed, p.original, p.incoming, p.changes = nil, nil, nil, nil
	closeSource := p.closeSource
	p.source, p.closeSource = nil, nil
	if closeSource != nil {
		if err := closeSource(); err != nil {
			return files.ErrSource
		}
	}
	return nil
}

// PrepareConfigRestore validates backup names/content, reads bounded destination
// files, merges local-only values, and validates the complete proposed set before
// returning a plan. It never creates a missing home or prunes omitted profiles.
// Any error returns no plan. All input/validator buffers are detached from it.
func PrepareConfigRestore(ctx context.Context, root Root, backup ConfigCapture, validate ConfigRestoreValidator) (*ConfigRestorePlan, error) {
	if root.Kind != CodexHome || validate == nil {
		return nil, ErrConfigRestoreInput
	}
	incoming, err := restoreInputs(ctx, backup)
	if err != nil {
		return nil, err
	}
	source, err := files.OpenSource(ctx, root.Path)
	if err != nil {
		return nil, err
	}
	retained := false
	defer func() {
		if !retained {
			source.Close()
		}
	}()
	plan, err := prepareConfigRestore(ctx, source, incoming, validate)
	if err != nil {
		return nil, err
	}
	plan.closeSource = source.Close
	retained = true
	return plan, nil
}

func restoreInputs(ctx context.Context, backup ConfigCapture) ([]ConfigFile, error) {
	if len(backup.Files) > MaxConfigFiles {
		return nil, files.ErrSourceLimit
	}
	names := make([]string, 0, len(backup.Files))
	for _, file := range backup.Files {
		names = append(names, file.Path)
	}
	selected, err := configNames(names)
	if err != nil {
		return nil, err
	}
	if len(selected) != len(names) {
		return nil, ErrConfigRestoreInput
	}
	out := make([]ConfigFile, 0, len(names))
	total, encodedTotal := 0, 0
	for _, file := range backup.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(file.Data) > configcodec.MaxBytes {
			return nil, configcodec.ErrSize
		}
		total += len(file.Data)
		if total > MaxConfigBytes {
			return nil, files.ErrSourceLimit
		}
		// Refuse unsanitized input even if local values would mask it in a merge.
		clean, err := configcodec.Restore(file.Data, nil)
		if err != nil {
			return nil, err
		}
		encodedTotal += len(clean)
		if encodedTotal > MaxConfigBytes {
			return nil, files.ErrSourceLimit
		}
		out = append(out, ConfigFile{Path: file.Path, Data: clean})
	}
	slices.SortFunc(out, func(a, b ConfigFile) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

func prepareConfigRestore(ctx context.Context, source configSource, incoming []ConfigFile, validate ConfigRestoreValidator) (*ConfigRestorePlan, error) {
	plan := &ConfigRestorePlan{source: source, original: map[string][32]byte{}}
	for _, file := range incoming {
		plan.incoming = append(plan.incoming, file.Path)
	}
	names, err := plan.destinationNames(ctx)
	if err != nil {
		return nil, err
	}
	current := map[string][]byte{}
	canonical := map[string][]byte{}
	total := 0
	for _, name := range names {
		raw, err := source.Read(ctx, name, configcodec.MaxBytes)
		if err != nil {
			return nil, err
		}
		total += len(raw)
		if total > MaxConfigBytes {
			return nil, files.ErrSourceLimit
		}
		// Also reject malformed local profiles absent from the backup. They are
		// part of the complete configuration presented for validation.
		encoded, err := configcodec.Restore(nil, raw)
		if err != nil {
			return nil, err
		}
		current[name], canonical[name] = raw, encoded
		plan.original[name] = sha256.Sum256(raw)
	}
	for _, file := range incoming {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		local, exists := current[file.Path]
		merged, err := configcodec.Restore(file.Data, local)
		if err != nil {
			return nil, err
		}
		action := "create"
		if exists {
			action = "update"
			if bytes.Equal(merged, canonical[file.Path]) {
				action = "unchanged"
				merged = local // Preserve original comments/format for a no-op.
			}
		}
		current[file.Path] = merged
		plan.changes = append(plan.changes, ConfigRestoreChange{Path: file.Path, Action: action})
	}
	if len(current) > MaxConfigFiles {
		return nil, files.ErrSourceLimit
	}
	total = 0
	for name, data := range current {
		total += len(data)
		if total > MaxConfigBytes {
			return nil, files.ErrSourceLimit
		}
		plan.proposed = append(plan.proposed, ConfigFile{Path: name, Data: data})
	}
	slices.SortFunc(plan.proposed, func(a, b ConfigFile) int { return strings.Compare(a.Path, b.Path) })
	if err := plan.Check(ctx); err != nil {
		return nil, err
	}
	validationFiles := make([]ConfigFile, len(plan.proposed))
	for i, file := range plan.proposed {
		validationFiles[i] = ConfigFile{Path: file.Path, Data: bytes.Clone(file.Data)}
	}
	if err := validate(ctx, validationFiles); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Third-party validators can echo credentials or parser source excerpts.
		return nil, ErrConfigValidation
	}
	if err := plan.Check(ctx); err != nil {
		return nil, err
	}
	return plan, nil
}

func (p *ConfigRestorePlan) destinationNames(ctx context.Context) ([]string, error) {
	names, err := configDirectoryNames(ctx, p.source, 0)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if candidate, ok := Classify(CodexHome, strings.ToLower(name)); ok &&
			(candidate.Kind == "config" || candidate.Kind == "config-profile") {
			if _, exact := Classify(CodexHome, name); !exact {
				return nil, ErrConfigNames
			}
		}
	}
	// Detect case aliases even when Classify would exclude them, e.g.
	// CONFIG.TOML beside an incoming config.toml on a case-sensitive machine.
	proposedCount := len(names)
	for _, incoming := range p.incoming {
		present := false
		for _, name := range names {
			if name != incoming && strings.EqualFold(name, incoming) {
				return nil, ErrConfigNames
			}
			present = present || name == incoming
		}
		if !present {
			proposedCount++
		}
	}
	// Refuse a restore that would exceed the user-entry bound after creation,
	// before staging or installing any files.
	if proposedCount > maxConfigDirectoryEntries {
		return nil, files.ErrSourceLimit
	}
	return configNames(names)
}

// Check re-reads the pinned destination and detects edits, profile arrivals or
// removals since preparation, including same-size edits with restored mtimes.
// Apply also checks immediately before each replacement under writer ownership.
// This check is not an atomic filesystem compare-and-swap.
func (p *ConfigRestorePlan) Check(ctx context.Context) error {
	if p.closed || p.source == nil {
		return ErrConfigPlanClosed
	}
	names, err := p.destinationNames(ctx)
	if err != nil {
		return err
	}
	if len(names) != len(p.original) {
		return ErrConfigSourceChanged
	}
	for _, name := range names {
		fingerprint, exists := p.original[name]
		if !exists {
			return ErrConfigSourceChanged
		}
		raw, err := p.source.Read(ctx, name, configcodec.MaxBytes)
		if err != nil {
			return err
		}
		if sha256.Sum256(raw) != fingerprint {
			return ErrConfigSourceChanged
		}
	}
	final, err := p.destinationNames(ctx)
	if err != nil {
		return err
	}
	if !slices.Equal(names, final) {
		return ErrConfigSourceChanged
	}
	return p.source.Check(ctx)
}

// Keep accidental fmt logging of the plan from exposing private configuration.
var _ fmt.Stringer = (*ConfigRestorePlan)(nil)
var _ fmt.GoStringer = (*ConfigRestorePlan)(nil)
