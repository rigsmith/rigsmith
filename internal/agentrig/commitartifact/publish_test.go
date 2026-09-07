package commitartifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

type testTransport struct {
	repo            gitRepo
	pushes, fetches int
	beforePush      func()
	pushErr         error
	noPush          bool
	fetchErrorAt    int
}

func (t *testTransport) Fetch(ctx context.Context, dir, ref string) (string, error) {
	t.fetches++
	if t.fetches == t.fetchErrorAt {
		return "", errors.New("offline")
	}
	sha, err := t.repo.run(ctx, nil, "for-each-ref", "--format=%(objectname)", "refs/heads/main")
	sha = strings.TrimSpace(sha)
	if err != nil || sha == "" {
		return sha, err
	}
	err = (gitRepo{dir: dir}).importRef(ctx, t.repo.dir, "refs/heads/main", ref, sha)
	return sha, err
}
func (t *testTransport) Push(ctx context.Context, dir, commit string) error {
	t.pushes++
	if t.beforePush != nil {
		f := t.beforePush
		t.beforePush = nil
		f()
	}
	if !t.noPush {
		if _, err := (gitRepo{dir: dir}).run(ctx, nil, "push", "--", t.repo.dir, commit+":refs/heads/main"); err != nil {
			return err
		}
	}
	return t.pushErr
}

