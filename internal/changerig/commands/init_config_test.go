package commands

import (
	"encoding/json"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
)

// The config init writes is JSON whatever the repo string holds: it's read
// from a remote URL, and Go's %q quoting isn't JSON's.
func TestRenderConfigIsJSONForAnyRepo(t *testing.T) {
	for _, repo := range []string{"acme/widgets", `we"ird/re\\po`, "ctl/\x01repo", "bad/\xffutf8"} {
		var cfg struct {
			Changelog []json.RawMessage `json:"changelog"`
		}
		if err := json.Unmarshal([]byte(renderConfig(config.SourceChangesets, repo)), &cfg); err != nil {
			t.Errorf("repo %q: config isn't JSON: %v", repo, err)
			continue
		}
		var opts struct{ Repo string }
		if len(cfg.Changelog) != 2 || json.Unmarshal(cfg.Changelog[1], &opts) != nil {
			t.Errorf("repo %q: changelog = %s", repo, cfg.Changelog)
		}
	}
}
