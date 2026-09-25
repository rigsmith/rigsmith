package dotnet

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// Published asks the feed whether the version exists, through NuGet's
// package base address (the "flat container"): GET <base>/<id>/index.json
// lists every version of a package, and a 404 means the package has none. Any
// other failure is an error rather than "not published". A package that isn't
// packable has no registry.
func (a *Adapter) Published(ctx context.Context, req plugin.PublishedRequest) (plugin.PublishedResponse, error) {
	if req.Package.Private {
		return plugin.PublishedResponse{NoRegistry: true}, nil
	}
	what := req.Package.Name + "@" + req.Package.Version
	creds := feedCredentials(req)
	base, err := packageBaseAddress(ctx, req.PackageSource, creds)
	if err != nil {
		return plugin.PublishedResponse{}, fmt.Errorf("checking %s on the feed: %w", what, err)
	}
	var index struct {
		Versions *[]string `json:"versions"`
	}
	found, err := getJSON(ctx, base+strings.ToLower(req.Package.Name)+"/index.json", &index, creds)
	if err != nil {
		return plugin.PublishedResponse{}, fmt.Errorf("checking %s on the feed: %w", what, err)
	}
	if !found {
		return plugin.PublishedResponse{}, nil
	}
	// A 200 means the package has versions (a package with none is a 404), so
	// one without a list, or with an empty one, isn't an answer.
	if index.Versions == nil || len(*index.Versions) == 0 {
		return plugin.PublishedResponse{}, fmt.Errorf("checking %s on the feed: its version index lists no versions", what)
	}
	want := normalizeNuGetVersion(req.Package.Version)
	for _, v := range *index.Versions {
		if normalizeNuGetVersion(v) == want {
			return plugin.PublishedResponse{Published: true}, nil
		}
	}
	return plugin.PublishedResponse{}, nil
}

// normalizeNuGetVersion is NuGet's normalized form, which a feed lists:
// build metadata dropped, leading zeros dropped from the numbers, a fourth
// number dropped when it's zero, at least three numbers, and lowercase.
// "01.0.0.0-Beta+abc" and "1.0.0-beta" are the same version.
func normalizeNuGetVersion(v string) string {
	v, _, _ = strings.Cut(v, "+")
	release, pre, hasPre := strings.Cut(v, "-")
	parts := strings.Split(release, ".")
	for i, p := range parts {
		if trimmed := strings.TrimLeft(p, "0"); trimmed != "" {
			parts[i] = trimmed
		} else {
			parts[i] = "0"
		}
	}
	if len(parts) == 4 && parts[3] == "0" {
		parts = parts[:3]
	}
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	out := strings.Join(parts, ".")
	if hasPre {
		out += "-" + pre
	}
	return strings.ToLower(out)
}

// nugetOrgBase is nuget.org's package base address.
const nugetOrgBase = "https://api.nuget.org/v3-flatcontainer/"

// packageBaseAddress resolves a package source to its flat-container base
// (with a trailing slash): nuget.org for the default names, else the
// PackageBaseAddress resource the source's v3 service index names. A source
// given by its NuGet.config name can't be resolved from here.
func packageBaseAddress(ctx context.Context, source string, creds *feedCreds) (string, error) {
	switch {
	case source == "" || strings.EqualFold(source, "nuget") || strings.EqualFold(source, "nuget.org"):
		return nugetOrgBase, nil
	case !strings.HasPrefix(source, "http"):
		return "", fmt.Errorf("can't look up versions on the named source %q: give its v3 service index URL as the package source", source)
	}
	var index struct {
		Resources []struct {
			ID   string `json:"@id"`
			Type string `json:"@type"`
		} `json:"resources"`
	}
	found, err := getJSON(ctx, source, &index, creds)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%s: no v3 service index there", redactURL(source))
	}
	for _, r := range index.Resources {
		if strings.HasPrefix(r.Type, "PackageBaseAddress/3.0.0") {
			return strings.TrimSuffix(r.ID, "/") + "/", nil
		}
	}
	return "", fmt.Errorf("%s: the service index names no PackageBaseAddress", redactURL(source))
}

// registryHTTP is the client for feed queries: a variable so a test can
// shorten its timeout.
var registryHTTP = &http.Client{Timeout: 30 * time.Second}

// redactURL hides credentials a feed URL may carry (https://user:token@…)
// before it goes into an error.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(unparseable feed URL)"
	}
	return u.Redacted()
}

// withoutURL drops the URL a *url.Error repeats, keeping its cause, so the
// message names the (redacted) URL once. The client masks a password in its
// own errors already; redactURL is what keeps credentials out of ours.
func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// feedCreds is what a private feed is asked with: HTTP Basic auth, scoped to
// the configured source's host.
type feedCreds struct {
	host, user, token string
	// unavailable: a configured dotnet.auth couldn't be resolved, so there
	// is no token, and NUGET_API_KEY wasn't taken in its place.
	unavailable bool
}

// mask hides the token, and the Basic value carrying it, in s: a feed may
// echo what it was sent.
func (c *feedCreds) mask(s string) string {
	if c == nil || c.token == "" {
		return s
	}
	basic := base64.StdEncoding.EncodeToString([]byte(c.user + ":" + c.token))
	return strings.NewReplacer(basic, "***", c.token, "***").Replace(s)
}

