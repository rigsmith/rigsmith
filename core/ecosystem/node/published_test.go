package node

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// stubNpmView makes `npm view` answer with the given output, stderr and error,
// recording the arguments it got.
func stubNpmView(t *testing.T, out, stderr string, err error) *[]string {
	t.Helper()
	var got []string
	was := npmView
	npmView = func(_ context.Context, _ string, args ...string) (string, string, error) {
		got = args
		return out, stderr, err
	}
	t.Cleanup(func() { npmView = was })
	return &got
}

func publishedReq(source string) plugin.PublishedRequest {
	return plugin.PublishedRequest{
		RepoRoot:      "/repo",
		Package:       plugin.Package{Name: "@acme/lib", Version: "1.2.0", Dir: "packages/lib"},
		PackageSource: source,
	}
}

func TestPublishedAnswersFromNpmView(t *testing.T) {
	for _, tc := range []struct {
		name    string
		out     string
		stderr  string
		err     error
		want    bool
		wantErr bool
	}{
		{"the version is there", "1.2.0\n", "", nil, true, false},
		{"the package is there without it", "", "", nil, false, false},
		{"an answer that isn't the version", "npm notice something\n", "", nil, false, true},
		{"no such package", "", "npm error code E404\nnpm error 404 Not Found", errors.New("exit status 1"), false, false},
		{"the registry failed", "", "npm error code E500", errors.New("exit status 1"), false, true},
		{"npm isn't reachable", "", "npm error network request failed", errors.New("exit status 1"), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubNpmView(t, tc.out, tc.stderr, tc.err)
			resp, err := (&Adapter{}).Published(context.Background(), publishedReq(""))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if resp.Published != tc.want || resp.NoRegistry {
				t.Errorf("resp = %+v, want Published %v", resp, tc.want)
			}
		})
	}
}

func TestPublishedPassesARegistryURL(t *testing.T) {
	got := stubNpmView(t, "1.2.0", "", nil)
	if _, err := (&Adapter{}).Published(context.Background(), publishedReq("https://npm.example.com/")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*got, []string{"@acme/lib@1.2.0", "version", "--registry", "https://npm.example.com/"}) {
		t.Errorf("npm view args = %v", *got)
	}
}

func TestPublishedPrivatePackageHasNoRegistry(t *testing.T) {
	stubNpmView(t, "", "", errors.New("npm must not be asked"))
	req := publishedReq("")
	req.Package.Private = true
	resp, err := (&Adapter{}).Published(context.Background(), req)
	if err != nil || !resp.NoRegistry {
		t.Errorf("resp = %+v, err = %v, want NoRegistry", resp, err)
	}
}

// A packed tarball goes out as it is, under the plan's dist-tag.
func TestPublishSendsAPackedTarballWithItsDistTag(t *testing.T) {
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	var got []string
	was := npmPublish
	npmPublish = func(_ context.Context, _ string, _ []string, args ...string) (string, string, error) {
		got = args
		return "", "", nil
	}
	t.Cleanup(func() { npmPublish = was })

	_, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot:     "/repo",
		Package:      plugin.Package{Name: "@acme/lib", Version: "1.2.0-next.0", Dir: "packages/lib"},
		Access:       "public",
		ArtifactPath: "/pack/packages/acme-lib-1.2.0-next.0.tgz",
		Tag:          "next",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"publish", "/pack/packages/acme-lib-1.2.0-next.0.tgz", "--access", "public", "--tag", "next"}
	if !slices.Equal(got, want) {
		t.Errorf("npm args = %v, want %v", got, want)
	}
}
