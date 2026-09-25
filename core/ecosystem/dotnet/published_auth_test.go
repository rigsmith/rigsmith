package dotnet

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// privateFeed serves a feed that answers only user:token (401 otherwise),
// with its flat container at base (this server's own unless given). It
// records every Authorization header it sees, anonymous requests as "".
type privateFeed struct {
	url  string
	mu   sync.Mutex
	seen []string
	// redirect, when set, sends an authenticated flat-container read there
	// (a 302 to redirect + the path).
	redirect string
	// echo, when set, fails an authenticated flat-container read with a 500
	// whose body repeats the credential it was sent.
	echo bool
	// anonRedirect, when set, sends an anonymous flat-container read there.
	anonRedirect string
}

func (f *privateFeed) auths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

func newPrivateFeed(t *testing.T, user, token, base string, open bool) *privateFeed {
	t.Helper()
	f := &privateFeed{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, r.Header.Get("Authorization"))
		f.mu.Unlock()
		if f.anonRedirect != "" && r.URL.Path == "/flat/acme.lib/index.json" && r.Header.Get("Authorization") == "" {
			http.Redirect(w, r, f.anonRedirect+r.URL.Path, http.StatusFound)
			return
		}
		if u, p, ok := r.BasicAuth(); !open && (!ok || u != user || p != token) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.RawQuery == "moved" {
			fmt.Fprint(w, `{"versions":["1.2.0"]}`)
			return
		}
		switch r.URL.Path {
		case "/index.json":
			b := base
			if b == "" {
				b = f.url + "/flat"
			}
			fmt.Fprintf(w, `{"resources":[{"@id":"%s","@type":"PackageBaseAddress/3.0.0"}]}`, b)
		case "/flat/acme.lib/index.json":
			if f.echo {
				_, p, _ := r.BasicAuth()
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, "bad request with %s (password %s)", r.Header.Get("Authorization"), p)
				return
			}
			if f.redirect != "" {
				http.Redirect(w, r, f.redirect+r.URL.Path+"?moved", http.StatusFound)
				return
			}
			fmt.Fprint(w, `{"versions":["1.2.0"]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

func published(t *testing.T, source, user string, auth *plugin.AuthCredential) (bool, error) {
	t.Helper()
	resp, err := (&Adapter{}).Published(context.Background(), plugin.PublishedRequest{
		Package:       plugin.Package{Name: "Acme.Lib", Version: "1.2.0"},
		PackageSource: source,
		Auth:          auth,
		User:          user,
	})
	return resp.Published, err
}

func TestPublishedAsksAPrivateFeedWithThePublishCredential(t *testing.T) {
	t.Setenv("NUGET_API_KEY", "")
	f := newPrivateFeed(t, "shiprig", "tok", "", false)
	ok, err := published(t, f.url+"/index.json", "", &plugin.AuthCredential{Token: "tok"})
	if err != nil || !ok {
		t.Fatalf("published = %v, %v", ok, err)
	}
	// Anonymous first, credentials only once asked.
	if got := f.auths(); len(got) != 4 || got[0] != "" || got[1] == "" || got[2] != "" || got[3] == "" {
		t.Errorf("Authorization headers = %q, want anonymous then Basic, per request", got)
	}
}

func TestPublishedSendsTheConfiguredUser(t *testing.T) {
	t.Setenv("NUGET_API_KEY", "")
	f := newPrivateFeed(t, "acme-bot", "tok", "", false)
	if ok, err := published(t, f.url+"/index.json", "acme-bot", &plugin.AuthCredential{Token: "tok"}); err != nil || !ok {
		t.Fatalf("published = %v, %v", ok, err)
	}
}

func TestPublishedFallsBackToNuGetAPIKey(t *testing.T) {
	t.Setenv("NUGET_API_KEY", "env-tok")
	f := newPrivateFeed(t, "shiprig", "env-tok", "", false)
	if ok, err := published(t, f.url+"/index.json", "", nil); err != nil || !ok {
		t.Fatalf("published = %v, %v", ok, err)
	}
}

// Credentials in the source URL reach the flat container too, not only the
// service index.
func TestPublishedCarriesURLCredentialsToTheFlatContainer(t *testing.T) {
	t.Setenv("NUGET_API_KEY", "")
	f := newPrivateFeed(t, "me", "url-tok", "", false)
	source := strings.Replace(f.url, "http://", "http://me:url-tok@", 1) + "/index.json"
	if ok, err := published(t, source, "", nil); err != nil || !ok {
		t.Fatalf("published = %v, %v", ok, err)
	}
	// Like any credential, they wait to be asked for.
	if got := f.auths(); len(got) == 0 || got[0] != "" {
		t.Errorf("Authorization headers = %q, want the first request anonymous", got)
	}
}

func TestPublishedExplainsAMissingOrRejectedCredential(t *testing.T) {
	t.Setenv("NUGET_API_KEY", "")
	f := newPrivateFeed(t, "shiprig", "tok", "", false)
	if _, err := published(t, f.url+"/index.json", "", nil); err == nil || !strings.Contains(err.Error(), "needs credentials") {
		t.Errorf("no credential: err = %v", err)
	}
	_, err := published(t, f.url+"/index.json", "", &plugin.AuthCredential{Token: "wrong"})
	if err == nil || !strings.Contains(err.Error(), "turned down the credentials") {
		t.Errorf("wrong credential: err = %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "wrong") {
		t.Errorf("the error leaks the token: %v", err)
	}
}

// A public feed is never sent the credential, and neither is another host
// the service index points at.
func TestPublishedSendsCredentialsOnlyWhenAskedAndOnlyToTheSource(t *testing.T) {
	t.Setenv("NUGET_API_KEY", "")
	public := newPrivateFeed(t, "", "", "", true)
	if ok, err := published(t, public.url+"/index.json", "", &plugin.AuthCredential{Token: "tok"}); err != nil || !ok {
		t.Fatalf("public feed: published = %v, %v", ok, err)
	}
	for _, a := range public.auths() {
		if a != "" {
			t.Errorf("a public feed was sent credentials: %q", a)
		}
	}

	elsewhere := newPrivateFeed(t, "shiprig", "tok", "", false)
	f := newPrivateFeed(t, "shiprig", "tok", elsewhere.url+"/flat", false)
	if _, err := published(t, f.url+"/index.json", "", &plugin.AuthCredential{Token: "tok"}); err == nil || !strings.Contains(err.Error(), "only sent to the package source's own host") {
		t.Errorf("another host: err = %v, want it refused without sending, saying why", err)
	}
	for _, a := range elsewhere.auths() {
		if a != "" {
			t.Errorf("another host was sent credentials: %q", a)
		}
	}
}

// nuget.org's reads are public and its key is for pushing: no credentials.
func TestFeedCredentialsNeverForNuGetOrg(t *testing.T) {
	t.Setenv("NUGET_API_KEY", "push-key")
	for _, source := range []string{"", "nuget.org", "https://api.nuget.org/v3/index.json"} {
		if c := feedCredentials(plugin.PublishedRequest{PackageSource: source, Auth: &plugin.AuthCredential{Token: "tok"}}); c != nil {
			t.Errorf("feedCredentials(%q) = %+v, want none", source, c)
		}
	}
}

// A configured credential that couldn't be resolved isn't replaced by
// NUGET_API_KEY, which may be a key for another feed.
func TestPublishedDoesNotSubstituteTheEnvironmentForAnUnresolvedCredential(t *testing.T) {
	t.Setenv("NUGET_API_KEY", "tok")
	f := newPrivateFeed(t, "shiprig", "tok", "", false)
	resp, err := (&Adapter{}).Published(context.Background(), plugin.PublishedRequest{
		Package:         plugin.Package{Name: "Acme.Lib", Version: "1.2.0"},
		PackageSource:   f.url + "/index.json",
		AuthUnavailable: true,
	})
	if err == nil || !strings.Contains(err.Error(), "`dotnet.auth` couldn't be resolved") || resp.Published {
		t.Errorf("published = %v, %v; want the 401 reported against the unresolved dotnet.auth", resp.Published, err)
	}
	for _, a := range f.auths() {
		if a != "" {
			t.Errorf("sent %q in place of the unresolved credential", a)
		}
	}
}

// A credentialed request follows a redirect only where its credentials may
// go: the same host is fine, another host is refused before anything is sent.
func TestPublishedFollowsCredentialedRedirectsOnlyToTheSource(t *testing.T) {
	t.Setenv("NUGET_API_KEY", "")
	same := newPrivateFeed(t, "shiprig", "tok", "", false)
	same.redirect = same.url
	if ok, err := published(t, same.url+"/index.json", "", &plugin.AuthCredential{Token: "tok"}); err != nil || !ok {
		t.Errorf("same-host redirect: published = %v, %v", ok, err)
	}

	elsewhere := newPrivateFeed(t, "", "", "", true)
	f := newPrivateFeed(t, "shiprig", "tok", "", false)
	f.redirect = elsewhere.url
	_, err := published(t, f.url+"/index.json", "", &plugin.AuthCredential{Token: "tok"})
	if err == nil || !strings.Contains(err.Error(), "where its credentials may not go") {
		t.Errorf("cross-host redirect: err = %v", err)
	}
	for _, a := range elsewhere.auths() {
		if a != "" {
			t.Errorf("another host was sent credentials through a redirect: %q", a)
		}
	}
}

// Credentials go over https only (loopback aside, for tests like these).
func TestFeedCredentialsStayOnHTTPS(t *testing.T) {
	c := &feedCreds{host: "feed.example.com", token: "tok"}
	for rawURL, want := range map[string]bool{
		"https://feed.example.com/x":     true,
		"http://feed.example.com/x":      false,
		"https://sub.feed.example.com/x": false,
		"https://feed.example.com:8443/": false,
	} {
		if got := c.allows(rawURL); got != want {
			t.Errorf("allows(%s) = %v, want %v", rawURL, got, want)
		}
	}
}

// Credentials in the source URL are the source's own: they still apply when a
// configured dotnet.auth couldn't be resolved.
func TestPublishedKeepsURLCredentialsWhenAuthIsUnavailable(t *testing.T) {
	t.Setenv("NUGET_API_KEY", "")
	f := newPrivateFeed(t, "me", "url-tok", "", false)
	resp, err := (&Adapter{}).Published(context.Background(), plugin.PublishedRequest{
		Package:         plugin.Package{Name: "Acme.Lib", Version: "1.2.0"},
		PackageSource:   strings.Replace(f.url, "http://", "http://me:url-tok@", 1) + "/index.json",
		AuthUnavailable: true,
	})
	if err != nil || !resp.Published {
		t.Errorf("published = %v, %v", resp.Published, err)
	}
}

// Whichever credential the adapter chose, a feed that echoes it doesn't put
// it in the error, raw or Basic-encoded.
func TestPublishedMasksTheCredentialInErrors(t *testing.T) {
	for name, setup := range map[string]func(f *privateFeed) (string, *plugin.AuthCredential){
		"NUGET_API_KEY": func(f *privateFeed) (string, *plugin.AuthCredential) {
			t.Setenv("NUGET_API_KEY", "echoed-value")
			return f.url + "/index.json", nil
		},
		"source URL": func(f *privateFeed) (string, *plugin.AuthCredential) {
			t.Setenv("NUGET_API_KEY", "")
			return strings.Replace(f.url, "http://", "http://shiprig:echoed-value@", 1) + "/index.json", nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newPrivateFeed(t, "shiprig", "echoed-value", "", false)
			f.echo = true
			source, auth := setup(f)
			_, err := published(t, source, "", auth)
			if err == nil || !strings.Contains(err.Error(), "500") {
				t.Fatalf("err = %v, want the 500", err)
			}
			if strings.Contains(err.Error(), "echoed-value") || strings.Contains(err.Error(), "c2hpcHJpZzplY2hvZWQtdmFsdWU=") {
				t.Errorf("the error leaks the credential: %v", err)
			}
		})
	}
}

// A 401 from a host an anonymous request was redirected to isn't the
// source's: the source isn't re-asked with credentials on its behalf.
func TestPublishedAnswersOnlyTheSourcesOwn401(t *testing.T) {
	t.Setenv("NUGET_API_KEY", "")
	elsewhere := newPrivateFeed(t, "someone", "else", "", false)
	f := newPrivateFeed(t, "", "", "", true)
	f.anonRedirect = elsewhere.url
	_, err := published(t, f.url+"/index.json", "", &plugin.AuthCredential{Token: "tok"})
	if err == nil || !strings.Contains(err.Error(), "only sent to the package source's own host") {
		t.Errorf("err = %v, want the other host's 401 left unanswered", err)
	}
	for _, a := range append(f.auths(), elsewhere.auths()...) {
		if a != "" {
			t.Errorf("credentials were sent: %q", a)
		}
	}
}

// The mask keeps the cause: a cancelled context is still recognisable.
func TestPublishedErrorsKeepTheirCause(t *testing.T) {
	f := newPrivateFeed(t, "", "", "", true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&Adapter{}).Published(ctx, plugin.PublishedRequest{
		Package:       plugin.Package{Name: "Acme.Lib", Version: "1.2.0"},
		PackageSource: f.url + "/index.json",
		Auth:          &plugin.AuthCredential{Token: "tok"},
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
}
