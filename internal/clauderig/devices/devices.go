// Package devices models clauderig-devices.json — a synced registry of the
// machines that share the repo, each with its last-sync time. sync touches this
// machine's entry; status/ui read it to show "who synced when".
package devices

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// FileName is the registry's name at the sync repo root.
const FileName = "clauderig-devices.json"

const schemaVersion = 1

// Device is one machine's entry.
type Device struct {
	Name          string    `json:"name"`
	OS            string    `json:"os"`
	LastSync      time.Time `json:"lastSync"`
	ClaudeVersion string    `json:"claudeVersion,omitempty"`
	// Account is which Claude Code login this device's last sync ran as. See
	// Account — it is the only account provenance anything in the repo carries.
	Account *Account `json:"account,omitempty"`
}

// Account identifies a Claude Code login, and nothing more: the two uuids that
// tell accounts apart plus the email that makes them legible to a person. Read
// from ~/.claude.json's oauthAccount, which also holds the plan, the rate-limit
// tier and (in the wider file) MCP credentials — none of which belong in a
// synced repo, and none of which are recorded here.
//
// This exists because account identity is otherwise invisible downstream: CLI
// transcripts carry no account field at all, so a synced tree — and any restore
// taken from it — cannot otherwise say whose sessions it holds. Desktop's own
// sidecars are already account-partitioned by path
// (claude-code-sessions/<accountUuid>/<organizationUuid>/), and these uuids are
// the same ones, so the two agree by construction.
type Account struct {
	AccountUUID      string `json:"accountUuid,omitempty"`
	OrganizationUUID string `json:"organizationUuid,omitempty"`
	Email            string `json:"email,omitempty"`
}

// Registry is the synced device list.
type Registry struct {
	Schema  int               `json:"schema"`
	Devices map[string]Device `json:"devices"`
}

// Load reads the registry from dir, returning an empty one when absent.
func Load(dir string) (*Registry, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		if os.IsNotExist(err) {
			return &Registry{Schema: schemaVersion, Devices: map[string]Device{}}, nil
		}
		return nil, err
	}
	var r Registry
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Devices == nil {
		r.Devices = map[string]Device{}
	}
	r.Schema = schemaVersion
	return &r, nil
}

// Touch records this machine's sync, under the account it ran as.
//
// A nil acct keeps whatever account the device last recorded instead of erasing
// it: a sync that could not read the identity (not logged in, unreadable
// ~/.claude.json) is not evidence that the machine changed accounts, and losing
// the record would be worse than keeping a slightly stale one.
func (r *Registry) Touch(name, os, claudeVersion string, acct *Account, when time.Time) {
	if r.Devices == nil {
		r.Devices = map[string]Device{}
	}
	if acct == nil {
		acct = r.Devices[name].Account
	}
	r.Devices[name] = Device{Name: name, OS: os, LastSync: when.UTC(), ClaudeVersion: claudeVersion, Account: acct}
}

// Save writes the registry to dir/FileName.
func (r *Registry) Save(dir string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, FileName), append(b, '\n'), 0o644)
}

// List returns devices sorted by most-recent sync first.
func (r *Registry) List() []Device {
	out := make([]Device, 0, len(r.Devices))
	for _, d := range r.Devices {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSync.After(out[j].LastSync) })
	return out
}
