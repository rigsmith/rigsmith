package dotnet

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// What `dotnet nuget push --skip-duplicate --force-english-output` printed
// against a local feed answering every push with 409, and then with 201.
// Both exited 0.
const (
	pushOutputDuplicate = `Pushing acme.lib.1.2.0.nupkg to 'https://feed.example/api/v2/package'...
  PUT https://feed.example/api/v2/package/
  Conflict https://feed.example/api/v2/package/ 23ms
Package '/pack/packages/Acme.Lib.1.2.0.nupkg' already exists at feed 'https://feed.example/api/v2/package'.
`
	pushOutputPushed = `Pushing acme.lib.1.2.0.nupkg to 'https://feed.example/api/v2/package'...
  PUT https://feed.example/api/v2/package/
  Created https://feed.example/api/v2/package/ 4ms
Your package was pushed.
`
)

func publishPacked(t *testing.T, stdout string) (plugin.PublishResponse, []string) {
	t.Helper()
	t.Setenv("NUGET_API_KEY", "")
	wasPush := pushRunner
	t.Cleanup(func() { pushRunner = wasPush })
	var args []string
	pushRunner = func(_ context.Context, _ string, _ string, a ...string) (string, string, error) {
		args = a
		return stdout, "", nil
	}
	resp, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot:      "/repo",
		Package:       plugin.Package{Name: "Acme.Lib", Version: "1.2.0", ManifestPath: "src/Acme.Lib/Acme.Lib.csproj"},
		ArtifactPath:  "/pack/packages/Acme.Lib.1.2.0.nupkg",
		PackageSource: "https://feed.example/api/v2/package",
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp, args
}

// A version already on the feed exits 0 like a real push, and used to be
// reported as "published … pushed". It is skipped, and says so.
func TestPublishReportsADuplicateAsAlreadyOnTheFeed(t *testing.T) {
	resp, _ := publishPacked(t, pushOutputDuplicate)
	if resp.Published || !resp.Skipped {
		t.Fatalf("duplicate push: Published=%v Skipped=%v, want skipped", resp.Published, resp.Skipped)
	}
	if !strings.Contains(resp.Message, "already on https://feed.example/api/v2/package") {
		t.Errorf("message = %q, want it to say the version is already on the feed", resp.Message)
	}
}

func TestPublishReportsARealPushAsPublished(t *testing.T) {
	resp, _ := publishPacked(t, pushOutputPushed)
	if !resp.Published || resp.Skipped {
		t.Fatalf("real push: Published=%v Skipped=%v, want published", resp.Published, resp.Skipped)
	}
}

// The duplicate is recognised by NuGet's English wording, so the push asks for
// English whatever the machine's language.
func TestPublishAsksNuGetForEnglishOutput(t *testing.T) {
	_, args := publishPacked(t, pushOutputPushed)
	if !slices.Contains(args, "--force-english-output") || !slices.Contains(args, "--skip-duplicate") {
		t.Errorf("push args = %v, want --skip-duplicate and --force-english-output", args)
	}
}

// A local folder feed can log "already exists at feed" and then "Your package
// was pushed." for a package it did not add. "Already exists" decides it.
func TestPublishReportsAFolderFeedDuplicateAsSkipped(t *testing.T) {
	resp, _ := publishPacked(t, "Pushing acme.lib.1.2.0.nupkg to '/feeds/local'...\n"+
		"warn : Package '/pack/packages/Acme.Lib.1.2.0.nupkg' already exists at feed '/feeds/local'.\n"+
		"Your package was pushed.\n")
	if resp.Published || !resp.Skipped {
		t.Fatalf("folder-feed duplicate: Published=%v Skipped=%v, want skipped", resp.Published, resp.Skipped)
	}
}
