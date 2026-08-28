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
	page, err := os.ReadFile(filepath.Join("..", "frontend", "dist", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)

	called := []string{
		fqn(&Status{}, "Get"),
		fqn(&Activity{}, "Recent"),
		fqn(&Actions{}, "Run"),
		fqn(&Actions{}, "Busy"),
		fqn(&Actions{}, "RunWith"),
		fqn(&Sessions{}, "List"),
		fqn(&Sessions{}, "Read"),
		fqn(&Sessions{}, "Machines"),
		fqn(&Accounts{}, "Get"),
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
		{&Activity{}, []string{"Recent"}},
		{&Actions{}, []string{"Run", "Busy", "RunWith"}},
		{&Sessions{}, []string{"List", "Read", "Machines"}},
		{&Accounts{}, []string{"Get"}},
	} {
		typ := reflect.TypeOf(tc.svc)
		for _, method := range tc.methods {
			if _, ok := typ.MethodByName(method); !ok {
				t.Errorf("%s has no method %q", typ.Elem().Name(), method)
			}
		}
	}
}
