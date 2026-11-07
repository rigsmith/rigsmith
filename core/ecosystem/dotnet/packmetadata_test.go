package dotnet

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluatePackMetadata(t *testing.T) {
	original := metadataRunner
	t.Cleanup(func() { metadataRunner = original })
	var calls [][]string
	metadataRunner = func(_ context.Context, _, _ string, args ...string) (string, string, error) {
		calls = append(calls, args)
		framework := ""
		for _, arg := range args {
			if strings.HasPrefix(arg, "-p:TargetFramework=") {
				framework = strings.TrimPrefix(arg, "-p:TargetFramework=")
			}
		}
		items := map[string]any{}
		if framework != "" {
			items["PackageReference"] = []map[string]string{
				{"Identity": "ThirdParty", "Version": "[3.2.1]"},
				{"Identity": "Central"},
				{"Identity": "BuildOnly", "Version": "1.0.0", "PrivateAssets": "All"},
			}
			items["PackageVersion"] = []map[string]string{{"Identity": "Central", "Version": "4.0.0"}}
			items["ProjectReference"] = []map[string]string{{"Identity": "../Lib/Lib.csproj", "FullPath": filepath.Join("root", "Lib", "Lib.csproj")}}
		}
		data, _ := json.Marshal(map[string]any{"Properties": map[string]string{"AssemblyName": "Controls", "CustomAfterMicrosoftCommonTargets": "original.targets", "MSBuildToolsPath": "tools", "TargetFrameworks": "net9.0;net10.0", "IsPackable": "true"}, "Items": items})
		return string(data), "", nil
	}
	meta, err := EvaluatePackMetadata(context.Background(), "root", "Controls/Controls.csproj", map[string]string{"RepositoryUrl": "https://example.invalid/a;b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || !meta.Packable || meta.AssemblyName != "Controls" || meta.OriginalHook != "original.targets" || len(meta.Dependencies) != 6 {
		t.Fatalf("metadata=%+v calls=%v", meta, calls)
	}
	if meta.Dependencies[0].Version != "[3.2.1]" || meta.Dependencies[1].Version != "4.0.0" {
		t.Fatalf("versions = %+v", meta.Dependencies)
	}
	if meta.Dependencies[0].Framework != "net9.0" || meta.Dependencies[3].Framework != "net10.0" {
		t.Fatalf("frameworks = %+v", meta.Dependencies)
	}
	if !strings.Contains(strings.Join(calls[0], " "), "a%3Bb") {
		t.Fatalf("unescaped property: %v", calls[0])
	}
}
