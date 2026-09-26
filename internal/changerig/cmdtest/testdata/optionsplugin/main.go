// optionsplugin is a changelog generator plugin for the tests: it renders the
// release heading and the options object it was sent, and each change's
// commit exactly as it arrived, so a test can see what reached it.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	var req struct {
		Package struct {
			NewVersion string `json:"newVersion"`
		} `json:"package"`
		Options json.RawMessage `json:"options"`
		Changes []struct {
			Summary string `json:"summary"`
			Commit  string `json:"commit"`
			PR      int    `json:"pr"`
			Author  string `json:"author"`
			Deps    bool   `json:"dependencies"`
		} `json:"changes"`
		DependencyUpdates []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
			NewVersion  string `json:"newVersion"`
		} `json:"dependencyUpdates"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	opts := "none"
	if len(req.Options) > 0 {
		opts = string(req.Options)
	}
	fmt.Printf("## %s\n\noptions: %s\n", req.Package.NewVersion, opts)
	for _, c := range req.Changes {
		if c.Deps {
			fmt.Printf("flagged as dependencies: %s\n", strings.SplitN(c.Summary, "\n", 2)[0])
			continue
		}
		fmt.Printf("change: %s commit=%s pr=%d author=%s\n", strings.TrimSpace(c.Summary), c.Commit, c.PR, c.Author)
	}
	for _, d := range req.DependencyUpdates {
		fmt.Printf("dependency: %s (%s) @ %s\n", d.Name, d.DisplayName, d.NewVersion)
	}
}