func mustRun(t *testing.T, repo gitRepo, input string, args ...string) string {
	t.Helper()
	s, err := repo.run(t.Context(), strings.NewReader(input), args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(s)
}
func putPublicationFile(t *testing.T, root, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func newPublicationCommit(t *testing.T, repo gitRepo, parent, name, value string) string {
	t.Helper()
	// Fixture histories share the seed's file and add one independent change.
	root := t.TempDir()
	putPublicationFile(t, root, "history.txt", "second history entry")
	putPublicationFile(t, root, name, value)
	tree, err := repo.writeTree(t.Context(), root, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	return mustRun(t, repo, "fixture\n", "commit-tree", tree, "-p", parent)
}
func publicationFixture(t *testing.T, format string) (PublishRequest, *testTransport, gitRepo, string) {
	t.Helper()
	seed, parent := seedRepository(t, format)
	store := artifact.Store{Dir: filepath.Join(t.TempDir(), "captures")}
	seedRef, err := RetainSeed(t.Context(), store, seed.dir, parent)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := store.BuildWithMetadata(t.Context(), artifact.Key([]byte("publication capture")), func(ctx context.Context, tree string, meta *artifact.Metadata) error {
		meta.BaseReference, meta.SeedReference = parent, seedRef
		putPublicationFile(t, tree, "history.txt", "second history entry")
		putPublicationFile(t, tree, "captured", "sealed\r\n\x00bytes")
		putPublicationFile(t, tree, ".gitattributes", "* text filter=hostile\ncaptured export-ignore\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r := Request{Captures: store, Commits: artifact.Store{Dir: filepath.Join(t.TempDir(), "commits")}, CaptureRef: capture,
		PolicyID: "publication fixture", Message: "capture", AuthorName: "fixture", AuthorEmail: "fixture@example.com", Time: time.Unix(1700000000, 0),
		Prepare: func(context.Context, string) error { return nil }, Audit: func(context.Context, string) error { return nil }}
	ref, err := Build(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "remote"), parent)
	if err != nil {
		t.Fatal(err)
	}
	remote.identity = seed.identity
	tr := &testTransport{repo: remote}
	return PublishRequest{Commits: r.Commits, CommitRef: ref, CaptureRef: capture, Remote: tr,
		Message: "merge retained capture", AuthorName: r.AuthorName, AuthorEmail: r.AuthorEmail, Time: r.Time, Attempts: 3,
		Validate: r.Audit, Audit: r.Audit}, tr, seed, parent
}

func TestPublicationResolverKeepsNewerLocalPrecedence(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			r, remote, local, parent := publicationFixture(t, format)
			r.LocalDir = local.dir
			r.LocalCommit = newPublicationCommit(t, local, parent, "captured", "newer local value")
			if err := remote.repo.importRef(t.Context(), local.dir, parent, "refs/heads/main", parent); err != nil {
				t.Fatal(err)
			}
			remoteHead := newPublicationCommit(t, remote.repo, parent, "captured", "remote value")
			mustRun(t, remote.repo, "", "update-ref", "refs/heads/main", remoteHead)
			calls := 0
			r.Resolve = func(_ context.Context, path string, base, ours, theirs []byte) ([]byte, error) {
				calls++
				wantTheirs := "sealed\r\n\x00bytes"
				if calls == 2 {
					wantTheirs = "remote value"
				}
				if path != "captured" || string(ours) != "newer local value" || string(theirs) != wantTheirs {
					t.Errorf("merge %d precedence: %s ours=%q theirs=%q", calls, path, ours, theirs)
				}
				// Claude's shared manifest keys use this same ours-wins rule.
				return ours, nil
			}
			result, err := Publish(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 || mustRun(t, remote.repo, "", "show", result.RemoteCommit+":captured") != "newer local value" {
				t.Fatal("newer local value lost", calls)
			}
			if again, err := Publish(t.Context(), r); err != nil || again != result || remote.pushes != 1 || calls != 2 {
				t.Fatal("replay rebuilt or republished", again, err, calls, remote.pushes)
			}
		})
	}
}

func TestPublicationMergesNewerHistoriesAndReplays(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			r, tr, local, parent := publicationFixture(t, format)
			localHead := newPublicationCommit(t, local, parent, "local", "newer synchronous bytes")
			mustRun(t, local, "", "update-ref", "refs/heads/main", localHead)
			r.LocalDir, r.LocalCommit = local.dir, localHead
			if err := tr.repo.importRef(t.Context(), local.dir, parent, "refs/heads/main", parent); err != nil {
				t.Fatal(err)
			}
			remoteHead := newPublicationCommit(t, tr.repo, parent, "remote", "newer remote bytes")
			mustRun(t, tr.repo, "", "update-ref", "refs/heads/main", remoteHead)
			// A canonical index/config sentinel must remain byte-identical.
			putPublicationFile(t, local.dir, "index", "pending canonical index")
			configBefore, _ := os.ReadFile(filepath.Join(local.dir, "config"))
			result, err := Publish(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			for path, want := range map[string]string{"captured": "sealed\r\n\x00bytes", "local": "newer synchronous bytes", "remote": "newer remote bytes"} {
				got, err := tr.repo.run(t.Context(), nil, "show", result.RemoteCommit+":"+path)
				if err != nil || got != want {
					t.Fatalf("%s = %q: %v", path, got, err)
				}
			}
			for _, sha := range []string{localHead, remoteHead, result.CaptureCommit} {
				if yes, err := tr.repo.ancestor(t.Context(), sha, result.RemoteCommit); err != nil || !yes {
					t.Fatalf("lost ancestry %s: %v", sha, err)
				}
			}
			if head := mustRun(t, local, "", "rev-parse", "HEAD"); head != localHead {
				t.Fatal("changed local HEAD")
			}
			index, _ := os.ReadFile(filepath.Join(local.dir, "index"))
			configAfter, _ := os.ReadFile(filepath.Join(local.dir, "config"))
			if string(index) != "pending canonical index" || string(configBefore) != string(configAfter) {
				t.Fatal("changed canonical state")
			}
			// Simulate accepted publication followed by a lost queue marker. Recovery
			// needs neither captures nor local history, and sends no duplicate push.
			if err := os.RemoveAll(local.dir); err != nil {
				t.Fatal(err)
			}
			r.LocalDir, r.LocalCommit = "", ""
			pushes := tr.pushes
			again, err := Publish(t.Context(), r)
			if err != nil || again != result || tr.pushes != pushes {
				t.Fatalf("replay: %+v %v", again, err)
			}
			leftovers, _ := filepath.Glob(filepath.Join(r.Commits.Dir, ".publication-*"))
			if len(leftovers) != 0 {
				t.Fatal(leftovers)
			}
		})
	}
}

func TestPublicationConfirmsLostResponsesAndRejectsUnconfirmedSuccess(t *testing.T) {
	for _, mode := range []string{"lost-response", "false-success", "confirmation-offline", "initial-offline"} {
		t.Run(mode, func(t *testing.T) {
			r, tr, _, _ := publicationFixture(t, "sha1")
			switch mode {
			case "lost-response":
				tr.pushErr = errors.New("connection lost after acceptance")
			case "false-success":
				tr.noPush = true
			case "confirmation-offline":
				tr.fetchErrorAt = 2
			case "initial-offline":
				tr.fetchErrorAt = 1
			}
			result, err := Publish(t.Context(), r)
			if mode == "lost-response" {
				if err != nil || result.RemoteCommit == "" || tr.pushes != 1 {
					t.Fatalf("%+v %v", result, err)
				}
				return
			}
			if err == nil || result != (Publication{}) {
				t.Fatalf("unconfirmed publication succeeded: %+v %v", result, err)
			}
			if mode == "false-success" && (!errors.Is(err, ErrUnconfirmed) || tr.pushes != r.Attempts) {
				t.Fatalf("%d pushes: %v", tr.pushes, err)
			}
			if mode == "initial-offline" && tr.pushes != 0 {
				t.Fatal("treated offline as empty remote")
			}
			if mode == "confirmation-offline" {
				pushes := tr.pushes
				if _, err := Publish(t.Context(), r); err != nil || tr.pushes != pushes {
					t.Fatalf("retry after lost confirmation: %v", err)
				}
			}
		})
	}
}

