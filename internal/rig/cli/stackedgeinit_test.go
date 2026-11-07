package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/huh"
)

type edgeSetupAPI struct {
	server                                                    *httptest.Server
	exists, fork, secret, stale, disableStuck                 bool
	environment, customBranches, environmentStuck, repoSecret bool
	policies                                                  []stackEdgeBranchPolicy
	workflows                                                 []stackEdgeWorkflow
	failures, sets, writes, reads                             int
	updated                                                   time.Time
	requests                                                  []string
	commands                                                  [][]string
	inputs                                                    []string
	value                                                     string
}

func edgeSetupFixture(t *testing.T) (*stackEdgeSetup, *edgeSetupAPI, *bytes.Buffer) {
	t.Helper()
	f := &edgeSetupAPI{exists: true, fork: true, value: "fixture-private-push-value", updated: time.Date(2026, 11, 6, 12, 0, 1, 0, time.UTC), workflows: []stackEdgeWorkflow{
		{ID: 1, Path: ".github/workflows/upstream.yml", State: "active"},
		{ID: 2, Path: ".github/workflows/stack-edge.yml", State: "active"},
		{ID: 3, Path: ".github/workflows/old.yml", State: "disabled_manually"},
	}}
	f.environment, f.customBranches = true, true
	f.policies = []stackEdgeBranchPolicy{{ID: 10, Name: "main", Type: "branch"}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
		if r.Method == "GET" {
			f.reads++
		} else {
			f.writes++
		}
		write := func(v any) {
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/publisher/edge":
			if !f.exists {
				http.NotFound(w, r)
				return
			}
			write(map[string]any{"fork": f.fork, "default_branch": "main"})
		case r.Method == "GET" && r.URL.Path == "/user":
			write(map[string]string{"login": "publisher"})
		case r.Method == "POST" && r.URL.Path == "/user/repos":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["name"] != "edge" || body["private"] != true {
				t.Errorf("create body: %v", body)
			}
			f.exists = true
			f.fork = false
			w.WriteHeader(201)
		case r.Method == "GET" && r.URL.Path == "/repos/publisher/edge/actions/workflows":
			write(map[string]any{"workflows": f.workflows})
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/disable"):
			if !f.disableStuck {
				f.workflows[0].State = "disabled_manually"
			}
			w.WriteHeader(204)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/actions/workflows/1"):
			write(f.workflows[0])
		case r.URL.Path == "/repos/publisher/edge/environments/edge-publish":
			if r.Method == "PUT" {
				var body struct {
					Policy struct {
						Custom    bool `json:"custom_branch_policies"`
						Protected bool `json:"protected_branches"`
					} `json:"deployment_branch_policy"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if !body.Policy.Custom || body.Policy.Protected {
					t.Error("environment not restricted to custom branches")
				}
				if !f.environmentStuck {
					f.environment, f.customBranches = true, true
				}
			}
			if !f.environment {
				http.NotFound(w, r)
				return
			}
			write(map[string]any{"name": "edge-publish", "deployment_branch_policy": map[string]bool{"protected_branches": false, "custom_branch_policies": f.customBranches}})
		case strings.Contains(r.URL.Path, "/environments/edge-publish/deployment-branch-policies"):
			switch r.Method {
			case "GET":
				write(map[string]any{"branch_policies": f.policies})
			case "POST":
				var policy stackEdgeBranchPolicy
				if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
					t.Error(err)
				}
				policy.ID = 10
				if !f.environmentStuck {
					f.policies = append(f.policies, policy)
				}
				write(policy)
			case "DELETE":
				for i, p := range f.policies {
					if strings.HasSuffix(r.URL.Path, fmt.Sprintf("/%d", p.ID)) {
						f.policies = append(f.policies[:i], f.policies[i+1:]...)
						break
					}
				}
				w.WriteHeader(204)
			default:
				t.Errorf("unexpected policy method %s", r.Method)
			}
		case r.URL.Path == "/repos/publisher/edge/actions/secrets/EDGE_FEED_KEY":
			if r.Method != "GET" {
				t.Errorf("repository secret mutated: %s", r.Method)
			}
			if !f.repoSecret {
				http.NotFound(w, r)
				return
			}
			write(map[string]any{"name": "EDGE_FEED_KEY", "updated_at": f.updated})
		case strings.HasSuffix(r.URL.Path, "/environments/edge-publish/secrets/EDGE_FEED_KEY"):
			if r.Method == "GET" {
				if !f.secret {
					http.NotFound(w, r)
					return
				}
				at := f.updated
				if f.stale {
					at = at.Add(-time.Hour)
				}
				write(map[string]any{"name": "EDGE_FEED_KEY", "updated_at": at})
				return
			}
			if r.Method != "PUT" {
				t.Errorf("unexpected secret method %s", r.Method)
			}
			f.sets++
			input, _ := io.ReadAll(r.Body)
			if string(input) != f.value {
				t.Errorf("secret stdin mismatch")
			}
			if f.sets <= f.failures {
				w.WriteHeader(502)
				fmt.Fprint(w, "fixture upstream failure")
				return
			}
			f.secret = true
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	m := edgeTestManifest()
	m.Edge["lib"].PublishFrom = "github.com/publisher/edge"
	m.Edge["lib"].PushKeySecret = "EDGE_FEED_KEY"
	out := &bytes.Buffer{}
	s := &stackEdgeSetup{ctx: context.Background(), root: t.TempDir(), m: m, out: out, in: strings.NewReader(f.value), api: stackDepsAPI{base: f.server.URL, client: f.server.Client()},
		tty: func() bool { return true }, prompt: func(c *huh.Confirm) error { return c.RunAccessible(out, strings.NewReader("y\n")) },
		now: func() time.Time { return f.updated.Add(-time.Second) }, sleep: func(context.Context, time.Duration) error { return nil },
		pack: func(context.Context, io.Writer, string, string) error { return nil },
	}
	s.run = func(ctx context.Context, name string, args []string, in io.Reader) ([]byte, error) {
		command := append([]string{name}, args...)
		f.commands = append(f.commands, command)
		input, err := io.ReadAll(in)
		if err != nil {
			t.Fatal(err)
		}
		f.inputs = append(f.inputs, string(input))
		want := []string{"gh", "secret", "set", "EDGE_FEED_KEY", "--repo", "github.com/publisher/edge", "--env", "edge-publish"}
		if !reflect.DeepEqual(command, want) {
			t.Errorf("command=%v want %v", command, want)
		}
		req, err := http.NewRequestWithContext(ctx, "PUT", f.server.URL+"/repos/publisher/edge/environments/edge-publish/secrets/EDGE_FEED_KEY", bytes.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := f.server.Client().Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 204 {
			return []byte(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, f.value)), errors.New(f.value)
		}
		return []byte(f.value), nil // even successful subprocess output must not escape
	}
	return s, f, out
}

func assertEdgeSetupNoSecret(t *testing.T, s *stackEdgeSetup, f *edgeSetupAPI, out *bytes.Buffer, err error) {
	t.Helper()
	text := out.String()
	if err != nil {
		text += err.Error()
	}
	if strings.Contains(text, f.value) {
		t.Fatal("secret leaked into output or errors")
	}
	for _, cmd := range f.commands {
		if strings.Contains(strings.Join(cmd, " "), f.value) {
			t.Fatal("secret leaked into argv")
		}
	}
}

func TestStackEdgeInitSecretRetries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failures  int
		stale     bool
		wantCalls int
		wantError string
	}{
		{name: "502-then-success", failures: 1, wantCalls: 2},
		{name: "three-502s", failures: 3, wantCalls: 3, wantError: "gh secret set failed"},
		{name: "stale-metadata", stale: true, wantCalls: 1, wantError: "updated_at predates"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, out := edgeSetupFixture(t)
			f.failures = tc.failures
			f.stale = tc.stale
			var sleeps []time.Duration
			s.sleep = func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }
			err := s.repairSecret("lib")
			if tc.wantError == "" && err != nil || tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("err=%v want %q", err, tc.wantError)
			}
			if f.sets != tc.wantCalls || len(sleeps) != tc.wantCalls-1 {
				t.Fatalf("sets=%d sleeps=%v", f.sets, sleeps)
			}
			if len(sleeps) > 0 && sleeps[0] != time.Second || len(sleeps) > 1 && sleeps[1] != 2*time.Second {
				t.Fatalf("backoff=%v", sleeps)
			}
			assertEdgeSetupNoSecret(t, s, f, out, err)
			if err == nil {
				before := f.sets
				s.in = failEdgeSetupReader{t}
				s.authRef = "env:MUST_NOT_RESOLVE"
				if err := s.repairSecret("lib"); err != nil || f.sets != before {
					t.Fatalf("second run sets secret: %v", err)
				}
			}
		})
	}
}

type failEdgeSetupReader struct{ t *testing.T }

func (r failEdgeSetupReader) Read([]byte) (int, error) {
	r.t.Error("read an already-satisfied secret")
	return 0, io.EOF
}

func TestStackEdgeInitNonTTYOutwardFacts(t *testing.T) {
	for _, action := range []string{"track", "create-repo", "disable-workflow", "secret", "wire"} {
		t.Run(action, func(t *testing.T) {
			s, f, out := edgeSetupFixture(t)
			s.tty = func() bool { return false }
			s.prompt = func(*huh.Confirm) error { t.Fatal("prompt reached without terminal"); return nil }
			var err error
			switch action {
			case "track":
				err = s.repairTrack("lib")
			case "create-repo":
				f.exists = false
				err = s.repairPublishingRepo("lib")
			case "disable-workflow":
				err = s.repairPublishingRepo("lib")
			case "secret":
				s.in = failEdgeSetupReader{t}
				err = s.repairSecret("lib")
			case "wire":
				err = s.repairWiring("lib")
			}
			if err == nil || !strings.Contains(err.Error(), "would ") || !strings.Contains(err.Error(), "stdin and stdout on a terminal") {
				t.Fatalf("missing non-TTY refusal: %v", err)
			}
			if f.writes != 0 || len(f.commands) != 0 {
				t.Fatalf("non-TTY made writes: %v", f.requests)
			}
			assertEdgeSetupNoSecret(t, s, f, out, err)
		})
	}
}

func TestStackEdgeInitWorkflowReadbackAndIdempotence(t *testing.T) {
	s, f, out := edgeSetupFixture(t)
	if err := s.checkPublishingRepo("lib"); err == nil {
		t.Fatal("active upstream workflow passed")
	}
	if err := s.repairPublishingRepo("lib"); err != nil {
		t.Fatal(err)
	}
	if err := s.checkPublishingRepo("lib"); err != nil {
		t.Fatal(err)
	}
	if f.writes != 1 || f.workflows[1].State != "active" {
		t.Fatalf("writes=%d workflows=%v", f.writes, f.workflows)
	}
	before := out.String()
	s.prompt = func(*huh.Confirm) error { t.Fatal("second run prompted"); return nil }
	if err := s.repairPublishingRepo("lib"); err != nil || f.writes != 1 || out.String() != before {
		t.Fatalf("repeat changed state: %v", err)
	}
	if !slicesContainEdgeRequest(f.requests, "GET /repos/publisher/edge/actions/workflows/1") {
		t.Fatal("disable did not read back workflow")
	}
}

func slicesContainEdgeRequest(requests []string, want string) bool {
	for _, r := range requests {
		if r == want {
			return true
		}
	}
	return false
}

func TestStackEdgeInitWorkflowRejectsUnconfirmedDisable(t *testing.T) {
	s, f, _ := edgeSetupFixture(t)
	f.disableStuck = true
	if err := s.repairPublishingRepo("lib"); err == nil || !strings.Contains(err.Error(), "readback") {
		t.Fatalf("accepted failed disable: %v", err)
	}
}

func TestStackEdgeInitCreatesEmptyPublishingRepo(t *testing.T) {
	s, f, _ := edgeSetupFixture(t)
	f.exists = false
	if err := s.repairPublishingRepo("lib"); err != nil {
		t.Fatal(err)
	}
	if f.writes != 1 || !f.exists || f.fork {
		t.Fatalf("create did not make empty repo: %v", f.requests)
	}
	if err := s.checkPublishingRepo("lib"); err != nil {
		t.Fatal(err)
	}
	if !slicesContainEdgeRequest(f.requests, "POST /user/repos") {
		t.Fatal("missing repo creation")
	}
}

func TestStackEdgeInitDeclinedPrompts(t *testing.T) {
	for _, action := range []string{"track", "create-repo", "disable-workflow", "secret", "wire"} {
		t.Run(action, func(t *testing.T) {
			s, f, out := edgeSetupFixture(t)
			s.prompt = func(c *huh.Confirm) error { return c.RunAccessible(out, strings.NewReader("n\n")) }
			var err error
			switch action {
			case "track":
				err = s.repairTrack("lib")
			case "create-repo":
				f.exists = false
				err = s.repairPublishingRepo("lib")
			case "disable-workflow":
				err = s.repairPublishingRepo("lib")
			case "secret":
				err = s.repairSecret("lib")
			case "wire":
				err = s.repairWiring("lib")
			}
			if err == nil || !strings.Contains(err.Error(), "cancelled") {
				t.Fatalf("decline accepted: %v", err)
			}
			if f.writes != 0 || len(f.commands) != 0 {
				t.Fatal("decline wrote outward")
			}
		})
	}
}

func TestStackEdgeInitSecretAuthAndErrorsArePrivate(t *testing.T) {
	t.Run("auth-ref", func(t *testing.T) {
		s, f, out := edgeSetupFixture(t)
		t.Setenv("EDGE_SETUP_TEST_TOKEN", f.value)
		s.authRef = "env:EDGE_SETUP_TEST_TOKEN"
		s.in = failEdgeSetupReader{t}
		err := s.repairSecret("lib")
		if err != nil {
			t.Fatal(err)
		}
		assertEdgeSetupNoSecret(t, s, f, out, err)
	})
	t.Run("auth-error", func(t *testing.T) {
		s, f, out := edgeSetupFixture(t)
		s.authRef = "unsupported:" + f.value
		err := s.repairSecret("lib")
		if err == nil {
			t.Fatal("bad auth accepted")
		}
		assertEdgeSetupNoSecret(t, s, f, out, err)
	})
	t.Run("non-5xx", func(t *testing.T) {
		s, f, out := edgeSetupFixture(t)
		s.run = func(context.Context, string, []string, io.Reader) ([]byte, error) {
			return []byte("HTTP 403: " + f.value), errors.New(f.value)
		}
		err := s.repairSecret("lib")
		if err == nil {
			t.Fatal("failed set accepted")
		}
		assertEdgeSetupNoSecret(t, s, f, out, err)
	})
}

func TestStackEdgeInitSecretTimestampResolution(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		fraction, metadataOffset time.Duration
		wantError                bool
	}{
		{name: "same-second", fraction: 700 * time.Millisecond},
		{name: "exact-second"},
		{name: "previous-second", fraction: 700 * time.Millisecond, metadataOffset: -time.Second, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, out := edgeSetupFixture(t)
			started := f.updated.Add(tc.fraction)
			f.updated = started.Truncate(time.Second).Add(tc.metadataOffset)
			s.now = func() time.Time { return started }
			err := s.repairSecret("lib")
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "updated_at predates") {
					t.Fatalf("metadata from an earlier second accepted: %v", err)
				}
			} else if err != nil {
				t.Fatalf("metadata from the attempt's second refused: %v", err)
			}
			if f.sets != 1 {
				t.Fatalf("sets = %d, want 1", f.sets)
			}
			assertEdgeSetupNoSecret(t, s, f, out, err)
		})
	}
}

func TestStackEdgeInitPublicHelp(t *testing.T) {
	edge := newStackEdgeCmd()
	init, _, err := edge.Find([]string{"init"})
	if err != nil || init == edge || init.Hidden || init.ValidArgsFunction == nil {
		t.Fatal("init must be public with member completion")
	}
	if init.Flags().Lookup("yes") != nil {
		t.Fatal("confirmation bypass flag")
	}
	doctor := newStackDoctorCmd()
	if doctor.Flags().Lookup("edge").Hidden {
		t.Fatal("doctor edge flag hidden")
	}
	var out bytes.Buffer
	edge.SetOut(&out)
	if err := edge.Help(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "rig stack edge init") {
		t.Fatal("init not advertised")
	}
	columns := regexp.MustCompile(`^  rig stack edge .*? {2,}`)
	column := -1
	for _, line := range strings.Split(edge.Long, "\n") {
		if !strings.HasPrefix(line, "  rig stack edge ") {
			continue
		}
		prefix := columns.FindString(line)
		if prefix == "" || column >= 0 && len(prefix) != column {
			t.Fatalf("misaligned edge help: %s", line)
		}
		column = len(prefix)
	}
	out.Reset()
	doctor.SetOut(&out)
	if err := doctor.Help(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--edge") {
		t.Fatal("doctor flag not advertised")
	}
	root := inTempStackspace(t, `{"repos":{"lib":{"upstream":"github.com/acme/Lib","fork":"github.com/you/Lib"},"app":{"upstream":"github.com/acme/App","fork":"github.com/you/App"}}}`)
	if _, _, err := loadStackManifest(root); err != nil {
		t.Fatal(err)
	}
	init.SetContext(t.Context())
	names, _ := init.ValidArgsFunction(init, nil, "")
	if !reflect.DeepEqual(names, []string{"app", "lib"}) {
		t.Fatalf("member completion: %v", names)
	}
}

func TestStackEdgeInitManifestEdits(t *testing.T) {
	s, _, out := edgeSetupFixture(t)
	s.m.Edge = nil
	s.m.Repos["lib"].TrackBranch = ""
	s.m.Repos["lib"].PublishPrefix = ""
	data, _ := json.Marshal(s.m)
	root := inTempStackspace(t, string(data))
	s.root = root
	before, err := os.ReadFile(filepath.Join(root, "rig.stack.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	init := newStackEdgeInitCmd()
	init.SetOut(out)
	init.SetErr(out)
	init.SetArgs([]string{"lib", "-n"})
	if err := init.Execute(); err == nil {
		t.Fatal("missing manifest facts accepted")
	}
	for _, want := range []string{`"edge"`, "repos.lib.trackBranch", "repos.lib.publishPrefix", "(b)", "(c)", "(d)", "(f)", "(g)", "(h)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
	after, _ := os.ReadFile(filepath.Join(root, "rig.stack.jsonc"))
	if !bytes.Equal(before, after) {
		t.Fatal("manifest was edited")
	}
}

func TestStackEdgeInitSetupLoaderKeepsValidation(t *testing.T) {
	m := edgeTestManifest()
	m.Repos["lib"].TrackBranch = ""
	m.Repos["lib"].PublishPrefix = ""
	data, _ := json.Marshal(m)
	root := inTempStackspace(t, string(data))
	if _, _, err := loadStackManifest(root); err == nil {
		t.Fatal("ordinary manifest validation weakened")
	}
	if _, _, err := loadStackManifestForEdgeSetup(root, true); err != nil {
		t.Fatal(err)
	}
	m.Edge["lib"].Feed = ""
	data, _ = json.Marshal(m)
	if err := os.WriteFile(filepath.Join(root, "rig.stack.jsonc"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadStackManifestForEdgeSetup(root, true); err == nil {
		t.Fatal("setup ignored unrelated invalid edge config")
	}
}

func TestStackEdgeInitDefaultHomeChecksWithoutCreating(t *testing.T) {
	s, f, out := edgeSetupFixture(t)
	s.m.Edge["lib"].PublishFrom = ""
	mustGitStack(t, s.root, "init", "-q")
	mustGitStack(t, s.root, "config", "rig.stackHomeURL", "https://github.com/publisher/edge.git")
	s.prompt = func(*huh.Confirm) error { t.Fatal("default-home setup must use the seed flow"); return nil }
	// Even a forked default home is checked only for existence, without disabling
	// its workflows; workflow cleanup belongs to an explicit publishFrom role.
	if err := s.checkPublishingRepo("lib"); err != nil {
		t.Fatal(err)
	}
	if err := s.repairPublishingRepo("lib"); err != nil {
		t.Fatal(err)
	}
	if f.reads != 2 {
		t.Fatalf("default home existence reads = %d, want 2", f.reads)
	}
	for _, request := range f.requests {
		if strings.Contains(request, "/actions/workflows") {
			t.Fatalf("default home workflow cleanup: %s", request)
		}
	}
	f.exists = false
	if err := s.checkPublishingRepo("lib"); err == nil || !strings.Contains(err.Error(), "configure the manifest home: `rig stack seed --publish`") {
		t.Fatalf("missing home accepted without remedy: %v", err)
	}
	if err := s.repairPublishingRepo("lib"); err == nil || !strings.Contains(err.Error(), "rig stack seed --publish") {
		t.Fatalf("default-home repair missing seed remedy: %v", err)
	}
	if f.writes != 0 || f.exists {
		t.Fatal("edge created the missing default home")
	}
	assertEdgeSetupNoSecret(t, s, f, out, nil)
}

type edgeSetupReaderFunc func([]byte) (int, error)

func (read edgeSetupReaderFunc) Read(p []byte) (int, error) { return read(p) }

func TestStackEdgeInitSecretAppearsBeforeSet(t *testing.T) {
	for _, phase := range []string{"confirmation", "credential-input", "retry-delay"} {
		t.Run(phase, func(t *testing.T) {
			s, f, out := edgeSetupFixture(t)
			wantSets := 0
			switch phase {
			case "confirmation":
				prompt := s.prompt
				s.prompt = func(c *huh.Confirm) error { err := prompt(c); f.secret = true; return err }
				s.in = failEdgeSetupReader{t}
			case "credential-input":
				input := strings.NewReader(f.value)
				s.in = edgeSetupReaderFunc(func(p []byte) (int, error) { f.secret = true; return input.Read(p) })
			case "retry-delay":
				f.failures = 1
				s.sleep = func(context.Context, time.Duration) error { f.secret = true; return nil }
				wantSets = 1
			}
			err := s.repairSecret("lib")
			if err != nil {
				t.Fatal(err)
			}
			if f.sets != wantSets || f.writes != wantSets || len(f.commands) != wantSets {
				t.Fatalf("overwrote concurrent secret: sets=%d writes=%d commands=%d; want %d", f.sets, f.writes, len(f.commands), wantSets)
			}
			if !strings.Contains(out.String(), "Another process created push secret") || !strings.Contains(out.String(), "leaving it unchanged") {
				t.Fatalf("missing concurrent-creation report: %s", out)
			}
			assertEdgeSetupNoSecret(t, s, f, out, err)
		})
	}
}
