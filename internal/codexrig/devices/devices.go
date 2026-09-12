// Package devices models codexrig-devices.json, the registry of machines that
// have synced into a repo. It answers "is another machine's copy older than
// mine?", which is the question behind every coverage warning a search prints.
package devices

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// FileName is the registry's name in the synced repo.
const FileName = "codexrig-devices.json"

const schemaVersion = 1

// Account is the login a device was syncing as. Identity only — never a token.
type Account struct {
	Email     string `json:"email,omitempty"`
	AccountID string `json:"accountId,omitempty"`
}

// Device is one machine's last-known state.
type Device struct {
	Name         string    `json:"name"`
	OS           string    `json:"os"`
	LastSync     time.Time `json:"lastSync"`
	CodexVersion string    `json:"codexVersion,omitempty"`
	Account      *Account  `json:"account,omitempty"`
}

// Registry is the whole file.
type Registry struct {
	Schema  int               `json:"schema"`
	Devices map[string]Device `json:"devices"`
}

// Load reads the registry. An absent file is an empty registry, not an error —
// the first machine to sync has nobody to read about.
func Load(dir string) (*Registry, error) {
	r := &Registry{Schema: schemaVersion, Devices: map[string]Device{}}
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, err
	}
	if r.Devices == nil {
		r.Devices = map[string]Device{}
	}
	return r, nil
}

// Save writes the registry.
func (r *Registry) Save(dir string) error {
	r.Schema = schemaVersion
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, FileName), append(b, '\n'), 0o644)
}

// Touch records a machine's sync. A nil account leaves whatever was known
// before: a run that could not read the login must not erase a record of it.
func (r *Registry) Touch(name, osToken, codexVersion string, acct *Account, when time.Time) {
	if r.Devices == nil {
		r.Devices = map[string]Device{}
	}
	d := r.Devices[name]
	d.Name, d.OS, d.LastSync = name, osToken, when
	if codexVersion != "" {
		d.CodexVersion = codexVersion
	}
	if acct != nil {
		d.Account = acct
	}
	r.Devices[name] = d
}

// Remove forgets a machine.
func (r *Registry) Remove(name string) bool {
	if _, ok := r.Devices[name]; !ok {
		return false
	}
	delete(r.Devices, name)
	return true
}

// Has reports whether a machine is registered.
func (r *Registry) Has(name string) bool { _, ok := r.Devices[name]; return ok }

// List returns the devices, most recently synced first.
func (r *Registry) List() []Device {
	out := make([]Device, 0, len(r.Devices))
	for _, d := range r.Devices {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastSync.Equal(out[j].LastSync) {
			return out[i].LastSync.After(out[j].LastSync)
		}
		return out[i].Name < out[j].Name
	})
	return out
}
