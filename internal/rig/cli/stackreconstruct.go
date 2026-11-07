package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// stackReconstructManifest reads committed bytes only. The seed clone stays
// separate from the fused repository and is never adopted as a home.
func stackReconstructManifest(ctx context.Context, checkout, temp, manifest, seed, allowed string) ([]byte, string, error) {
	if (manifest == "") == (seed == "") {
		return nil, "", fmt.Errorf("exactly one of --manifest or --seed is required")
	}
	var data []byte
	var seedCommit string
	var err error
	if seed != "" {
		data, seedCommit, err = stackCISeed(ctx, checkout, filepath.Join(temp, "seed"), seed, allowed)
	} else {
		p := manifest
		if !filepath.IsAbs(p) {
			p = filepath.Join(checkout, p)
		}
		var rel string
		rel, err = filepath.Rel(checkout, p)
		if err == nil {
			data, err = stackCIGit(ctx, checkout, "show", "HEAD:"+filepath.ToSlash(rel))
			if err != nil {
				err = fmt.Errorf("manifest must be committed: %w", err)
			}
		}
	}
	if err != nil {
		return nil, "", err
	}
	return data, seedCommit, nil
}

func stackReconstructRepository(ctx context.Context, root string, data []byte, allowed string) (*stackManifest, error) {
	if err := os.WriteFile(filepath.Join(root, "rig.stack.jsonc"), data, 0600); err != nil {
		return nil, err
	}
	if _, err := stackCIGit(ctx, root, "init", "-q"); err != nil {
		return nil, err
	}
	m, _, err := loadStackManifest(root)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("no stack manifest")
	}
	if err = stackCIValidate(m, allowed); err != nil {
		return nil, err
	}
	return m, nil
}

// Only edge members retain fork selection. The CI checkout member is supplied
// separately; every other member must have a full cursor unless CI requests tips.
func stackReconstructCursors(m *stackManifest, checkoutMember string, tip bool, edge map[string]bool) error {
	for _, n := range m.names() {
		if edge[n] {
			continue
		}
		r := m.Repos[n]
		r.TrackBranch = ""
		delete(m.LastPropose, n)
		if tip {
			r.UpstreamCommit = ""
			r.UpstreamTag = ""
			delete(m.LastSync, n)
		} else if n != checkoutMember {
			if !stackIsSHA(m.cursor(n)) {
				return fmt.Errorf("member %s has no full manifest cursor", n)
			}
			r.UpstreamBranch = ""
			r.Branch = ""
			r.UpstreamTag = ""
			r.UpstreamCommit = m.cursor(n)
		}
	}
	if len(edge) == 0 {
		m.LastPropose = nil
	}
	return nil
}

func stackReconstructWrite(root string, m *stackManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "rig.stack.jsonc"), data, 0600)
}
