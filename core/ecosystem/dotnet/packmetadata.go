package dotnet

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// PackMetadata is evaluated in Release, just as Artifacts packs it. Dependencies
// include public PackageReferences and resolved ProjectReferences; build-only
// references (PrivateAssets=all) are omitted. No build or restore is run.
type PackMetadata struct {
	AssemblyName string
	OriginalHook string
	ToolsPath    string
	Packable     bool
	Dependencies []PackDependency
}

type PackDependency struct {
	ID          string
	Version     string
	ProjectPath string
	Framework   string
}

var metadataRunner = runCmd

// EvaluatePackMetadata keeps MSBuild's evaluation contract inside the adapter.
// Multiple target frameworks are evaluated separately so conditional references
// are retained, rather than treating the outer build's empty item list as proof.
func EvaluatePackMetadata(ctx context.Context, root, manifest string, properties map[string]string) (PackMetadata, error) {
	var result PackMetadata
	query := func(framework string) (map[string]string, map[string][]map[string]string, error) {
		args := []string{"msbuild", filepath.Join(root, manifest), "-nologo", "-p:Configuration=Release",
			"-getProperty:AssemblyName,CustomAfterMicrosoftCommonTargets,MSBuildToolsPath,TargetFrameworks,TargetFramework,IsPackable",
			"-getItem:PackageReference,PackageVersion,ProjectReference"}
		if framework != "" {
			args = append(args, "-p:TargetFramework="+escapePropertyValue(framework))
		}
		keys := make([]string, 0, len(properties))
		for key := range properties {
			if !propertyNameRe.MatchString(key) {
				return nil, nil, fmt.Errorf("invalid property name %q", key)
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			args = append(args, "-p:"+key+"="+escapePropertyValue(properties[key]))
		}
		stdout, _, err := metadataRunner(ctx, root, "dotnet", args...)
		if err != nil {
			return nil, nil, err
		}
		var response struct {
			Properties map[string]string
			Items      map[string][]map[string]string
		}
		if err := json.Unmarshal([]byte(stdout), &response); err != nil {
			return nil, nil, fmt.Errorf("MSBuild pack metadata: %w", err)
		}
		return response.Properties, response.Items, nil
	}
	props, items, err := query("")
	if err != nil {
		return result, err
	}
	result.AssemblyName, result.OriginalHook, result.ToolsPath = props["AssemblyName"], props["CustomAfterMicrosoftCommonTargets"], props["MSBuildToolsPath"]
	result.Packable = strings.EqualFold(props["IsPackable"], "true")
	appendItems := func(items map[string][]map[string]string, framework string) {
		central := map[string]string{}
		for _, item := range items["PackageVersion"] {
			central[strings.ToLower(item["Identity"])] = item["Version"]
		}
		for _, item := range items["PackageReference"] {
			if strings.EqualFold(item["PrivateAssets"], "all") {
				continue
			}
			version := item["VersionOverride"]
			if version == "" {
				version = item["Version"]
			}
			if version == "" {
				version = central[strings.ToLower(item["Identity"])]
			}
			result.Dependencies = append(result.Dependencies, PackDependency{ID: item["Identity"], Version: version, Framework: framework})
		}
		for _, item := range items["ProjectReference"] {
			if strings.EqualFold(item["PrivateAssets"], "all") || strings.EqualFold(item["ReferenceOutputAssembly"], "false") {
				continue
			}
			path := item["FullPath"]
			if path == "" {
				path = filepath.Join(root, filepath.Dir(manifest), item["Identity"])
			}
			result.Dependencies = append(result.Dependencies, PackDependency{ProjectPath: filepath.Clean(path), Framework: framework})
		}
	}
	if frameworks := props["TargetFrameworks"]; frameworks != "" {
		for _, framework := range strings.Split(frameworks, ";") {
			inner, innerItems, err := query(framework)
			if err != nil {
				return result, err
			}
			if inner["AssemblyName"] != result.AssemblyName {
				return result, fmt.Errorf("%s has framework-dependent AssemblyName; edge cannot verify it", manifest)
			}
			appendItems(innerItems, framework)
		}
	} else {
		appendItems(items, props["TargetFramework"])
	}
	if result.AssemblyName == "" {
		return result, fmt.Errorf("%s has no evaluated AssemblyName", manifest)
	}
	return result, nil
}