func TestPublicationRetriesRemoteRace(t *testing.T) {
	r, tr, local, parent := publicationFixture(t, "sha1")
	if err := tr.repo.importRef(t.Context(), local.dir, parent, "refs/heads/main", parent); err != nil {
		t.Fatal(err)
	}
	raced := newPublicationCommit(t, tr.repo, parent, "race", "concurrent publication")
	tr.beforePush = func() { mustRun(t, tr.repo, "", "update-ref", "refs/heads/main", raced) }
	result, err := Publish(t.Context(), r)
	if err != nil || tr.pushes != 2 {
		t.Fatalf("race: %+v pushes=%d %v", result, tr.pushes, err)
	}
	if got := mustRun(t, tr.repo, "", "show", result.RemoteCommit+":race"); got != "concurrent publication" {
		t.Fatal(got)
	}
}

func TestPublicationBlocksConflictsAndUnsafeTrees(t *testing.T) {
	for _, mode := range []string{"conflict", "unrelated", "audit", "validate", "mutating-audit", "symlink", "gitlink", "case-collision", "capacity", "wrong-capture", "missing-artifact", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			r, tr, local, parent := publicationFixture(t, "sha1")
			switch mode {
			case "conflict":
				head := newPublicationCommit(t, local, parent, "captured", "incompatible later content")
				if err := tr.repo.importRef(t.Context(), local.dir, head, "refs/heads/main", head); err != nil {
					t.Fatal(err)
				}
			case "unrelated":
				tree := mustRun(t, local, "", "mktree")
				head := mustRun(t, local, "unrelated\n", "commit-tree", tree)
				if err := tr.repo.importRef(t.Context(), local.dir, head, "refs/heads/main", head); err != nil {
					t.Fatal(err)
				}
			case "audit":
				r.Audit = func(context.Context, string) error { return errors.New("secret found") }
			case "validate":
				r.Validate = func(context.Context, string) error { return errors.New("attributes unsafe") }
			case "mutating-audit":
				r.Audit = func(_ context.Context, root string) error { return os.Remove(filepath.Join(root, "captured")) }
			case "symlink", "gitlink", "case-collision":
				blob := mustRun(t, local, "target", "hash-object", "-w", "--stdin")
				line := "120000 blob " + blob + "\tlink\x00"
				if mode == "gitlink" {
					line = "160000 commit " + parent + "\tmodule\x00"
				}
				if mode == "case-collision" {
					line = "100644 blob " + blob + "\tName\x00" + "100644 blob " + blob + "\tname\x00"
				}
				tree := mustRun(t, local, line, "mktree", "-z")
				head := mustRun(t, local, "unsafe\n", "commit-tree", tree, "-p", parent)
				if err := tr.repo.importRef(t.Context(), local.dir, head, "refs/heads/main", head); err != nil {
					t.Fatal(err)
				}
			case "capacity":
				r.MaxTreeBytes = 1
			case "wrong-capture":
				r.CaptureRef = "other capture"
			case "missing-artifact":
				if err := os.RemoveAll(r.Commits.Dir); err != nil {
					t.Fatal(err)
				}
			}
			ctx := t.Context()
			if mode == "cancel" {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			result, err := Publish(ctx, r)
			if err == nil || result != (Publication{}) || tr.pushes != 0 {
				t.Fatalf("unsafe publication: %+v pushes=%d %v", result, tr.pushes, err)
			}
			if mode == "conflict" && !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
		})
	}
}

type transportFuncs struct {
	fetch func(context.Context, string, string) (string, error)
	push  func(context.Context, string, string) error
}

func (t transportFuncs) Fetch(ctx context.Context, dir, ref string) (string, error) {
	return t.fetch(ctx, dir, ref)
}
func (t transportFuncs) Push(ctx context.Context, dir, sha string) error {
	return t.push(ctx, dir, sha)
}

