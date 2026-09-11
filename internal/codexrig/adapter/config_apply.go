package adapter

import (
	"context"
	"crypto/sha256"
	"errors"

	"github.com/rigsmith/rigsmith/internal/agentrig/files"
	"github.com/rigsmith/rigsmith/internal/codexrig/configcodec"
)

// ConfigRestoreResult reports only confirmed replacements and an uncertain
// current filename. On error, earlier Applied files remain installed. There is
// no automatic rollback or retry; inspect the destination and prepare a new plan.
type ConfigRestoreResult struct {
	Applied   []string
	Uncertain string
}

type configReplacements interface {
	Stage(context.Context, string, files.ExpectedFile, []byte, int64) error
	Apply(context.Context, string) error
	Close() error
}

// Apply consumes and closes a plan. It obtains the shared destination writer
// lock, rechecks the full plan, stages all changes, then rechecks before each
// replacement. Participating restore writers serialize; Codex and other editors
// must be idle through application. This is not a multi-file transaction. A
// supported-version validator is still required during PrepareConfigRestore.
func (p *ConfigRestorePlan) Apply(ctx context.Context) (result ConfigRestoreResult, err error) {
	if p.closed || p.source == nil {
		return result, ErrConfigPlanClosed
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	changed := false
	for _, change := range p.changes {
		changed = changed || change.Action != "unchanged"
	}
	if !changed {
		return result, p.Check(ctx)
	}
	source, ok := p.source.(*files.Source)
	if !ok {
		return result, ErrConfigRestoreInput
	}
	batch, err := files.BeginReplace(ctx, source)
	if err != nil {
		return result, err
	}
	return p.apply(ctx, batch)
}

func (p *ConfigRestorePlan) apply(ctx context.Context, batch configReplacements) (result ConfigRestoreResult, err error) {
	defer func() { err = errors.Join(err, batch.Close()) }()
	if err := p.Check(ctx); err != nil {
		return result, err
	}
	proposed := make(map[string][]byte, len(p.proposed))
	for _, file := range p.proposed {
		proposed[file.Path] = file.Data
	}
	for _, change := range p.changes {
		if change.Action == "unchanged" {
			continue
		}
		digest, exists := p.original[change.Path]
		if err := batch.Stage(ctx, change.Path, files.ExpectedFile{Exists: exists, SHA256: digest}, proposed[change.Path], configcodec.MaxBytes); err != nil {
			return result, err
		}
	}
	for _, change := range p.changes {
		if change.Action == "unchanged" {
			continue
		}
		if err := p.Check(ctx); err != nil {
			return result, err
		}
		if err := batch.Apply(ctx, change.Path); err != nil {
			if errors.Is(err, files.ErrReplacementUncertain) {
				result.Uncertain = change.Path
			}
			return result, err
		}
		result.Applied = append(result.Applied, change.Path)
		// Later full-set checks account for our own confirmed replacements,
		// while still checking retained/unchanged profiles for external edits.
		p.original[change.Path] = sha256.Sum256(proposed[change.Path])
	}
	if err := p.Check(ctx); err != nil {
		return result, err
	}
	return result, nil
}
