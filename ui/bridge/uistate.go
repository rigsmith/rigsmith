package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

// uiStateFile is the UI's own settings, next to clauderig's config rather than
// inside it.
//
// Deliberately a separate file. ~/.clauderig/config.json is the CLI's, with a
// published schema, a scaffold template and a knownKeys list that a new field
// has to be threaded through; "does this Mac want a popup" is none of the CLI's
// business and would be a config key nothing but a window ever reads.
//
// Machine-local by construction, which is the right scope for it: whether the
// Desktop notice is welcome is a fact about the machine you are sitting at, not
// about your account, and ~/.clauderig sits outside every sync root anyway.
const uiStateFile = "ui-state.json"

// desktopWarnKey records whether the Claude Desktop launch notice is wanted.
// Absent means yes: a preference nobody has expressed is not a "no".
const desktopWarnKey = "desktopWarnOnLaunch"

// uiState serialises the whole file as a map so one pane writing its own
// setting cannot drop another's. There is one key today and this is what keeps
// the second one from costing somebody their first.
type uiState struct {
	mu   sync.Mutex
	path func() (string, error)
}

func defaultUIState() *uiState {
	return &uiState{path: func() (string, error) {
		dir, err := config.Dir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, uiStateFile), nil
	}}
}

// Bool reads one setting, falling back to def when the file is missing,
// unreadable or does not hold that key.
//
// A preference file we cannot read must not silently answer "false": every
// setting here is a warning somebody may be relying on, and the failure to read
// one is not the same as being asked to turn it off.
func (u *uiState) Bool(key string, def bool) bool {
	u.mu.Lock()
	defer u.mu.Unlock()

	all, err := u.read()
	if err != nil {
		return def
	}
	if v, ok := all[key].(bool); ok {
		return v
	}
	return def
}

// SetBool records one setting, leaving every other key in the file alone.
func (u *uiState) SetBool(key string, value bool) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	path, err := u.path()
	if err != nil {
		return err
	}
	all, err := u.read()
	if err != nil {
		// An unreadable file is not a reason to refuse the setting; it is a
		// reason not to inherit whatever could not be parsed out of it.
		all = map[string]any{}
	}
	all[key] = value

	body, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if mkerr := os.MkdirAll(filepath.Dir(path), 0o755); mkerr != nil {
		return mkerr
	}
	// Written through a temp file and renamed: a half-written settings file
	// would come back as "unreadable", which every reader above answers with
	// the default — turning a crash mid-save into a setting silently undone.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ui-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, werr := tmp.Write(body); werr != nil {
		tmp.Close()
		return werr
	}
	if cerr := tmp.Close(); cerr != nil {
		return cerr
	}
	if cerr := os.Chmod(tmp.Name(), 0o644); cerr != nil {
		return cerr
	}
	return os.Rename(tmp.Name(), path)
}

// read loads the file. A missing one is an empty setting set, not an error:
// nobody has expressed a preference yet.
func (u *uiState) read() (map[string]any, error) {
	path, err := u.path()
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	all := map[string]any{}
	if uerr := json.Unmarshal(body, &all); uerr != nil {
		return nil, uerr
	}
	return all, nil
}
