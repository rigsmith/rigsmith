package cli

import (
	"bytes"
	"context"
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

func TestStackEdgeCommandHiddenAndHereRequired(t *testing.T) {
	stack := newStackCmd()
	edge, _, err := stack.Find([]string{"edge"})
	if err != nil || !edge.Hidden || edge.RunE == nil {
		t.Fatalf("edge group: %+v, %v", edge, err)
	}
	var output bytes.Buffer
	stack.SetOut(&output)
	if err := stack.Help(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "edge") {
		t.Fatalf("edge leaked into help: %s", output.String())
	}
	cmd := newStackEdgePackCmd()
	if err := cmd.RunE(cmd, []string{"lib"}); err == nil || !strings.Contains(err.Error(), "reconstruction mode comes later") {
		t.Fatalf("without --here: %v", err)
	}
	if cmd.ValidArgsFunction == nil || cmd.Flags().ShorthandLookup("o") == nil || cmd.Flags().Lookup("number").DefValue != "0" {
		t.Fatal("missing flag/completion contract")
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

func TestStackEdgePublicVersionRefusesFailedFeed(t *testing.T) {
	feed := httptest.NewServer(http.NotFoundHandler())
	defer feed.Close()
	if _, err := stackEdgePublicVersion(context.Background(), feed.Client(), feed.URL, "Lib"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("feed: %v", err)
	}
}
