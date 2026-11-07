package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (f *edgeCITestFixture) editSeed(t *testing.T, edit func(*stackManifest)) {
	t.Helper()
	m, _, err := loadStackManifest(f.checkout)
	if err != nil {
		t.Fatal(err)
	}
	edit(m)
	if err = stackReconstructWrite(f.checkout, m); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, f.checkout, "add", "rig.stack.jsonc")
	mustGitStack(t, f.checkout, "-c", "user.name=test", "-c", "user.email=test@localhost", "commit", "-qm", "update seed")
	mustGitStack(t, f.checkout, "push", "-q", f.o.Seed, "HEAD")
	f.r.SeedBase = strings.TrimSpace(mustGitStack(t, f.checkout, "rev-parse", "HEAD"))
}

func (f *edgeCITestFixture) addSibling(t *testing.T, files map[string]string, prefix string) string {
	t.Helper()
	f.g.seed(t, "acme/app", "app")
	work := t.TempDir()
	mustGitStack(t, work, "clone", "-q", f.g.path("acme/app"), work)
	for path, body := range files {
		path = filepath.Join(work, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mustGitStack(t, work, "add", ".")
	mustGitStack(t, work, "-c", "user.name=test", "-c", "user.email=test@localhost", "commit", "-qm", "sibling projects")
	mustGitStack(t, work, "push", "-q", "origin", "HEAD")
	sha := strings.TrimSpace(mustGitStack(t, work, "rev-parse", "HEAD"))
	f.editSeed(t, func(m *stackManifest) {
		m.Repos["app"] = &stackRepo{Upstream: f.g.spec("acme/app"), Fork: f.g.spec("acme/app"), PublishPrefix: prefix}
		m.LastSync["app"] = sha
	})
	f.r.Pack.Provenance["app"] = stackEdgeProvenance{UpstreamCommit: sha}
	return sha
}

func (f *edgeCITestFixture) editTrack(t *testing.T, files map[string]string) {
	t.Helper()
	work := t.TempDir()
	mustGitStack(t, work, "clone", "-q", "--branch", "stack/integration", f.g.path("you/lib"), work)
	for path, body := range files {
		path = filepath.Join(work, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mustGitStack(t, work, "add", ".")
	// Integration snapshots have one upstream parent; amend preserves it.
	mustGitStack(t, work, "-c", "user.name=test", "-c", "user.email=test@localhost", "commit", "--amend", "--no-edit", "-q")
	mustGitStack(t, work, "push", "-q", "--force", "origin", "HEAD")
	f.track = strings.TrimSpace(mustGitStack(t, work, "rev-parse", "HEAD"))
	f.r.Pack.Commit = f.track
	p := f.r.Pack.Provenance["lib"]
	p.TrackCommit = f.track
	f.r.Pack.Provenance["lib"] = p
}

func TestStackEdgeCIPublishTrustedNumber(t *testing.T) {
	f := newEdgeCITestFixture(t)
	// A compromised builder can produce a perfectly consistent report and
	// package that would otherwise reserve the series' largest run number.
	f.r.Number = 2147483647
	f.r.Pack.Version = "1.2.4-edge.2147483647"
	f.r.Pack.Expected.Version = f.r.Pack.Version
	f.packageFile(t, "You.Lib", f.r.Pack.Version, "", "Lib")
	if _, err := f.run(t); err == nil || !strings.Contains(err.Error(), "number differs from trusted --number") {
		t.Fatalf("inflated number accepted: %v", err)
	}
	if len(f.f.pushes) != 0 {
		t.Fatal("inflated number reached push")
	}
}

func TestStackEdgeCIPublishNumberFlag(t *testing.T) {
	f := newEdgeCITestFixture(t)
	if err := stackEdgeCIWriteReport(f.o.Publish, f.r); err != nil {
		t.Fatal(err)
	}
	args := []string{"--seed", f.o.Seed, "--member", "lib", "--publish", f.o.Publish, "-o", f.o.Out, "--allowed-hosts", f.o.Allowed}
	c := newStackEdgeCICmd()
	c.SilenceUsage = true
	c.SilenceErrors = true
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	if err := c.Execute(); err == nil || !strings.Contains(err.Error(), "require an explicit --number") {
		t.Fatal(err)
	}
}

func TestStackEdgeCIPublishAllowedSubset(t *testing.T) {
	for _, name := range []string{"implied-test-omitted", "unmapped-implied-test-omitted", "literal-nonpackable", "literal-test", "outside-id", "empty"} {
		t.Run(name, func(t *testing.T) {
			f := newEdgeCITestFixture(t)
			want := ""
			switch name {
			case "implied-test-omitted", "unmapped-implied-test-omitted":
				// The test SDK determines packability, with neither literal flag.
				// Its unrelated version must not influence the selected packages.
				f.editTrack(t, map[string]string{"Tests/Tests.csproj": `<Project Sdk="MSTest.Sdk/3.6.0"><PropertyGroup><TargetFramework>net10.0</TargetFramework><Version>99.0.0</Version></PropertyGroup></Project>`})
				if name == "unmapped-implied-test-omitted" {
					f.editSeed(t, func(m *stackManifest) {
						m.Repos["lib"].PublishPrefix = ""
						m.Repos["lib"].PublishesAs = map[string]string{"Lib": "You.Lib"}
					})
				}
			case "literal-nonpackable", "literal-test":
				flag := "<IsPackable>false</IsPackable>"
				if name == "literal-test" {
					flag = "<IsTestProject>true</IsTestProject>"
				}
				f.editTrack(t, map[string]string{"Tests/Tests.csproj": "<Project><PropertyGroup><Version>1.2.3</Version>" + flag + "</PropertyGroup></Project>"})
				f.r.Pack.Expected.Packages = append(f.r.Pack.Expected.Packages, stackEdgeExpectedPackage{OriginalID: "Tests", ID: "You.Tests", AssemblyBefore: "Tests", AssemblyAfter: "Tests"})
				want = "untrusted or duplicate republished id"
			case "outside-id":
				// Keep a legitimate package alongside the forged one, so removing
				// the subset guard really would publish the extra id.
				f.r.Pack.Expected.Packages = append(f.r.Pack.Expected.Packages, stackEdgeExpectedPackage{OriginalID: "Extra", ID: "You.Extra", AssemblyBefore: "Extra", AssemblyAfter: "Extra"})
				f.r.Pack.Expected.MemberIDs["Extra"] = "You.Extra"
				edgeTestNupkg(t, f.o.Out, "extra.nupkg", "You.Extra", f.r.Pack.Version, "", "Extra")
				stat, err := os.Stat(filepath.Join(f.o.Out, "extra.nupkg"))
				if err != nil {
					t.Fatal(err)
				}
				f.r.Pack.Artifacts = append([]stackEdgeVerifiedPackage{{File: "extra.nupkg", ID: "You.Extra", Size: uint64(stat.Size())}}, f.r.Pack.Artifacts...)
				f.r.SHA256, err = stackEdgeCIHashes(f.o.Out, f.r.Pack.Artifacts)
				if err != nil {
					t.Fatal(err)
				}
				want = "untrusted or duplicate republished id"
			case "empty":
				f.r.Pack.Expected.Packages = nil
				want = "non-empty subset"
			}
			_, err := f.run(t)
			if want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if len(f.f.pushes) != 1 {
					t.Fatal("subset was not published")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("want %q, got %v", want, err)
			}
			if len(f.f.pushes) != 0 {
				t.Fatal("untrusted id reached push")
			}
		})
	}
}

func TestStackEdgeCIPublishNonDotnetSibling(t *testing.T) {
	f := newEdgeCITestFixture(t)
	f.addSibling(t, map[string]string{"package.json": `{"name":"app-package","version":"1.0.0"}`}, "")
	// Builder registry discovery includes npm; the publisher's authority is
	// NuGet ids only and must not depend on this claimed list.
	f.r.Pack.Expected.OtherMemberIDs = []string{"app-package"}
	if _, err := f.run(t); err != nil {
		t.Fatal(err)
	}
}

func TestStackEdgeCIPublishSibling(t *testing.T) {
	for _, name := range []string{"cursor-fetch", "wrong-provenance", "missing-provenance", "unavailable-fetch", "original-dependency", "republished-dependency"} {
		t.Run(name, func(t *testing.T) {
			f := newEdgeCITestFixture(t)
			f.addSibling(t, map[string]string{"Sibling.csproj": `<Project><PropertyGroup><Version>1.2.3</Version></PropertyGroup></Project>`}, "You.")
			want := ""
			switch name {
			case "cursor-fetch":
				// Advance upstream: the report still reconstructs the seed cursor.
				f.g.commit(t, "acme/app", "Sibling.csproj", `<Project><PropertyGroup><PackageId>Moved.Sibling</PackageId><Version>2.0.0</Version></PropertyGroup></Project>`, "advance sibling")
				f.r.Pack.Expected.OtherMemberIDs = []string{"builder-list-is-not-authority"}
			case "wrong-provenance":
				f.r.Pack.Provenance["app"] = stackEdgeProvenance{UpstreamCommit: f.parent}
				want = "sibling provenance differs"
			case "missing-provenance":
				delete(f.r.Pack.Provenance, "app")
				want = "provenance differs"
			case "unavailable-fetch":
				f.editSeed(t, func(m *stackManifest) { m.Repos["app"].Upstream = f.g.spec("acme/unavailable") })
				want = "cannot read trusted member ref"
			case "original-dependency", "republished-dependency":
				id := "Sibling"
				if name == "republished-dependency" {
					id = "You.Sibling"
				}
				f.r.Pack.Expected.Packages[0].Dependencies = []stackEdgeDependency{{ID: id, Version: "1.2.3"}}
				// Omit the sibling from the claimed list; independent discovery
				// must still refuse both its original and publishing ids.
				f.r.Pack.Expected.OtherMemberIDs = nil
				f.packageFile(t, "You.Lib", f.r.Pack.Version, fmt.Sprintf(`<dependency id="%s" version="1.2.3"/>`, id), "Lib")
				want = "another member's package"
			}
			_, err := f.run(t)
			if want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("want %q, got %v", want, err)
			}
			if len(f.f.pushes) != 0 {
				t.Fatal("invalid sibling reached push")
			}
		})
	}
}
