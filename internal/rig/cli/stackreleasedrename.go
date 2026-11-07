package cli

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rigsmith/rigsmith/core/ecosystem/dotnet"
	"github.com/rigsmith/rigsmith/core/plugin"
)

type stackReleaseSelection struct {
	Reference, Original string
}

func (s stackReleaseSelection) symbol() string {
	return "STACK_" + strings.ToUpper(dotnet.StackSourceName(s.Reference))
}

// Read raw identities too: discovery can omit Update, unused central entries or
// generated files. Such an id must not silently survive a member migration.
func stackReleaseReferenceIDs(root string, edits []stackReleaseEdit) ([]string, error) {
	ids := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			switch strings.ToLower(e.Name()) {
			case ".git", "bin", "obj", "vendor", "node_modules", "packages", "generated", ".vs":
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".csproj" && ext != ".props" && ext != ".targets" {
			return nil
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("cannot check package identities in symlink %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, edit := range edits {
			if edit.path == path {
				data = edit.after
				break
			}
		}
		d := xml.NewDecoder(bytes.NewReader(data))
		for {
			tok, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("%s: invalid XML: %w", path, err)
			}
			el, ok := tok.(xml.StartElement)
			if !ok || (!strings.EqualFold(el.Name.Local, "PackageReference") && !strings.EqualFold(el.Name.Local, "PackageVersion")) {
				continue
			}
			for _, attr := range el.Attr {
				if strings.EqualFold(attr.Name.Local, "Include") || strings.EqualFold(attr.Name.Local, "Update") || strings.EqualFold(attr.Name.Local, "Remove") {
					ids[attr.Value] = true
				}
			}
		}
		return nil
	})
	var result []string
	for id := range ids {
		result = append(result, id)
	}
	slices.Sort(result)
	return result, err
}

func stackReleaseSelect(ctx context.Context, root string, m *stackManifest, links []stackLink, member, sibling, pkg string) (stackReleaseSelection, []stackLink, error) {
	selection := stackReleaseSelection{Reference: pkg, Original: pkg}
	pub := m.publishing()[sibling]
	if pub.Prefix == "" && len(pub.As) == 0 {
		return selection, links, nil
	}
	// Resolve only ids this sibling really produces, just as wire does. This
	// built-in discovery reads project text and never evaluates MSBuild.
	discovered, err := dotnet.New().Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: sibling, IncludeUnversioned: true, IncludeRegistrySiblings: true})
	if err != nil {
		return selection, nil, err
	}
	ids, err := stackReleaseReferenceIDs(filepath.Join(root, member), nil)
	if err != nil {
		return selection, nil, err
	}
	var left []string
	for _, id := range ids {
		original := ""
		for _, produced := range discovered.Packages {
			dir := filepath.ToSlash(produced.Dir)
			if dir != sibling && !strings.HasPrefix(dir, sibling+"/") {
				continue
			}
			as, ok := stackRepublishedID(pub, produced.Name)
			if ok && strings.EqualFold(as, id) {
				if original != "" && original != produced.Name {
					return selection, nil, fmt.Errorf("ambiguous republished id %s", id)
				}
				original = produced.Name
			}
		}
		if original == "" || strings.EqualFold(original, id) {
			continue
		}
		if strings.EqualFold(original, pkg) {
			selection.Reference = id
		} else {
			left = append(left, id)
		}
	}
	if len(left) > 0 {
		return selection, nil, fmt.Errorf("all-or-nothing migration of %s refuses to leave republished ids behind: %s", sibling, strings.Join(left, ", "))
	}
	// Delivery is proven against public/original ids, while the edit and pin
	// proof retain the exact id written by the consumer.
	public := slices.Clone(links)
	for i := range public {
		if public[i].To == sibling && public[i].Via != "" {
			public[i].Package = public[i].Via
		}
	}
	return selection, public, nil
}

func stackReleaseSelectedEdits(root string, selection stackReleaseSelection, pin, version string, warnings ...string) ([]stackReleaseEdit, []string, error) {
	if strings.EqualFold(selection.Reference, selection.Original) {
		return stackReleaseMigrationEdits(root, selection.Original, pin, version, selection.symbol(), warnings...)
	}
	if !strings.EqualFold(pin, selection.Original) {
		return nil, nil, fmt.Errorf("republished reference requires direct public package delivery")
	}
	edits, notes, err := stackReleaseRenameEdits(root, selection.Reference, selection.Original, version, selection.symbol(), warnings...)
	if err != nil {
		return nil, notes, err
	}
	ids, err := stackReleaseReferenceIDs(root, edits)
	if err != nil {
		return nil, notes, err
	}
	if slices.ContainsFunc(ids, func(id string) bool { return strings.EqualFold(id, selection.Reference) }) {
		return nil, notes, fmt.Errorf("all-or-nothing migration refuses to leave republished ids behind: %s", selection.Reference)
	}
	return edits, notes, nil
}
