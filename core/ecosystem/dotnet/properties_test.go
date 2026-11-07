package dotnet

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestArtifactsPropertiesArguments(t *testing.T) {
	for _, hook := range []string{
		"/tmp/edge build/you & me/rename.targets",
		`C:\Users\You & Me\edge build\rename.targets`,
	} {
		t.Run(hook, func(t *testing.T) {
			restore := packRunner
			t.Cleanup(func() { packRunner = restore })
			var got []string
			packRunner = func(_ context.Context, _, name string, args ...string) (string, string, error) {
				if name != "dotnet" {
					t.Fatalf("command = %q", name)
				}
				got = args
				return "", "", nil
			}
			_, err := New().Artifacts(context.Background(), plugin.ArtifactsRequest{
				RepoRoot: t.TempDir(), OutputDir: t.TempDir(),
				Package: plugin.Package{Name: "Lib", ManifestPath: "Lib.csproj", Version: "1.2.3-edge.7"},
				Properties: map[string]string{
					"CustomAfterMicrosoftCommonTargets": hook,
					"Description":                       `a;b,c="quoted" %3B $(Name) @(Items) 'end' *?`,
					"Empty":                             "",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{
				"-p:Version=1.2.3-edge.7",
				"-p:CustomAfterMicrosoftCommonTargets=" + hook,
				"-p:Description=a%3Bb%2Cc=%22quoted%22 %253B %24%28Name%29 %40%28Items%29 %27end%27 %2A%3F",
				"-p:Empty=",
			}
			if len(got) < len(want) || !reflect.DeepEqual(got[len(got)-len(want):], want) {
				t.Fatalf("property argv = %#v, want suffix %#v", got, want)
			}
		})
	}
}

func TestArtifactsPropertiesRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", "Bad=Name", "Bad;Injected", "Bad,Injected", "Bad Name", "-Bad", "Version", "vErSiOn"} {
		t.Run(name, func(t *testing.T) {
			_, err := New().Artifacts(context.Background(), plugin.ArtifactsRequest{
				DryRun: true, Package: plugin.Package{Version: "1.2.3"}, Properties: map[string]string{name: "value"},
			})
			if err == nil || (!strings.Contains(err.Error(), "invalid property name") && !strings.Contains(err.Error(), "must not override")) {
				t.Fatalf("invalid property accepted: %q, %v", name, err)
			}
		})
	}
}
