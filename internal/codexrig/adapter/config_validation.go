package adapter

import (
	"context"

	"github.com/rigsmith/rigsmith/internal/codexrig/configcodec"
)

// PrepareVersionedConfigRestore requires pinned schema/runtime rules and the
// caller's destination-readiness checks. Destination checks must cover local
// references, credentials, managed layers and runtime-only constraints, without
// executing configured helpers or exposing private diagnostics. No command is
// exposed and no files are written until the resulting plan is applied.
func PrepareVersionedConfigRestore(ctx context.Context, root Root, backup ConfigCapture, version string, destination ConfigRestoreValidator) (*ConfigRestorePlan, error) {
	return PrepareLayeredConfigRestore(ctx, root, backup, version, configcodec.ValidationLayers{}, destination)
}

// PrepareLayeredConfigRestore checks proposed home files in caller-selected
// destination layers. Layers must come from trusted local resolution, never the
// backup. The caller must pin/recheck external layer sources through application
// and enforce requirements beyond permission catalog selection. Those sources
// are not covered by ConfigRestorePlan.Check. The destination callback remains
// mandatory; this API does not discover layers or certify startup readiness.
func PrepareLayeredConfigRestore(ctx context.Context, root Root, backup ConfigCapture, version string, layers configcodec.ValidationLayers, destination ConfigRestoreValidator) (*ConfigRestorePlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if version != configcodec.SupportedConfigVersion {
		return nil, configcodec.ErrUnsupportedVersion
	}
	if destination == nil {
		return nil, ErrConfigRestoreInput
	}
	return PrepareConfigRestore(ctx, root, backup, func(ctx context.Context, proposed []ConfigFile) error {
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
		return destination(ctx, proposed)
	})
}
