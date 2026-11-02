package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const stackReleaseCentralFixture = `<Project><PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup><ItemGroup><PackageVersion Include="Acme.Core" Version="1.0.0" /></ItemGroup></Project>`

func TestStackReleasedUnsafePinPlans(t *testing.T) {
	for _, tc := range []struct{ name, path, body, want string }{
		{"override", "app/App.csproj", `<Project><ItemGroup><PackageReference Include="Acme.Core" VersionOverride="1.0.0" /></ItemGroup></Project>`, "VersionOverride"},
		{"update", "app/App.csproj", `<Project><ItemGroup><PackageReference Update="Acme.Core" Version="1.0.0" /></ItemGroup></Project>`, "Update"},
		{"property", "app/App.csproj", `<Project><ItemGroup><PackageReference Include="Acme.Core" Version="$(CoreVersion)" /></ItemGroup></Project>`, "unresolved"},
		{"nested-override", "app/App.csproj", `<Project><ItemGroup><PackageReference Include="Acme.Core"><VersionOverride>1.0.0</VersionOverride></PackageReference></ItemGroup></Project>`, "nested"},
		{"props-add", "app/Directory.Build.props", `<Project><ItemGroup><PackageReference Include="Acme.Core" Version="1.0.0" /></ItemGroup></Project>`, "inherited"},
		{"targets-update", "app/Directory.Build.targets", `<Project><ItemGroup><PackageReference Update="Acme.Core" Version="1.0.0" /></ItemGroup></Project>`, "Update"},
		{"ancestor-props", "Directory.Build.props", `<Project><ItemGroup><PackageReference Include="Acme.Core" Version="1.0.0" /></ItemGroup></Project>`, "inherited"},
		{"ancestor-targets", "Directory.Build.targets", `<Project><ItemGroup><PackageReference Update="Acme.Core" VersionOverride="1.0.0" /></ItemGroup></Project>`, "VersionOverride"},
		{"dynamic-identity", "app/App.csproj", `<Project><ItemGroup><PackageReference Include="$(CorePackage)" Version="1.0.0" /></ItemGroup></Project>`, "identity"},
		{"wildcard-update", "app/Directory.Build.targets", `<Project><ItemGroup><PackageReference Update="*" Version="1.0.0" /></ItemGroup></Project>`, "identity"},
		{"import", "app/App.csproj", `<Project><Import Project="../External.props" /><ItemGroup><PackageReference Include="Acme.Core" /></ItemGroup></Project>`, "imports"},
		{"nested-central-shadow", "app/sub/Directory.Packages.props", `<Project />`, "effective central pin"},
		{"unproven-project", "app/sub/Sub.csproj", `<Project />`, "cannot prove"},
		{"project-metadata", "app/sub/Sub.csproj", `<Project><ItemGroup><ProjectReference Include="../App.csproj" ReferenceOutputAssembly="false" /></ItemGroup></Project>`, "project reference"},
		{"nested-project-metadata", "app/sub/Sub.csproj", `<Project><ItemGroup><ProjectReference Include="../App.csproj"><ReferenceOutputAssembly>false</ReferenceOutputAssembly></ProjectReference></ItemGroup></Project>`, "project reference"},
		{"defaults", "app/Directory.Build.targets", `<Project><ItemDefinitionGroup><PackageReference><VersionOverride>1.0.0</VersionOverride></PackageReference></ItemDefinitionGroup></Project>`, "default"},
		{"duplicate-version", "app/App.csproj", `<Project><ItemGroup><PackageReference Include="Acme.Core" Version="1.0.0" Version="0.1.0" /></ItemGroup></Project>`, "duplicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{
				"app/Directory.Packages.props": stackReleaseCentralFixture,
				"app/App.csproj":               `<Project><ItemGroup><PackageReference Include="Acme.Core" /></ItemGroup></Project>`,
				"app/sub/Sub.csproj":           `<Project><ItemGroup><PackageReference Include="Acme.Core" /></ItemGroup></Project>`,
				"app/Code.cs":                  "#if STACK_ACME_CORE\nApi.New();\n#else\nApi.Old();\n#endif\n",
			}
			files[tc.path] = tc.body
			for path, data := range files {
				path = filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			edits, _, err := stackReleaseEdits(filepath.Join(root, "app"), "Acme.Core", "2.0.0", "STACK_ACME_CORE")
			if err == nil || !strings.Contains(err.Error(), tc.want) || len(edits) != 0 {
				t.Fatalf("unsafe plan: %v %v", edits, err)
			}
			for path, before := range files {
				after, err := os.ReadFile(filepath.Join(root, path))
				if err != nil || !bytes.Equal(after, []byte(before)) {
					t.Fatalf("modified %s: %v", path, err)
				}
			}
		})
	}
}

