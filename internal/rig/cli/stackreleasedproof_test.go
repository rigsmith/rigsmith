package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStackReleaseProposalProof(t *testing.T) {
	merge, cursor, release := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	for _, tc := range []struct{ name, merged, cursorStatus, releaseStatus, repo, commit, want, failure string }{
		{"open", "null", "ahead", "ahead", "github.com/acme/lib", release, "open", ""},
		{"closed-unmerged", "null", "ahead", "ahead", "github.com/acme/lib", release, "open", ""},
		{"merged", `"today"`, "behind", "diverged", "github.com/acme/lib", release, "merged", ""},
		{"in-cursor", `"today"`, "ahead", "behind", "github.com/acme/lib", release, "merged and in cursor", ""},
		{"identical-cursor", `"today"`, "identical", "behind", "github.com/acme/lib", release, "merged and in cursor", ""},
		{"released", `"today"`, "behind", "ahead", "github.com/acme/lib", release, "released in V", ""},
		{"identical-release", `"today"`, "behind", "identical", "github.com/acme/lib", release, "released in V", ""},
		{"missing-metadata", `"today"`, "ahead", "ahead", "github.com/acme/lib", "", "merged and in cursor", "can't tell"},
		{"private-edge-is-not-public-proof", `"today"`, "ahead", "ahead", "github.com/you/lib", release, "merged and in cursor", "can't tell"},
		{"cursor-error", `"today"`, "error", "ahead", "github.com/acme/lib", release, "merged", "can't check cursor"},
		{"release-error", `"today"`, "ahead", "error", "github.com/acme/lib", release, "merged and in cursor", "can't check release"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, numbered := range []bool{false, true} {
				t.Run(fmt.Sprint(numbered), func(t *testing.T) {
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch {
						case strings.HasSuffix(r.URL.Path, "/pulls/7"):
							fmt.Fprintf(w, `{"number":7,"state":"closed","merged_at":%s,"merge_commit_sha":%q}`, tc.merged, merge)
						case strings.HasSuffix(r.URL.Path, "/pulls"):
							fmt.Fprint(w, `[{"number":7,"head":{"ref":"stack/topic","repo":{"full_name":"you/lib"}}}]`)
						case strings.Contains(r.URL.Path, "/compare/"):
							status := tc.releaseStatus
							if strings.HasSuffix(r.URL.Path, cursor) {
								status = tc.cursorStatus
							}
							if status == "error" {
								w.WriteHeader(503)
								return
							}
							fmt.Fprintf(w, `{"status":%q}`, status)
						case strings.HasSuffix(r.URL.Path, "/index.json"):
							fmt.Fprint(w, `{"versions":["1.0.0","2.0.0"]}`)
						case strings.HasSuffix(r.URL.Path, ".nuspec"):
							fmt.Fprintf(w, `<package><metadata><repository url="https://%s" commit="%s" /></metadata></package>`, tc.repo, tc.commit)
						default:
							t.Errorf("unexpected request %s", r.URL)
							w.WriteHeader(404)
						}
					}))
					defer srv.Close()
					m := &stackManifest{Repos: map[string]*stackRepo{"lib": {Upstream: "github.com/acme/lib", Fork: "github.com/you/lib"}}, LastSync: map[string]string{"lib": cursor}, Proposals: map[string]map[string]stackProposal{"lib": {"other-open": {Branch: "stack/open"}}}}
					item := stackReleaseItem{Proposal: stackProposal{Branch: "stack/topic"}}
					if numbered {
						item = stackReleaseItem{PR: 7}
						m.Repos["lib"].Fork = ""
					}
					proof, err := stackReleaseProposalProof(context.Background(), stackDepsAPI{base: srv.URL, client: srv.Client()}, m, "lib", item, "Lib", "", srv.URL)
					if proof.Status != tc.want || (tc.failure == "" && err != nil) || (tc.failure != "" && (err == nil || !strings.Contains(err.Error(), tc.failure))) {
						t.Fatalf("proof=%+v err=%v", proof, err)
					}
					if proof.Status == "released in V" && (proof.Version != "2.0.0" || proof.ReleaseCommit != release || proof.MergeCommit != merge) {
						t.Fatalf("missing release evidence: %+v", proof)
					}
				})
			}
		})
	}
}
