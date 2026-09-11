package adapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"

	"github.com/rigsmith/rigsmith/internal/codexrig/configcodec"
)

// PrepareVersionedConfigRestore requires pinned schema/runtime rules and the
// caller's destination-readiness checks. Destination checks must cover local
// references, credentials, managed layers and runtime-only constraints, without
// executing configured helpers or exposing private diagnostics. No command is
// exposed and no files are written until the resulting plan is applied.
func PrepareVersionedConfigRestore(ctx context.Context, root Root, backup ConfigCapture, version string, destination ConfigRestoreValidator) (*ConfigRestorePlan, error) {
	return PrepareLayeredConfigRestore(ctx, root, backup, version, func(context.Context) (configcodec.ValidationLayers, error) {
		return configcodec.ValidationLayers{}, nil
	}, destination)
}

// ConfigLayerSource rereads the current trusted destination context. It must
// detect source replacement and changes in selection/trust, bound its reads,
// honor cancellation and return private bytes without executing helpers. The
// caller owns any pinned handles and keeps them alive until the plan is closed.
// A function returning a permanently cached snapshot is not a production source.
type ConfigLayerSource func(context.Context) (configcodec.ValidationLayers, error)

var ErrConfigLayersChanged = errors.New("Codex configuration validation layers changed or became unavailable")

// PrepareLayeredConfigRestore snapshots trusted context and binds its ordered
// content fingerprint and source to the plan. Check/Apply reread it, refusing
// changes before each replacement. Source identity and trust remain the source's
// responsibility; this is not an atomic lock against external policy writers.
// The destination callback remains mandatory for other readiness constraints.
func PrepareLayeredConfigRestore(ctx context.Context, root Root, backup ConfigCapture, version string, source ConfigLayerSource, destination ConfigRestoreValidator) (*ConfigRestorePlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if version != configcodec.SupportedConfigVersion {
		return nil, configcodec.ErrUnsupportedVersion
	}
	if destination == nil || source == nil {
		return nil, ErrConfigRestoreInput
	}
	layers, expected, err := readLayerSnapshot(ctx, source)
	if err != nil {
		return nil, err
	}
	check := func(ctx context.Context) error {
		_, current, err := readLayerSnapshot(ctx, source)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || current != expected {
			return ErrConfigLayersChanged
		}
		return nil
	}
	plan, err := PrepareConfigRestore(ctx, root, backup, func(ctx context.Context, proposed []ConfigFile) error {
		var base []byte
		var profiles [][]byte
		for _, file := range proposed {
			if file.Path == "config.toml" {
				base = file.Data
			} else {
				profiles = append(profiles, file.Data)
			}
		}
		if err := configcodec.ValidateConfigSetWithLayers(ctx, version, base, profiles, layers); err != nil {
			return err
		}
		if err := check(ctx); err != nil {
			return err
		}
		if err := destination(ctx, proposed); err != nil {
			return err
		}
		return check(ctx)
	})
	if err != nil {
		return nil, err
	}
	plan.checkContext = check
	return plan, nil
}

func readLayerSnapshot(ctx context.Context, source ConfigLayerSource) (configcodec.ValidationLayers, [32]byte, error) {
	var empty configcodec.ValidationLayers
	var zero [32]byte
	if err := ctx.Err(); err != nil {
		return empty, zero, err
	}
	layers, err := source(ctx)
	if ctx.Err() != nil {
		return empty, zero, ctx.Err()
	}
	if err != nil {
		return empty, zero, ErrConfigLayersChanged
	}
	count := len(layers.Before) + len(layers.After)
	if layers.Requirements != nil {
		count++
	}
	if count > 16 {
		return empty, zero, ErrConfigLayersChanged
	}
	total := 0
	for _, group := range [][][]byte{layers.Before, layers.After, {layers.Requirements}} {
		for _, data := range group {
			if len(data) > configcodec.MaxBytes {
				return empty, zero, ErrConfigLayersChanged
			}
			total += len(data)
		}
	}
	if total > MaxConfigBytes {
		return empty, zero, ErrConfigLayersChanged
	}
	h := sha256.New()
	h.Write([]byte("codexrig-validation-layers-v1"))
	cloneData := func(data []byte) []byte {
		copied := bytes.Clone(data)
		var marker [9]byte
		if data != nil {
			marker[0] = 1
		}
		binary.BigEndian.PutUint64(marker[1:], uint64(len(copied)))
		h.Write(marker[:])
		h.Write(copied)
		return copied
	}
	cloneGroup := func(group [][]byte) [][]byte {
		var count [8]byte
		binary.BigEndian.PutUint64(count[:], uint64(len(group)))
		h.Write(count[:])
		copied := make([][]byte, len(group))
		for i, data := range group {
			copied[i] = cloneData(data)
		}
		return copied
	}
	copied := configcodec.ValidationLayers{Before: cloneGroup(layers.Before), After: cloneGroup(layers.After), Requirements: cloneData(layers.Requirements)}
	copy(zero[:], h.Sum(nil))
	return copied, zero, ctx.Err()
}
