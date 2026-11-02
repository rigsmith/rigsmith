package cli

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func stackReleaseChain() []stackLink {
	return []stackLink{
		{Redirect: plugin.Redirect{Package: "Acme.Core"}, From: []string{"control"}, To: "core"},
		{Redirect: plugin.Redirect{Package: "Acme.Control", Reachable: []string{"Acme.Core"}}, From: []string{"app"}, To: "control"},
	}
}
func TestStackReleasedDelivery(t *testing.T) {
	for _, tc := range []struct {
		name, dependencies string
		ok                 bool
	}{
		{"bare", `<dependency id="Acme.Core" version="2.0.0" />`, true},
		{"range", `<dependency id="Acme.Core" version="[2.0.0,3.0.0)" />`, true},
		{"old", `<dependency id="Acme.Core" version="[1.0.0,3.0.0)" />`, false},
		{"unbounded", `<dependency id="Acme.Core" version="(,3.0.0]" />`, false},
		{"excluded", `<dependency id="Acme.Core" version="2.0.0" exclude="Compile" />`, false},
		{"floating", `<dependency id="Acme.Core" version="2.*" />`, false},
		{"groups", `<group targetFramework="net8.0"><dependency id="Acme.Core" version="[2.0.0,)" /></group><group targetFramework="net10.0"><dependency id="Acme.Core" version="[2.1.0]" /></group>`, true},
		{"old-group", `<group targetFramework="net8.0"><dependency id="Acme.Core" version="1.0.0" /></group><group targetFramework="net10.0"><dependency id="Acme.Core" version="2.0.0" /></group>`, false},
		{"empty-group", `<group targetFramework="net8.0" /><group targetFramework="net10.0"><dependency id="Acme.Core" version="2.0.0" /></group>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `<package><metadata><dependencies>%s</dependencies></metadata></package>`, tc.dependencies)
			}))
			defer srv.Close()
			p, v, note, err := stackReleaseDelivery(context.Background(), stackDepsAPI{client: srv.Client()}, stackReleaseChain(), "app", "core", "Acme.Core", "2.0.0", "", "4.0.0", srv.URL)
			if tc.ok {
				if err != nil || p != "Acme.Control" || v != "4.0.0" || !strings.Contains(note, "core first, then control, then app") {
					t.Fatalf("%s %s %s %v", p, v, note, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "guards unchanged") || !strings.Contains(err.Error(), "Acme.Core >= 2.0.0") {
				t.Fatalf("%v", err)
			}
		})
	}
}
func TestStackReleasedDeliveryDirect(t *testing.T) {
	links := stackReleaseChain()
	links[0].From = append(links[0].From, "app")
	p, v, _, err := stackReleaseDelivery(context.Background(), stackDepsAPI{}, links, "app", "core", "Acme.Core", "2.0.0", "", "", "")
	if err != nil || p != "Acme.Core" || v != "2.0.0" {
		t.Fatalf("%s %s %v", p, v, err)
	}
}
func TestStackReleasedMinimum(t *testing.T) {
	for _, s := range []string{"2.0.0", "[2.0.0]", "[2.0.0,)", "(2.0.0,3.0.0)"} {
		if _, ok := stackReleaseMinimum(s); !ok {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"", "*", "(,3.0.0]", "[3.0.0,2.0.0]", "(2.0.0)", "2.*", "[2.0.0,2.0.0)"} {
		if _, ok := stackReleaseMinimum(s); ok {
			t.Fatal(s)
		}
	}
	var d stackReleaseDependencies
	if err := xml.Unmarshal([]byte(`<dependencies><group targetFramework="net8.0" /></dependencies>`), &d); err != nil {
		t.Fatal(err)
	}
	if d.delivers("Acme.Core", "2.0.0") {
		t.Fatal("missing group dependency accepted")
	}
}

func TestStackReleasedDeliveryRefusalKeepsGuards(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Code.cs")
	before := []byte("#if STACK_ACME_CORE\nApi.New();\n#else\nApi.Old();\n#endif\n")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<package><metadata><dependencies><dependency id="Acme.Core" version="1.0.0" /></dependencies></metadata></package>`)
	}))
	defer srv.Close()
	pin, v, _, err := stackReleaseDelivery(context.Background(), stackDepsAPI{client: srv.Client()}, stackReleaseChain(), "app", "core", "Acme.Core", "2.0.0", "", "1.0.0", srv.URL)
	if err == nil {
		t.Fatal("unsafe delivery accepted")
	}
	if pin != "" || v != "" {
		t.Fatal("refusal must not produce an editable pin")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("guard changed: %s %v", after, err)
	}
}
