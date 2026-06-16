package detect

import (
	"os"
	"path/filepath"
	"testing"
)

// write creates an empty file at the given path, creating parent dirs as needed.
func write(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNearestEcosystem_SingleEcosystem(t *testing.T) {
	cases := []struct {
		manifest string
		want     string
	}{
		{"go.mod", Go},
		{"go.work", Go},
		{"package.json", Node},
		{"Cargo.toml", Cargo},
		{"app.csproj", DotNet},
		{"Directory.Build.props", DotNet},
		{"Directory.Packages.props", DotNet},
		{"global.json", DotNet},
		{"NuGet.Config", DotNet}, // case-insensitive marker match
	}
	for _, tc := range cases {
		t.Run(tc.manifest, func(t *testing.T) {
			dir := t.TempDir()
			// Mark the repo root so Root() doesn't walk past dir.
			write(t, filepath.Join(dir, ".git"))
			write(t, filepath.Join(dir, tc.manifest))

			id, candidates := NearestEcosystem(dir)
			if candidates != nil {
				t.Fatalf("unexpected ambiguity: %v", candidates)
			}
			if id != tc.want {
				t.Fatalf("got id %q, want %q", id, tc.want)
			}
		})
	}
}

// A .NET repo whose solutions/projects all live in subdirectories (Source/,
// Build/, …) with only Directory.Build.props at the root still resolves to
// .NET — the root marker is the signal, since nothing matches at the root and
// NearestEcosystem doesn't recurse. Without the marker it'd resolve to none.
func TestNearestEcosystem_DotNetRootMarkerWithNestedProjects(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git"))
	write(t, filepath.Join(root, "Directory.Build.props"))
	write(t, filepath.Join(root, "Source", "Lib", "Lib.csproj"))

	id, candidates := NearestEcosystem(root)
	if candidates != nil {
		t.Fatalf("unexpected ambiguity: %v", candidates)
	}
	if id != DotNet {
		t.Fatalf("got id %q, want %q (root marker should signal .NET)", id, DotNet)
	}

	// Sanity: a bare nested csproj with no root marker resolves to none from the
	// root (the regression this guards against).
	bare := t.TempDir()
	write(t, filepath.Join(bare, ".git"))
	write(t, filepath.Join(bare, "Source", "Lib", "Lib.csproj"))
	if id, _ := NearestEcosystem(bare); id != "" {
		t.Fatalf("nested-only csproj at the root = %q, want none", id)
	}
}

func TestNearestEcosystem_Ambiguous(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".git"))
	write(t, filepath.Join(dir, "package.json"))
	write(t, filepath.Join(dir, "lib.csproj"))

	id, candidates := NearestEcosystem(dir)
	if id != "" {
		t.Fatalf("got id %q, want empty on ambiguity", id)
	}
	// Candidates are sorted: dotnet, node.
	want := []string{DotNet, Node}
	if len(candidates) != len(want) || candidates[0] != want[0] || candidates[1] != want[1] {
		t.Fatalf("got candidates %v, want %v", candidates, want)
	}
}

func TestNearestEcosystem_NestedResolvesToNearest(t *testing.T) {
	root := t.TempDir()
	// Ancestor (repo root) is a Go module; the nested package is Node. Walking up
	// from the nested dir must stop at the nearest manifest, not the ancestor.
	write(t, filepath.Join(root, ".git"))
	write(t, filepath.Join(root, "go.mod"))

	nested := filepath.Join(root, "web", "app")
	write(t, filepath.Join(nested, "package.json"))

	id, candidates := NearestEcosystem(nested)
	if candidates != nil {
		t.Fatalf("unexpected ambiguity: %v", candidates)
	}
	if id != Node {
		t.Fatalf("got id %q, want %q (nearest manifest, not the Go ancestor)", id, Node)
	}
}

func TestNearestEcosystem_NoneFound(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".git"))

	id, candidates := NearestEcosystem(dir)
	if id != "" || candidates != nil {
		t.Fatalf("got (%q, %v), want empty", id, candidates)
	}
}

func TestNearestEcosystem_SkipsEmptyDirsWhileWalkingUp(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git"))
	write(t, filepath.Join(root, "go.mod"))

	// Intermediate dirs carry no manifest; resolution should walk up to root.
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	id, candidates := NearestEcosystem(deep)
	if candidates != nil {
		t.Fatalf("unexpected ambiguity: %v", candidates)
	}
	if id != Go {
		t.Fatalf("got id %q, want %q", id, Go)
	}
}
