package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
)

// recordingNode is the node adapter with Publish replaced: it records each
// request and publishes nothing.
type recordingNode struct {
	plugin.Ecosystem
	mu   *sync.Mutex
	reqs *[]plugin.PublishRequest
}

func (r recordingNode) Publish(_ context.Context, req plugin.PublishRequest) (plugin.PublishResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.reqs = append(*r.reqs, req)
	return plugin.PublishResponse{Published: true}, nil
}

// runPublishRecording runs `shiprig publish` with args in the current
// directory's workspace, its npm adapter recording, and returns what each
// package was asked to publish, in order.
func runPublishRecording(t *testing.T, args ...string) []plugin.PublishRequest {
	t.Helper()
	var mu sync.Mutex
	var reqs []plugin.PublishRequest
	was := openPublishWorkspace
	openPublishWorkspace = func() (*commands.Workspace, error) {
		ws, err := commands.Open()
		if err != nil {
			return nil, err
		}
		node, _ := ws.EcosystemFor("node")
		ws.Registry.Register(recordingNode{Ecosystem: node, mu: &mu, reqs: &reqs})
		return ws, nil
	}
	t.Cleanup(func() { openPublishWorkspace = was })

	cmd := newPublishCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--yes", "--no-git-tag"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("publish: %v\n%s", err, out.String())
	}
	return reqs
}

// From a pack directory, publish asks for exactly the packed releases, in the
// plan's order, each with its file and the plan's dist-tag, and nothing else.
func TestPublishFromPackDirSendsThePackedFiles(t *testing.T) {
	planRepo(t, `{}`)
	var built []string
	// done is published and untagged: tag-only, never published again.
	dir, _, err := packWith(t, fakePacker{fakeRegistry: fakeRegistry{published: map[string]bool{"done": true}}, built: &built}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The plan's access wins over the config's: make lib's differ.
	planPath := filepath.Join(dir, packDirPlan)
	plan, err := readPublishPlan(planPath)
	if err != nil {
		t.Fatal(err)
	}
	for ci := range plan {
		for ri := range plan[ci] {
			if plan[ci][ri].Name == "lib" {
				plan[ci][ri].Access = "public"
			}
		}
	}
	if err := writePublishPlan(planPath, plan); err != nil {
		t.Fatal(err)
	}

	reqs := runPublishRecording(t, "--from-pack-dir", dir)
	var got []string
	for _, r := range reqs {
		got = append(got, r.Package.Name+" "+filepath.Base(r.ArtifactPath)+" "+r.Tag)
		if r.Package.Name == "lib" && r.Access != "public" {
			t.Errorf("lib access = %q, want the plan's public", r.Access)
		}
		if !strings.HasPrefix(r.ArtifactPath, dir) {
			t.Errorf("%s: artifact %q isn't the packed file", r.Package.Name, r.ArtifactPath)
		}
	}
	want := "lib lib-1.0.0.tgz latest|app app-2.0.0.tgz latest"
	if strings.Join(got, "|") != want {
		t.Errorf("publish requests = %v, want %s", got, want)
	}
}

// In pre mode an ordinary publish asks for the prerelease dist-tag; outside
// it, --tag names one; with neither, none.
func TestPublishPassesTheDistTag(t *testing.T) {
	for _, tc := range []struct {
		name string
		pre  bool
		args []string
		want string
	}{
		{"pre mode", true, nil, "next"},
		{"--tag", false, []string{"--tag", "canary"}, "canary"},
		{"neither", false, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := planRepo(t, `{}`)
			if tc.pre {
				if err := os.WriteFile(filepath.Join(root, ".changeset", "pre.json"),
					[]byte(`{ "mode": "pre", "tag": "next", "initialVersions": {}, "changesets": [] }`), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			reqs := runPublishRecording(t, tc.args...)
			if len(reqs) == 0 {
				t.Fatal("publish asked for nothing")
			}
			for _, r := range reqs {
				if r.Tag != tc.want || r.ArtifactPath != "" {
					t.Errorf("%s: tag %q, artifact %q; want tag %q and no artifact", r.Package.Name, r.Tag, r.ArtifactPath, tc.want)
				}
			}
		})
	}
}

// Only npm has dist-tags: a Go-only prerelease tagged "x", which npm would
// refuse as a range, publishes without the npm check getting in the way.
func TestPublishGoOnlyPrereleaseIgnoresNpmTagRules(t *testing.T) {
	root := t.TempDir()
	for rel, content := range map[string]string{
		".changeset/config.json": `{}`,
		".changeset/pre.json":    `{ "mode": "pre", "tag": "x", "initialVersions": {}, "changesets": [] }`,
		"go.mod":                 "module example.com/tool\n\ngo 1.22\n",
		"main.go":                "package main\n\nfunc main() {}\n",
	} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=x@y.z", "-c", "user.name=x", "-c", "commit.gpgsign=false", "add", "-A"},
		{"-c", "user.email=x@y.z", "-c", "user.name=x", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Chdir(root)
	// Fails the test if publish refuses the tag.
	if reqs := runPublishRecording(t); len(reqs) != 0 {
		t.Errorf("npm was asked to publish %d package(s) in a Go-only repo", len(reqs))
	}
}
