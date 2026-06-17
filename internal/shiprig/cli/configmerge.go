package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/cfgfind"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/confkit"
	"github.com/rigsmith/rigsmith/core/doctor"
	"github.com/rigsmith/rigsmith/core/jsonc"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
)

// shiprigSchemaURL is stamped onto a unified shiprig.jsonc (changeset + pipeline
// in one file). Publishing the schema itself is separate site work.
const shiprigSchemaURL = "https://rigsmith.dev/schemas/shiprig.json"

// configLayoutCheck reports whether the changeset config and the release config
// live in two separate dedicated files, and offers to merge them into one
// `.changeset/shiprig.jsonc`. It only fires for two distinct dedicated files —
// an embedded key (already unified) or a `.rig.json` key (which we must not
// delete) is left alone.
func configLayoutCheck(ws *commands.Workspace) doctor.Result {
	csSrc, err := cfgfind.Find(config.Spec(ws.ChangesetDir))
	if err != nil {
		return doctor.Result{Name: "config layout", Status: doctor.Warn, Detail: err.Error()}
	}
	relSrc, err := cfgfind.Find(releaseConfigSpec(ws.Root, ws.ChangesetDir))
	if err != nil {
		return doctor.Result{Name: "config layout", Status: doctor.Warn, Detail: err.Error()}
	}

	// Two distinct dedicated files (Path set on both) → offer the merge. A keyed
	// source (Path == "") is already unified or lives in .rig.json.
	if csSrc != nil && relSrc != nil && csSrc.Path != "" && relSrc.Path != "" && csSrc.Path != relSrc.Path {
		// Write the unified file in the release config's own directory, so a
		// pipeline's relative step-script (`file`) refs — resolved against the
		// config's BaseDir — keep working (a root release.jsonc → root
		// shiprig.jsonc, not relocated under .changeset/).
		target := filepath.Join(filepath.Dir(relSrc.Path), "shiprig.jsonc")
		rel := target
		if r, err := filepath.Rel(ws.Root, target); err == nil {
			rel = r
		}
		return doctor.Result{
			Name:   "config layout",
			Status: doctor.Warn,
			Detail: fmt.Sprintf("changeset and release config live in two files (%s, %s)",
				filepath.Base(csSrc.Path), filepath.Base(relSrc.Path)),
			Hint:     "shiprig can read both from one file — a `changeset` key in shiprig.jsonc, or a `release` key in config.json",
			FixLabel: "merge both into one " + rel,
			Fix: func(context.Context) error {
				return mergeIntoShiprig(target, csSrc, relSrc)
			},
		}
	}

	detail := "single file"
	if csSrc != nil && csSrc.File != "" {
		detail = "unified in " + filepath.Base(csSrc.File)
	}
	return doctor.Result{Name: "config layout", Status: doctor.OK, Detail: detail}
}

// mergeIntoShiprig writes a single unified `shiprig.jsonc` at target — the
// release pipeline at the top level, the changeset config under a `changeset`
// key — then removes the two original files. target sits in the release config's
// directory so relative step-script refs survive. Comments are not preserved
// (the scaffold header is regenerated) — a one-time migration trade-off.
func mergeIntoShiprig(target string, csSrc, relSrc *cfgfind.Source) error {
	merged, err := mergeUnified(csSrc.Data, relSrc.Data)
	if err != nil {
		return err
	}
	if err := os.WriteFile(target, merged, 0o644); err != nil {
		return err
	}
	// Remove the originals (never the merge target, never a shared .rig.json —
	// excluded already since this path only runs for two dedicated files).
	for _, f := range []string{csSrc.Path, relSrc.Path} {
		if f != "" && f != target {
			os.Remove(f)
		}
	}
	return nil
}

// mergeUnified builds the unified document bytes: the release object at the top
// level, with the changeset object nested under `changeset` and a `$schema`
// header. Both inputs are decoded generically so nothing is dropped (ecosystem
// blocks, unknown keys); keys come out alphabetized (JSON map order).
func mergeUnified(changesetData, releaseData []byte) ([]byte, error) {
	cs := map[string]any{}
	if err := jsonc.Unmarshal(changesetData, &cs); err != nil {
		return nil, fmt.Errorf("parsing changeset config: %w", err)
	}
	delete(cs, "$schema")

	unified := map[string]any{}
	if len(releaseData) > 0 {
		if err := jsonc.Unmarshal(releaseData, &unified); err != nil {
			return nil, fmt.Errorf("parsing release config: %w", err)
		}
	}
	delete(unified, "$schema")
	unified["changeset"] = cs

	w := confkit.Writer{SchemaURL: shiprigSchemaURL}
	return w.Document("shiprig config — changeset + release pipeline in one file.\nMerged by `shiprig doctor`; edit freely.", unified)
}
