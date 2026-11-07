package cli

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/ecosystem/dotnet"
)

func stackEdgeHookXML(renames map[string]string, originals map[string]dotnet.PackMetadata) []byte {
	escape := func(s string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	var body bytes.Buffer
	body.WriteString("<Project>\n")
	// Resolve the original independently for each project: referenced projects
	// need their own hook too, even though the edge hook is a global property.
	paths := make([]string, 0, len(originals))
	for path := range originals {
		if originals[path].OriginalHook != "" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		meta := originals[path]
		hook := meta.OriginalHook
		if !filepath.IsAbs(hook) {
			hook = filepath.Join(meta.ToolsPath, hook)
		}
		fmt.Fprintf(&body, "  <PropertyGroup Condition=\"%s\"><_RigEdgeOriginalHook>%s</_RigEdgeOriginalHook></PropertyGroup>\n",
			escape("'$(MSBuildProjectFullPath)' == '"+stackEdgeMSBuildLiteral(path)+"'"), escape(stackEdgeMSBuildLiteral(hook)))
	}
	if len(paths) > 0 {
		body.WriteString("  <Import Project=\"$(_RigEdgeOriginalHook)\" Condition=\"'$(_RigEdgeOriginalHook)' != '' and Exists('$(_RigEdgeOriginalHook)')\" />\n")
	}
	ids := make([]string, 0, len(renames))
	for id := range renames {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		condition := fmt.Sprintf("'$(PackageId)' == '%s' or ('$(PackageId)' == '' and '$(AssemblyName)' == '%s')", id, id)
		fmt.Fprintf(&body, "  <PropertyGroup Condition=\"%s\">\n    <PackageId>%s</PackageId>\n  </PropertyGroup>\n", escape(condition), escape(renames[id]))
	}
	body.WriteString("</Project>\n")
	return body.Bytes()
}

// XML escaping alone does not quote MSBuild expressions or condition literals.
// Keep paths containing $, %, apostrophes or semicolons literal in the hook.
func stackEdgeMSBuildLiteral(value string) string {
	return strings.NewReplacer("%", "%25", "$", "%24", "@", "%40", "'", "%27", ";", "%3B", "(", "%28", ")", "%29", "*", "%2A", "?", "%3F").Replace(value)
}

func stackEdgeWriteHook(renames map[string]string, originals map[string]dotnet.PackMetadata) (string, func(), error) {
	canonical := map[string]dotnet.PackMetadata{}
	for path, metadata := range originals {
		if metadata.OriginalHook == "" {
			continue
		}
		project, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", nil, fmt.Errorf("resolving original hook project %s: %w", path, err)
		}
		canonical[project] = metadata
	}
	dir, err := os.MkdirTemp("", "rig-stack-edge-hook-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "Edge.targets")
	if err := os.WriteFile(path, stackEdgeHookXML(renames, canonical), 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}
