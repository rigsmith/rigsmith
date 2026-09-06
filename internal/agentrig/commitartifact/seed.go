package commitartifact

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

const seedRefName = "refs/rig/seed"

// SeedStore is the reserved private seed-bundle substore of a capture store.
// It inherits the per-artifact size limit. Keep the entire capture directory
// outside vendor sources and backups; neither captures nor seeds expire yet.
func SeedStore(captures artifact.Store) artifact.Store {
	return artifact.Store{Dir: filepath.Join(captures.Dir, "seeds"), MaxBytes: captures.MaxBytes}
}

// RetainSeed seals a complete Git seed bundle before a capture can acknowledge
// its dependency. The caller holds the source staging lease throughout this call.
// Only committed objects are imported; canonical refs, index and working files
// remain untouched. Same-SHA retries reflush the existing seed without requiring
// the source repository. An uncertain result must not be written into a capture.
func RetainSeed(ctx context.Context, captures artifact.Store, source, commit string) (string, error) {
	if !filepath.IsAbs(captures.Dir) || !filepath.IsAbs(source) || !objectID(commit) {
		return "", ErrInvalid
	}
	key := artifact.Key([]byte("git-seed-bundle-v1:" + commit))
	return SeedStore(captures).BuildWithMetadata(ctx, key, func(ctx context.Context, output string, meta *artifact.Metadata) error {
		work := filepath.Dir(output)
		repo, err := initRepo(ctx, filepath.Join(work, "git"), commit)
		if err != nil {
			return err
		}
		if err = repo.importRef(ctx, source, commit, seedRefName, commit); err != nil {
			return err
		}
		bundle := filepath.Join(output, "seed.bundle")
		if _, err = repo.run(ctx, nil, "bundle", "create", bundle, seedRefName); err != nil {
			return err
		}
		// Validate in an empty repository, not the source or the writer. This
		// proves the bundle does not need objects from canonical staging later.
		check, err := initRepo(ctx, filepath.Join(work, "verify"), commit)
		if err != nil {
			return err
		}
		if err = check.importSeed(ctx, bundle, commit); err != nil {
			return err
		}
		meta.BaseReference = commit
		return nil
	})
}

// loadSeed imports only the seed artifact named by the capture. Missing or
// corrupt seed storage is never permission to fall back to a live repository.
func (r gitRepo) loadSeed(ctx context.Context, captures artifact.Store, ref, commit, dest string) error {
	if ref == "" || !objectID(commit) {
		return ErrInvalid
	}
	extracted, err := SeedStore(captures).ExtractWithMetadata(ctx, ref, dest)
	if err != nil {
		return err
	}
	if extracted.Metadata.BaseReference != commit || extracted.Metadata.SeedReference != "" {
		return ErrInvalid
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != "seed.bundle" || !entries[0].Type().IsRegular() {
		return ErrInvalid
	}
	return r.importSeed(ctx, filepath.Join(dest, "seed.bundle"), commit)
}

func (r gitRepo) importSeed(ctx context.Context, bundle, commit string) error {
	if _, err := r.run(ctx, nil, "bundle", "verify", bundle); err != nil {
		return err
	}
	if err := r.importRef(ctx, bundle, seedRefName, seedRefName, commit); err != nil {
		return err
	}
	if _, err := r.run(ctx, nil, "fsck", "--strict", "--no-reflogs"); err != nil {
		return err
	}
	return nil
}

func (r gitRepo) importRef(ctx context.Context, source, from, to, commit string) error {
	if _, err := r.run(ctx, nil, "fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "--", source, from+":"+to); err != nil {
		return err
	}
	got, err := r.run(ctx, nil, "rev-parse", to+"^{commit}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(got) != commit {
		return fmt.Errorf("retained seed commit mismatch: %w", ErrInvalid)
	}
	return nil
}
