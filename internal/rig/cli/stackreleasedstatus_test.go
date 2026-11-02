package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStackReleasedStatusBudget(t *testing.T) {
	var calls atomic.Int32
	sha := strings.Repeat("a", 40)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.HasSuffix(r.URL.Path, "index.json") {
			<-r.Context().Done()
			return
		}
		if strings.Contains(r.URL.Path, "/compare/") {
			fmt.Fprint(w, `{"status":"ahead"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/pulls/1") {
			fmt.Fprintf(w, `{"merged_at":"today","merge_commit_sha":%q}`, sha)
			return
		}
		fmt.Fprint(w, `[{"number":1,"head":{"ref":"stack/topic","repo":{"full_name":"you/core"}}}]`)
	}))
	defer srv.Close()
	m := &stackManifest{Repos: map[string]*stackRepo{"core": {Upstream: "github.com/acme/core", Fork: "github.com/you/core"}}, LastSync: map[string]string{"core": sha}, Proposals: map[string]map[string]stackProposal{"core": {"topic": {Branch: "stack/topic"}}}}
	a := stackDepsAPI{base: srv.URL, client: srv.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	scans := 0
	annotate := stackReleaseAnnotations(ctx, m, a, srv.URL, func(context.Context) ([]stackLink, error) { scans++; return stackReleaseChain(), nil })
	start := time.Now()
	annotate("app", "core")
	annotate("app", "core")
	annotate("control", "core")
	elapsed := time.Since(start)
	t.Logf("hanging feed: %s, discovery calls: %d, HTTP calls: %d", elapsed, scans, calls.Load())
	if elapsed > 3*time.Second || scans != 1 {
		t.Fatalf("budget/discovery: %s %d", elapsed, scans)
	}
}

func TestStackReleasedStatusNoMerged(t *testing.T) {
	for _, empty := range []bool{true, false} {
		t.Run(fmt.Sprint(empty), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if strings.HasSuffix(r.URL.Path, "/pulls/1") {
					fmt.Fprint(w, `{"state":"open"}`)
				} else {
					fmt.Fprint(w, `[{"number":1,"head":{"ref":"stack/topic","repo":{"full_name":"you/core"}}}]`)
				}
			}))
			defer srv.Close()
			m := &stackManifest{Repos: map[string]*stackRepo{"app": {Upstream: "github.com/acme/app"}, "core": {Upstream: "github.com/acme/core", Fork: "github.com/you/core"}}, Proposals: map[string]map[string]stackProposal{}}
			if !empty {
				m.Proposals = map[string]map[string]stackProposal{"app": {"topic": {DependsOn: []string{"core"}}}, "core": {"topic": {Branch: "stack/topic"}}}
			}
			scans := 0
			annotate := stackReleaseAnnotations(context.Background(), m, stackDepsAPI{}, srv.URL, func(context.Context) ([]stackLink, error) { scans++; return nil, nil })
			stackDepsReport(context.Background(), m, stackDepsAPI{base: srv.URL, client: srv.Client()}, io.Discard, false, annotate)
			if scans != 0 {
				t.Fatal("discovery without merged proposal")
			}
			if !empty && calls.Load() != 2 {
				t.Fatalf("expected open-PR lookups only; got %d", calls.Load())
			}
			if empty && calls.Load() != 0 {
				t.Fatal("network without proposals")
			}
		})
	}
}

func TestStackReleasedStatusDiscoveryOnceAcrossMembers(t *testing.T) {
	scans := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	annotate := stackReleaseAnnotations(ctx, &stackManifest{}, stackDepsAPI{}, "", func(c context.Context) ([]stackLink, error) {
		scans++
		if _, ok := c.Deadline(); !ok {
			t.Fatal("discovery lost deadline")
		}
		return stackReleaseChain(), nil
	})
	annotate("app", "core")
	annotate("app", "core")
	annotate("control", "core")
	if scans != 1 {
		t.Fatalf("discovery repeated %d times", scans)
	}
}
