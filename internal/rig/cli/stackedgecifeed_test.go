package cli

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStackEdgeCIFeedFailures(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"missing-version-list", `{}`, "lists no versions"},
		{"empty-version-list", `{"versions":[]}`, "lists no versions"},
		{"invalid-version", `{"versions":["invalid"]}`, "invalid NuGet version"},
		{"non-increasing", `{"versions":["1.2.4-edge.7"]}`, "increase"},
		{"lower-and-other-series", `{"versions":["1.2.4-edge.6","9.0.0-edge.99","1.2.4-beta.99"]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer s.Close()
			f := &stackEdgeCIFeed{client: s.Client(), flat: s.URL, key: edgeCIKey}
			err := f.monotonic(t.Context(), "You.Lib", "1.2.4-edge.7")
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), edgeCIKey) {
				t.Fatal(err)
			}
		})
	}
}

func TestStackEdgeCIFeedNetworkAndRedirect(t *testing.T) {
	client := &http.Client{Transport: stackDepsRoundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("transport echoed " + edgeCIKey) })}
	if _, err := stackEdgeCIOpenFeed(t.Context(), client, "https://feed.invalid/index.json", edgeCIKey); err == nil || strings.Contains(err.Error(), edgeCIKey) || !strings.Contains(err.Error(), "network") {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "package.nupkg")
	if err := os.WriteFile(file, []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	f := &stackEdgeCIFeed{client: client, publish: "https://feed.invalid/push", key: edgeCIKey}
	if err := f.push(t.Context(), file, "1.2.4-edge.7"); err == nil || strings.Contains(err.Error(), edgeCIKey) || !strings.Contains(err.Error(), "network") {
		t.Fatal(err)
	}
	contacts := 0
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacts++; t.Error("credential redirect followed") }))
	defer sink.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, sink.URL, http.StatusFound) }))
	defer source.Close()
	if _, err := stackEdgeCIOpenFeed(t.Context(), source.Client(), source.URL, edgeCIKey); err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatal(err)
	}
	if contacts != 0 {
		t.Fatal("redirect sink received a request")
	}
}

func TestStackEdgeCIStaticPackability(t *testing.T) {
	for _, tc := range []struct {
		name, props, project string
		want                 bool
		wantError            string
	}{
		{name: "default", want: true},
		{name: "explicit-false", project: "<IsPackable>false</IsPackable>"},
		{name: "literal-props-exclusion", props: "<IsPackable>false</IsPackable>", project: "<IsPackable>true</IsPackable>"},
		{name: "test-default", project: "<IsTestProject>true</IsTestProject>"},
		{name: "literal-test-exclusion", project: "<IsTestProject>true</IsTestProject><IsPackable>true</IsPackable>"},
		{name: "literal-conditional-exclusion", project: `<IsPackable Condition="'$(Configuration)' == 'Debug'">false</IsPackable>`},
		{name: "literal-group-exclusion", project: `</PropertyGroup><PropertyGroup Condition="'$(Configuration)' == 'Debug'"><IsPackable>false</IsPackable>`},
		{name: "unknown-property-is-allowed", project: "<IsPackable>$(SomeProperty)</IsPackable>", want: true},
		{name: "malformed-metadata", project: "<IsPackable>false", wantError: "XML syntax"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "Directory.Build.props"), []byte("<Project><PropertyGroup>"+tc.props+"</PropertyGroup></Project>"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "Lib.csproj"), []byte("<Project><PropertyGroup>"+tc.project+"</PropertyGroup></Project>"), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := stackEdgeCIIsPackable(root, "Lib.csproj")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %t, %v", got, err)
			}
		})
	}
}
