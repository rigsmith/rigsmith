package ecosystem

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestArtifactsPropertiesBuiltInContracts(t *testing.T) {
	for _, adapter := range Default().All() {
		t.Run(adapter.Info().ID, func(t *testing.T) {
			info := adapter.Info()
			capable := slices.Contains(info.Capabilities, plugin.CapabilityArtifactsProperties)
			if capable != (info.ID == "dotnet") {
				t.Fatalf("unexpected property capability: %+v", info)
			}
			for _, private := range []bool{false, true} {
				for _, dryRun := range []bool{false, true} {
					if capable && !private && !dryRun {
						continue
					} // Real build is covered by the .NET spike.
					_, err := adapter.Artifacts(context.Background(), plugin.ArtifactsRequest{
						Package: plugin.Package{Name: "Lib", Private: private}, DryRun: dryRun,
						Properties: map[string]string{"PackageId": "You.Lib"},
					})
					if capable {
						if err != nil {
							t.Fatal(err)
						}
					} else if err == nil || !strings.Contains(err.Error(), plugin.CapabilityArtifactsProperties) {
						t.Fatalf("private=%v dryRun=%v: expected capability refusal, got %v", private, dryRun, err)
					}
				}
			}
		})
	}
}
