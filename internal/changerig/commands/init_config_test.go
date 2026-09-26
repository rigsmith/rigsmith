package commands

import (
	"encoding/json"
	"strings"
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
		// JSON can't hold invalid UTF-8: encoding replaces it with U+FFFD.
		want := strings.ToValidUTF8(repo, "\uFFFD")
		if len(cfg.Changelog) != 2 || json.Unmarshal(cfg.Changelog[1], &opts) != nil || opts.Repo != want {
			t.Errorf("repo %q: changelog = %s, want repo %q", repo, cfg.Changelog, want)
		}
	}
}
