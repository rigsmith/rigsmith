package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/cfgfind"
	"github.com/rigsmith/rigsmith/core/jsonc"
)

// stackReconstructManifest reads committed bytes only. The seed clone stays
// separate from the fused repository and is never adopted as a home.
func stackReconstructManifest(ctx context.Context, checkout, temp, manifest, seed, allowed string) ([]byte, string, error) {
	if manifest != "" && seed != "" {
		return nil, "", fmt.Errorf("--manifest and --seed are mutually exclusive")
	}
	var data []byte
	var seedCommit string
	var err error
	if seed != "" {
		data, seedCommit, err = stackCISeed(ctx, checkout, filepath.Join(temp, "seed"), seed, allowed)
	} else if manifest == "" {
		// Discover the stackspace's own manifest from HEAD, including inline
		// configuration, without letting dirty or untracked files select it.
		var paths []byte
		paths, err = stackCIGit(ctx, checkout, "ls-tree", "--name-only", "HEAD", "--", "rig.stack.jsonc", "rig.stack.json", ".rig.json")
		if err != nil {
			return nil, "", err
		}
		dir := filepath.Join(temp, "manifest")
		if err = os.Mkdir(dir, 0700); err != nil {
			return nil, "", err
		}
		for _, path := range strings.Fields(string(paths)) {
			committed, e := stackCIGit(ctx, checkout, "show", "HEAD:"+path)
			if e != nil {
				return nil, "", e
			}
			if err = os.WriteFile(filepath.Join(dir, path), committed, 0600); err != nil {
				return nil, "", err
			}
		}
		var src *cfgfind.Source
		src, err = cfgfind.Find(stackSpec(dir))
		if err == nil {
			if src == nil {
				err = fmt.Errorf("no committed stack manifest; use --manifest or --seed")
			} else {
				data = src.Data
			}
		}
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
			if err == nil && filepath.Base(p) == ".rig.json" {
				var config struct {
					Stack json.RawMessage `json:"stack"`
				}
				err = jsonc.Unmarshal(data, &config)
				data = config.Stack
				if err == nil && (len(data) == 0 || string(data) == "null") {
					err = fmt.Errorf("committed .rig.json has no stack block")
				}
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
