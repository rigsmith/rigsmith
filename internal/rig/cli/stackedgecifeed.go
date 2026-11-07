package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type stackEdgeCIFeed struct {
	client             *http.Client
	key, flat, publish string
	pushTimeout        time.Duration
	pushHeaderTimeout  time.Duration
}

func stackEdgeCIEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")
}

func stackEdgeCIOpenFeed(ctx context.Context, client *http.Client, index, key string) (*stackEdgeCIFeed, error) {
	// Custom headers must never follow a redirect to another credential sink.
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	f := &stackEdgeCIFeed{client: &c, key: key}
	var service struct {
		Resources []struct {
			ID   string `json:"@id"`
			Type string `json:"@type"`
		} `json:"resources"`
	}
	if _, err := f.get(ctx, index, &service, false); err != nil {
		return nil, err
	}
	for _, resource := range service.Resources {
		switch resource.Type {
		case "PackagePublish/2.0.0":
			if f.publish == "" {
				f.publish = resource.ID
			}
		case "PackageBaseAddress/3.0.0":
			if f.flat == "" {
				f.flat = resource.ID
			}
		}
	}
	if !stackEdgeCIEndpoint(f.flat) || !stackEdgeCIEndpoint(f.publish) {
		return nil, fmt.Errorf("NuGet index needs secure flat-container and PackagePublish/2.0.0 resources")
	}
	return f, nil
}

func (f *stackEdgeCIFeed) request(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	if !stackEdgeCIEndpoint(endpoint) {
		return nil, fmt.Errorf("NuGet endpoint must use HTTPS (HTTP is allowed only on loopback)")
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("cannot prepare NuGet request")
	}
	req.Header.Set("X-NuGet-ApiKey", f.key)
	if method == http.MethodGet {
		// Private NuGet feeds accept the push token as the Basic password
		// for service-index and flat-container reads (as release reads do).
		req.SetBasicAuth("rig", f.key)
	}
	return req, nil
}

func (f *stackEdgeCIFeed) get(ctx context.Context, endpoint string, result any, missingOK bool) (bool, error) {
	req, err := f.request(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	response, err := f.client.Do(req)
	// Neither transport errors nor feed response bodies are safe diagnostics:
	// both can echo the credential, including encoded forms of it.
	if err != nil {
		return false, fmt.Errorf("NuGet read failed (network)")
	}
	defer response.Body.Close()
	if missingOK && response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("NuGet read failed (HTTP %d)", response.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(result); err != nil {
		return false, fmt.Errorf("invalid NuGet index response")
	}
	return true, nil
}

func (f *stackEdgeCIFeed) monotonic(ctx context.Context, id, version string) error {
	if err := stackEdgeCheckMonotonic(version, ""); err != nil {
		return err
	}
	var index struct {
		Versions *[]string `json:"versions"`
	}
	found, err := f.get(ctx, strings.TrimRight(f.flat, "/")+"/"+url.PathEscape(strings.ToLower(id))+"/index.json", &index, true)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if index.Versions == nil || len(*index.Versions) == 0 {
		return fmt.Errorf("feed version index lists no versions")
	}
	target, ok := stackParseNugetVersion(version)
	if !ok {
		return fmt.Errorf("invalid edge version")
	}
	series := target.release[:strings.LastIndex(target.release, ".")]
	newest := ""
	for _, raw := range *index.Versions {
		v, ok := stackParseNugetVersion(raw)
		if !ok {
			return fmt.Errorf("feed contains an invalid NuGet version")
		}
		i := strings.LastIndex(v.release, ".")
		if v.numbers != target.numbers || i < 0 || v.release[:i] != series {
			continue
		}
		previous, _ := stackParseNugetVersion(newest)
		if newest == "" || stackCompareNuget(v, previous) > 0 {
			newest = raw
		}
	}
	return stackEdgeCheckMonotonic(version, newest)
}

func (f *stackEdgeCIFeed) push(ctx context.Context, path, version string) error {
	timeout := f.pushTimeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Reads keep their short total timeout. Uploads use a separate client so
	// a large body is bounded by the push context instead of the read timer.
	client := *f.client
	client.Timeout = 0
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if base, ok := transport.(*http.Transport); ok {
		copy := base.Clone()
		copy.ResponseHeaderTimeout = f.pushHeaderTimeout
		if copy.ResponseHeaderTimeout <= 0 {
			copy.ResponseHeaderTimeout = 2 * time.Minute
		}
		client.Transport = copy
		defer copy.CloseIdleConnections()
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot read package for push")
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("cannot stat package for push")
	}
	// Build only the multipart envelope in memory; stream the package itself.
	var envelope bytes.Buffer
	w := multipart.NewWriter(&envelope)
	if _, err = w.CreateFormFile("package", filepath.Base(path)); err != nil {
		return fmt.Errorf("cannot prepare package upload")
	}
	prefix := append([]byte(nil), envelope.Bytes()...)
	envelope.Reset()
	if err = w.Close(); err != nil {
		return fmt.Errorf("cannot finish package envelope")
	}
	suffix := envelope.Bytes()
	req, err := f.request(ctx, http.MethodPut, f.publish, io.MultiReader(bytes.NewReader(prefix), file, bytes.NewReader(suffix)))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(prefix)+len(suffix)) + stat.Size()
	req.Header.Set("Content-Type", w.FormDataContentType())
	response, err := client.Do(req)
	if err != nil {
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
			return fmt.Errorf("NuGet push timed out for version %s; the package may have been stored. Check the package's version list on the configured feed before rerunning; if present, use a new increasing --number", version)
		}
		return fmt.Errorf("NuGet push failed (network)")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		return fmt.Errorf("NuGet version %s already exists (HTTP 409)", version)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("NuGet push failed (HTTP %d)", response.StatusCode)
	}
	return nil
}
