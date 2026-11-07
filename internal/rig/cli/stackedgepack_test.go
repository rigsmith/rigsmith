package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/spf13/cobra"
)

func TestStackEdgeResolveIDs(t *testing.T) {
	pkgs := []stackPackPackage{{pkg: plugin.Package{Name: "Lib"}}, {pkg: plugin.Package{Name: "Lib.Controls"}}, {pkg: plugin.Package{Name: "Lib.Samples"}}}
	pub := stackPublishing{Prefix: "You.", As: map[string]string{"Lib.Controls": "Custom.Controls"}}
	got, err := stackEdgeResolveIDs(pkgs, pub, []string{"Lib.Samples"})
	want := map[string]string{"Lib": "You.Lib", "Lib.Controls": "Custom.Controls"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, %v; want %v", got, err, want)
	}
	for name, pub := range map[string]stackPublishing{
		"missing": {}, "partial": {As: map[string]string{"Lib": "You.Lib"}},
		"original":         {As: map[string]string{"Lib": "lib"}},
		"another-original": {Prefix: "You.", As: map[string]string{"Lib": "Lib.Samples"}},
		"collision":        {Prefix: "You.", As: map[string]string{"Lib": "You.Lib.Controls"}},
		"invalid":          {Prefix: "invalid'"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := stackEdgeResolveIDs(pkgs, pub, nil); err == nil {
				t.Fatal("accepted invalid ids")
			}
		})
	}
}

func TestStackEdgeCommandDiscovery(t *testing.T) {
	stack := newStackCmd()
	edge, _, err := stack.Find([]string{"edge"})
	if err != nil || edge.Hidden || edge.RunE == nil || edge.Long == "" {
		t.Fatalf("edge group: %+v, %v", edge, err)
	}
	var output bytes.Buffer
	stack.SetOut(&output)
	if err := stack.Help(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "rig stack edge") {
		t.Fatalf("edge missing from help: %s", output.String())
	}
	for _, verb := range []string{"pack", "status"} {
		cmd, _, err := edge.Find([]string{verb})
		if err != nil || cmd == edge || cmd.Short == "" || cmd.Long == "" || cmd.ValidArgsFunction == nil {
			t.Fatalf("%s missing help/completion: %v", verb, err)
		}
	}
	cmd := newStackEdgePackCmd()
	if cmd.ValidArgsFunction == nil || cmd.Flags().ShorthandLookup("o") == nil || cmd.Flags().Lookup("number").DefValue != "0" {
		t.Fatal("missing flag/completion contract")
	}
}

