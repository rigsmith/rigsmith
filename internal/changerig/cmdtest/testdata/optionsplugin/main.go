// optionsplugin is a changelog generator plugin for the tests: it renders the
// release heading and the options object it was sent, so a test can see
// what reached it.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	var req struct {
		Package struct {
			NewVersion string `json:"newVersion"`
		} `json:"package"`
		Options json.RawMessage `json:"options"`
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
}