// feedCredentials works out the credentials for a feed read: those in the
// source URL (https://user:token@…), else the publish credential (the
// resolved `dotnet.auth`, else NUGET_API_KEY), which private feeds (GitHub
// Packages, Azure Artifacts, feedz) accept as the password. Never for
// nuget.org, whose reads are public and whose key is for pushing only.
func feedCredentials(req plugin.PublishedRequest) *feedCreds {
	u, err := url.Parse(req.PackageSource)
	if err != nil || !strings.HasPrefix(u.Scheme, "http") || strings.EqualFold(u.Hostname(), "api.nuget.org") {
		return nil
	}
	c := &feedCreds{host: strings.ToLower(u.Host), user: req.User, unavailable: req.AuthUnavailable}
	if u.User != nil {
		c.user = u.User.Username()
		c.token, _ = u.User.Password()
	}
	if c.token == "" && req.Auth != nil {
		c.token = req.Auth.Token
	}
	// A configured credential that couldn't be resolved isn't replaced by
	// the environment's: that key may be for somewhere else entirely.
	if c.token == "" && !req.AuthUnavailable {
		c.token = os.Getenv("NUGET_API_KEY")
	}
	if c.user == "" {
		c.user = "shiprig" // most feeds take any name with a token
	}
	return c
}

// getJSON GETs rawURL into dst. It reports false, with no error, for a 404;
// any other non-200 is an error. A 401 from the source's own host is retried
// once with creds, when there are any; the credentials never go anywhere
// else, and never unasked. Errors carry the URL with its credentials
// redacted.
func getJSON(ctx context.Context, rawURL string, dst any, creds *feedCreds) (bool, error) {
	found, err := getJSONUnmasked(ctx, rawURL, dst, creds)
	if err != nil {
		// Whatever the credential's source (dotnet.auth, the source URL,
		// NUGET_API_KEY), it never reaches the message; the cause stays
		// reachable for errors.Is (a cancelled context, a deadline).
		err = &maskedError{msg: creds.mask(err.Error()), err: err}
	}
	return found, err
}

// maskedError is err with its message masked. It unwraps to err's own cause
// (a cancelled context, a deadline, a transport or decode error), not to err:
// err's message is the unmasked one, which may carry a feed's response body.
type maskedError struct {
	msg string
	err error
}

func (e *maskedError) Error() string { return e.msg }
func (e *maskedError) Unwrap() error { return errors.Unwrap(e.err) }

func getJSONUnmasked(ctx context.Context, rawURL string, dst any, creds *feedCreds) (bool, error) {
	shown := redactURL(rawURL)
	resp, err := feedGet(ctx, rawURL, nil)
	if err != nil {
		return false, fmt.Errorf("%s: %w", shown, withoutURL(err))
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		// The host that asked: an anonymous request may have been
		// redirected, and only the source's own host is answered.
		asked := resp.Request.URL.String()
		switch {
		// Both the host that asked and the URL the retry goes to must be the
		// source's: the retry is sent to rawURL, before any redirect check.
		case creds != nil && creds.token != "" && (!creds.allows(asked) || !creds.allows(rawURL)):
			return false, fmt.Errorf("%s: %s: this host asks for credentials, but they're only sent to the package source's own host, %s (over https, or plain http on loopback)", shown, resp.Status, creds.host)
		case creds != nil && creds.token == "" && creds.unavailable:
			return false, fmt.Errorf("%s: %s: the feed needs credentials, and the configured `dotnet.auth` couldn't be resolved (NUGET_API_KEY isn't used in its place)", shown, resp.Status)
		case creds == nil || creds.token == "":
			return false, fmt.Errorf("%s: %s: the feed needs credentials. Set `dotnet.auth` (op://…, env:NAME, cmd:…) or NUGET_API_KEY to a token it accepts for reading, and `dotnet.user` if it checks the account name", shown, resp.Status)
		}
		if resp, err = feedGet(ctx, rawURL, creds); err != nil {
			return false, fmt.Errorf("%s: %w", shown, withoutURL(err))
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			resp.Body.Close()
			return false, fmt.Errorf("%s: %s: the feed turned down the credentials (from the source URL, `dotnet.auth` or NUGET_API_KEY, as user %q): does the token have read access to it?", shown, resp.Status, creds.user)
		}
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return false, fmt.Errorf("%s: %w", shown, err)
		}
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("%s: %s %s", shown, resp.Status, strings.TrimSpace(string(body)))
	}
}

// allows reports whether creds may be sent to rawURL: the source's own host,
// over https (or plain http to a local test server).
func (c *feedCreds) allows(rawURL string) bool {
	if c == nil {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(u.Host, c.host) {
		return false
	}
	return u.Scheme == "https" || isLoopback(u.Hostname())
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

// feedGet is one GET, with Basic auth when creds are given. Credentials in
// the URL itself are dropped: creds carries them, scoped to the host. A
// credentialed request follows a redirect only where the credentials may go:
// Go keeps Authorization across a redirect to a subdomain, or from https to
// http on the same host, and either would send them where they don't belong.
func feedGet(ctx context.Context, rawURL string, creds *feedCreds) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.URL.User = nil
	if creds == nil {
		return registryHTTP.Do(req)
	}
	// CheckRedirect guards every hop but the first: guard that one here.
	if !creds.allows(rawURL) {
		return nil, fmt.Errorf("refusing to send the feed's credentials to %s", redactURL(rawURL))
	}
	req.SetBasicAuth(creds.user, creds.token)
	client := *registryHTTP
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if !creds.allows(next.URL.String()) {
			return fmt.Errorf("the feed redirected a credentialed request to %s, where its credentials may not go", redactURL(next.URL.String()))
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return client.Do(req)
}