func TestStackEdgePackFlags(t *testing.T) {
	data, err := json.Marshal(edgeTestManifest())
	if err != nil {
		t.Fatal(err)
	}
	root := inTempStackspace(t, string(data))
	mustGitStack(t, root, "add", ".")
	mustGitStack(t, root, "-c", "user.name=test", "-c", "user.email=t@t", "commit", "-qm", "manifest")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"here-seed", []string{"lib", "--here", "--seed", "acme/stack@main"}, "--here cannot be combined"},
		{"here-manifest", []string{"lib", "--here", "--manifest", "rig.stack.jsonc"}, "--here cannot be combined"},
		{"here-tip", []string{"lib", "--here", "--upstream-tip"}, "--here cannot be combined"},
		{"seed-manifest", []string{"lib", "--seed", "acme/stack@main", "--manifest", "rig.stack.jsonc"}, "mutually exclusive"},
		{"missing-member", nil, "accepts 1 arg(s), received 0"},
		{"unknown-member", []string{"unknown"}, "unknown edge member"},
		{"unknown-here", []string{"unknown", "--here"}, "unknown stack member"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newStackEdgePackCmd()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestStackEdgeMemberCompletion(t *testing.T) {
	m := edgeTestManifest()
	m.Repos["app"] = &stackRepo{Upstream: "github.com/you/App", Fork: "github.com/you/App", Owned: true}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	root := inTempStackspace(t, string(data))
	if _, _, err := loadStackManifest(root); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []*cobra.Command{newStackEdgePackCmd(), newStackEdgeStatusCmd()} {
		names, directive := cmd.ValidArgsFunction(cmd, nil, "")
		if strings.Join(names, ",") != "lib" || directive != cobra.ShellCompDirectiveNoFileComp {
			t.Fatalf("%s completion: %v, %v", cmd.Name(), names, directive)
		}
		if names, _ := cmd.ValidArgsFunction(cmd, []string{"lib"}, ""); len(names) != 0 {
			t.Fatalf("%s offered second member: %v", cmd.Name(), names)
		}
	}
}

func edgeTestManifest() *stackManifest {
	return &stackManifest{
		Repos: map[string]*stackRepo{"lib": {Upstream: "github.com/acme/Lib", Fork: "github.com/you/Lib", TrackBranch: "stack/integration", PublishPrefix: "You."}},
		Edge:  map[string]*stackEdge{"lib": {Feed: "https://feed.invalid/index.json"}},
	}
}

func edgeTestFeed(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/index.json") {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"versions":["1.2.3","1.2.4-beta.1","1.0.0"]}`)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestStackEdgePackDryRun(t *testing.T) {
	root := t.TempDir()
	csproj(t, root, "lib/Lib", "Lib")
	m := edgeTestManifest()
	m.Edge["lib"].Exclude = []string{"Typo"}
	feed := edgeTestFeed(t)
	var out bytes.Buffer
	dir := filepath.Join(root, "out")
	result, err := stackEdgePackTree(context.Background(), &out, m, root, "lib", stackEdgePackOptions{
		OutputDir: dir, Commit: strings.Repeat("a", 40), Here: true, Number: 7, DryRun: true, PublicFeed: feed.URL, Client: feed.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "1.2.4-edge.7" || result.Publishable || !strings.Contains(out.String(), "would pack Lib as You.Lib") || !strings.Contains(out.String(), "excludes Typo") {
		t.Fatalf("result=%+v, output=%s", result, out.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote output: %v", err)
	}
}

func TestStackEdgePackRefusesCrossMemberSwap(t *testing.T) {
	root := t.TempDir()
	csproj(t, root, "lib/Lib", "Lib", "You.Sibling")
	csproj(t, root, "sibling/Sibling", "Sibling")
	m := edgeTestManifest()
	m.Repos["sibling"] = &stackRepo{Upstream: "github.com/acme/Sibling", PublishPrefix: "You."}
	_, err := stackEdgePackTree(context.Background(), io.Discard, m, root, "lib", stackEdgePackOptions{Commit: strings.Repeat("a", 40), Here: true, DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "overlay swap on You.Sibling") {
		t.Fatalf("cross-member: %v", err)
	}
}

func TestStackEdgePublicVersionMissingPackage(t *testing.T) {
	feed := httptest.NewServer(http.NotFoundHandler())
	defer feed.Close()
	if version, err := stackEdgePublicVersion(context.Background(), feed.Client(), feed.URL, "Lib"); err != nil || version != "" {
		t.Fatalf("unpublished package: version=%q, err=%v", version, err)
	}
}

func TestStackEdgePublicVersionCandidates(t *testing.T) {
	for _, tc := range []struct {
		name, body, want, wantError string
	}{
		{name: "empty", body: `{"versions":[]}`},
		{name: "only-prereleases", body: `{"versions":["1.0.0-beta.1","2.0.0-rc.2"]}`},
		{name: "stable", body: `{"versions":["1.0.0","2.0.0-rc.2","1.2.3"]}`, want: "1.2.3"},
		{name: "unsupported", body: `{"versions":["1.2.3","2.0.0.999999999999999999999"]}`, wantError: "unsupported feed version"},
		{name: "bad-json", body: `{`, wantError: "unexpected EOF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tc.body) }))
			defer feed.Close()
			version, err := stackEdgePublicVersion(context.Background(), feed.Client(), feed.URL, "Lib")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || errors.Unwrap(err) == nil {
					t.Fatalf("expected wrapped %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil || version != tc.want {
				t.Fatalf("version=%q, err=%v; want %q", version, err, tc.want)
			}
		})
	}
}

func TestStackEdgePublicVersionRefusesFailedFeed(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
			defer feed.Close()
			_, err := stackEdgePublicVersion(context.Background(), feed.Client(), feed.URL, "Lib")
			var responseError *stackReleaseHTTPError
			if !errors.As(err, &responseError) || responseError.StatusCode != status {
				t.Fatalf("expected wrapped HTTP %d, got %v", status, err)
			}
		})
	}
	t.Run("network", func(t *testing.T) {
		client := &http.Client{Transport: stackDepsRoundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("network unavailable") })}
		if _, err := stackEdgePublicVersion(context.Background(), client, "https://feed.invalid", "Lib"); err == nil {
			t.Fatal("network failure was treated as unpublished")
		}
	})
}

func TestStackEdgePackUsesTreeWithoutPublicStable(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{name: "unpublished", status: http.StatusNotFound},
		{name: "empty", status: http.StatusOK, body: `{"versions":[]}`},
		{name: "only-prereleases", status: http.StatusOK, body: `{"versions":["1.0.0-beta.1"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			csproj(t, root, "lib/Lib", "Lib")
			feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer feed.Close()
			opts := stackEdgePackOptions{OutputDir: filepath.Join(root, "out"), Commit: strings.Repeat("a", 40), Here: true, DryRun: true, PublicFeed: feed.URL, Client: feed.Client()}
			result, err := stackEdgePackTree(context.Background(), io.Discard, edgeTestManifest(), root, "lib", opts)
			if err != nil || result.Version != "1.0.0-edge.0" {
				t.Fatalf("tree fallback: version=%q, err=%v", result.Version, err)
			}
			// Discovery must still reject a missing R and T together.
			path := filepath.Join(root, "lib", "Lib", "Lib.csproj")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.Replace(string(data), "<Version>1.0.0</Version>", "", 1))
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			_, err = stackEdgePackTree(context.Background(), io.Discard, edgeTestManifest(), root, "lib", opts)
			if err == nil || !strings.Contains(err.Error(), "needs a public version R or an in-tree version T") {
				t.Fatalf("missing R and T: %v", err)
			}
		})
	}
}

func TestStackReleaseGetPreservesHTTPStatus(t *testing.T) {
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json" {
			fmt.Fprint(w, `not json`)
			return
		}
		http.NotFound(w, r)
	}))
	defer feed.Close()
	var result any
	err := stackReleaseGet(context.Background(), feed.Client(), feed.URL, &result, false)
	var status *stackReleaseHTTPError
	if !errors.As(err, &status) || status.StatusCode != http.StatusNotFound {
		t.Fatalf("HTTP status: %v", err)
	}
	// A successful response with invalid JSON is not an HTTP-status error.
	err = stackReleaseGet(context.Background(), feed.Client(), feed.URL+"/json", &result, false)
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) || errors.As(err, &status) {
		t.Fatalf("JSON error: %v", err)
	}
}
