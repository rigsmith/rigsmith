package bridge

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fqn builds the key Wails registers a bound method under, the same way
// application.bindings does: "<import path>.<type>.<method>".
func fqn(service any, method string) string {
	t := reflect.TypeOf(service).Elem()
	return t.PkgPath() + "." + t.Name() + "." + method
}

// The frontend calls bound methods by that string because we deliberately skip
// `wails3 generate bindings` (it would add a Node step to a Go-only CI). Nothing
// else ties the two together, so moving this package or renaming Status.Get
// would compile cleanly and fail only at runtime, in a window nobody has open.
// This test is that missing link.
func TestFrontendCallsMatchBoundMethods(t *testing.T) {
	// Every page, not just index.html: the window is more than one screen now,
	// and a method wired from sessions.html is just as bound as one wired from
	// the status page.
	pages, err := filepath.Glob(filepath.Join("..", "frontend", "dist", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 {
		t.Fatal("no frontend pages found — the embed would ship an empty window")
	}
	var b strings.Builder
	for _, p := range pages {
		body, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Fatal(rerr)
		}
		b.Write(body)
	}
	html := b.String()

	called := []string{
		fqn(&Status{}, "Get"),
		fqn(&Activity{}, "Recent"),
		fqn(&Activity{}, "Files"),
		fqn(&Repo{}, "Get"),
		fqn(&Repo{}, "Prune"),
		fqn(&Repo{}, "Repack"),
		fqn(&Chooser{}, "Directory"),
		fqn(&Actions{}, "Run"),
		fqn(&Actions{}, "Busy"),
		fqn(&Actions{}, "RunWith"),
		fqn(&Library{}, "List"),
		fqn(&Library{}, "Detail"),
		fqn(&Library{}, "OpenTerminal"),
		fqn(&Library{}, "OpenDesktop"),
		fqn(&Library{}, "OpenVSCode"),
		fqn(&Library{}, "Delete"),
		fqn(&Library{}, "RerootSession"),
		fqn(&Library{}, "Materialize"),
		fqn(&Library{}, "HandOff"),
		fqn(&Library{}, "TakeHandOff"),
		fqn(&Accounts{}, "Get"),
		fqn(&Accounts{}, "OpenDesktop"),
		fqn(&Accounts{}, "RunCLI"),
		fqn(&Windows{}, "Open"),
		fqn(&Windows{}, "Hide"),
	}
	for _, want := range called {
		if !strings.Contains(html, want) {
			t.Errorf("frontend does not reference the bound method %q — "+
				"the window will fail at runtime with 'unknown bound method name'", want)
		}
	}
}

// Every method the frontend names must actually exist on the service, so a
// typo in the HTML is caught here rather than in a live window.
func TestBoundMethodsExist(t *testing.T) {
	for _, tc := range []struct {
		svc     any
		methods []string
	}{
		{&Status{}, []string{"Get", "Health"}},
		{&Activity{}, []string{"Recent", "Files"}},
		{&Repo{}, []string{"Get", "Prune", "Repack"}},
		{&Chooser{}, []string{"Directory"}},
		{&Actions{}, []string{"Run", "Busy", "RunWith"}},
		{&Library{}, []string{"List", "Detail", "OpenTerminal", "OpenDesktop", "OpenVSCode", "Materialize", "HandOff", "TakeHandOff", "Delete", "RerootSession"}},
		{&Accounts{}, []string{"Get", "OpenDesktop", "RunCLI"}},
		{&Windows{}, []string{"Open", "Hide"}},
	} {
		typ := reflect.TypeOf(tc.svc)
		for _, method := range tc.methods {
			if _, ok := typ.MethodByName(method); !ok {
				t.Errorf("%s has no method %q", typ.Elem().Name(), method)
			}
		}
	}
}