func TestStackReleasedProvenPins(t *testing.T) {
	for _, central := range []bool{false, true} {
		t.Run(map[bool]string{false: "literal", true: "central"}[central], func(t *testing.T) {
			root := t.TempDir()
			member := filepath.Join(root, "app")
			if err := os.MkdirAll(filepath.Join(member, "tests"), 0755); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{
				"App.csproj":         `<Project><ItemGroup><PackageReference Include="Acme.Core" Version="1.0.0" /></ItemGroup></Project>`,
				"tests/Tests.csproj": `<Project><ItemGroup><ProjectReference Include="../App.csproj" /></ItemGroup></Project>`,
				"Code.cs":            "#if STACK_ACME_CORE\nApi.New();\n#else\nApi.Old();\n#endif\n",
			}
			if central {
				files["App.csproj"] = `<Project><ItemGroup><PackageReference Include="Acme.Core" /></ItemGroup></Project>`
				files["Directory.Packages.props"] = stackReleaseCentralFixture
			}
			for path, data := range files {
				if err := os.WriteFile(filepath.Join(member, path), []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			// The ordinary stack overlay is inactive in the package build being proved.
			if err := os.WriteFile(filepath.Join(root, "Directory.Build.targets"), []byte(`<Project><ItemGroup Condition="'$(UseStackSources)' != 'false'"><PackageReference Remove="@(StackSource)" /></ItemGroup></Project>`), 0644); err != nil {
				t.Fatal(err)
			}
			edits, _, err := stackReleaseEdits(member, "Acme.Core", "2.0.0", "STACK_ACME_CORE")
			if err != nil {
				t.Fatal(err)
			}
			if err = stackReleaseWrite(edits); err != nil {
				t.Fatal(err)
			}
			code, err := os.ReadFile(filepath.Join(member, "Code.cs"))
			if err != nil || string(code) != "Api.New();\n" {
				t.Fatalf("%s %v", code, err)
			}
			pin := "App.csproj"
			if central {
				pin = "Directory.Packages.props"
			}
			data, err := os.ReadFile(filepath.Join(member, pin))
			if err != nil || !bytes.Contains(data, []byte(`Version="2.0.0"`)) {
				t.Fatalf("pin not bumped: %s %v", data, err)
			}
		})
	}
}

func TestStackReleasedXMLOverrideRefused(t *testing.T) {
	for _, ref := range []string{`Include="Acme.Core" VersionOverride="1.0.0"`, `Update="Acme.Core" Version="1.0.0"`} {
		in := []byte(`<Project><ItemGroup><PackageReference ` + ref + ` /></ItemGroup></Project>`)
		after, changed, notes := stackReleaseXML(in, "Acme.Core", "2.0.0")
		if changed || !bytes.Equal(in, after) || !strings.Contains(strings.Join(notes, ";"), "unsafe package version") {
			t.Fatalf("%s %v", after, notes)
		}
	}
}

func TestStackReleasedTransitiveOverrideRefused(t *testing.T) {
	for _, ref := range []string{`<PackageReference Include="Acme.Core" VersionOverride="1.0.0" />`, `<PackageReference Include="Acme.Core" Version="1.0.0" />`, `<PackageVersion Include="Acme.Core" Version="1.0.0" />`} {
		root := t.TempDir()
		files := map[string]string{"App.csproj": `<Project><ItemGroup><PackageReference Include="Acme.Control" Version="1.0.0" />` + ref + `</ItemGroup></Project>`, "Code.cs": "#if STACK_ACME_CORE\nApi.New();\n#endif\n"}
		for path, body := range files {
			if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
		}
		edits, _, err := stackReleaseMigrationEdits(root, "Acme.Core", "Acme.Control", "2.0.0", "STACK_ACME_CORE")
		if err == nil || len(edits) != 0 {
			t.Fatalf("sibling override accepted: %v", err)
		}
	}
}