func TestPublicationValidatesFreshObservationAndCancellation(t *testing.T) {
	for _, mode := range []string{"wrong-ref", "shallow", "cancel-fetch", "cancel-audit", "unsafe-confirmation", "empty-confirmation"} {
		t.Run(mode, func(t *testing.T) {
			r, tr, local, parent := publicationFixture(t, "sha1")
			if err := tr.repo.importRef(t.Context(), local.dir, parent, "refs/heads/main", parent); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r.Attempts = 1
			r.Remote = transportFuncs{fetch: func(ctx context.Context, dir, ref string) (string, error) {
				sha, err := tr.Fetch(ctx, dir, ref)
				if err != nil {
					return "", err
				}
				switch mode {
				case "wrong-ref":
					return strings.Repeat("a", len(sha)), nil
				case "shallow":
					if err := os.WriteFile(filepath.Join(dir, "shallow"), []byte(sha+"\n"), 0600); err != nil {
						return "", err
					}
				case "cancel-fetch":
					cancel()
				case "empty-confirmation":
					if tr.fetches == 2 {
						return "", nil
					}
				}
				return sha, nil
			}, push: tr.Push}
			if mode == "cancel-audit" {
				r.Audit = func(context.Context, string) error { cancel(); return nil }
			}
			if mode == "unsafe-confirmation" {
				r.Audit = func(_ context.Context, root string) error {
					if _, err := os.Stat(filepath.Join(root, "unsafe")); err == nil {
						return errors.New("unsafe remote descendant")
					}
					return nil
				}
				r.Remote = transportFuncs{fetch: tr.Fetch, push: func(ctx context.Context, dir, sha string) error {
					if err := tr.Push(ctx, dir, sha); err != nil {
						return err
					}
					descendant := newPublicationCommit(t, tr.repo, sha, "unsafe", "must be audited")
					mustRun(t, tr.repo, "", "update-ref", "refs/heads/main", descendant)
					return nil
				}}
			}
			result, err := Publish(ctx, r)
			if err == nil || result != (Publication{}) {
				t.Fatalf("invalid observation accepted: %+v %v", result, err)
			}
			if strings.HasPrefix(mode, "cancel-") && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if mode != "unsafe-confirmation" && mode != "empty-confirmation" && tr.pushes != 0 {
				t.Fatal("pushed after invalid observation/policy")
			}
		})
	}
}

func TestPublicationCandidateDeterminism(t *testing.T) {
	r, tr, local, parent := publicationFixture(t, "sha1")
	r.LocalDir = local.dir
	r.LocalCommit = newPublicationCommit(t, local, parent, "local", "local value")
	if err := tr.repo.importRef(t.Context(), local.dir, parent, "refs/heads/main", parent); err != nil {
		t.Fatal(err)
	}
	head := newPublicationCommit(t, tr.repo, parent, "remote", "remote value")
	mustRun(t, tr.repo, "", "update-ref", "refs/heads/main", head)
	r.Attempts = 1
	var candidates []string
	r.Remote = transportFuncs{fetch: tr.Fetch, push: func(ctx context.Context, dir, sha string) error {
		candidates = append(candidates, sha)
		return errors.New("offline before push")
	}}
	// Host configuration must not transform bytes or redirect a private Git call.
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "wrong"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.autocrlf")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	for range 2 {
		if _, err := Publish(t.Context(), r); !errors.Is(err, ErrUnconfirmed) {
			t.Fatal(err)
		}
	}
	if len(candidates) != 2 || candidates[0] != candidates[1] {
		t.Fatalf("retry changed candidate: %v", candidates)
	}
}

func TestPublicationPreservesUnixArchiveModeOnEveryHost(t *testing.T) {
	r := captureRequest(t)
	var err error
	r.Captures.Dir, err = filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	r.CaptureRef = "48b409d7d20dafa4e42174d93331b99d11696cc06ffc9d7bdec60d6db03c9b5e:aa2a54080185efb3e42ad81f096b8f637fbfc1dd030d5c02bc8409196372e963"
	ref, err := Build(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "remote"), "")
	if err != nil {
		t.Fatal(err)
	}
	tr := &testTransport{repo: remote}
	result, err := Publish(t.Context(), PublishRequest{Commits: r.Commits, CommitRef: ref, CaptureRef: r.CaptureRef,
		Remote: tr, Message: "publish", AuthorName: r.AuthorName, AuthorEmail: r.AuthorEmail, Time: r.Time, Attempts: 1,
		Validate: r.Audit, Audit: r.Audit})
	if err != nil {
		t.Fatal(err)
	}
	out := mustRun(t, remote, "", "ls-tree", result.RemoteCommit, "executable")
	if !strings.HasPrefix(out, "100755 blob ") {
		t.Fatal("lost archived mode:", out)
	}
}
