package scripts

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// npm's trusted publishing binds a publisher to a repository and a WORKFLOW
// FILENAME, and the registry allows one configuration per package. So every
// workflow that publishes these packages has to be the same file — the moment
// publishing appears in a second one, that second workflow cannot mint a
// credential and needs a stored NPM_TOKEN, which is the thing being removed.
//
// This is why the npm recovery path is a job in release.yml rather than the
// separate npm-republish.yml it used to be. Nothing about that arrangement is
// self-evident from reading either file, and the failure it prevents shows up as
// a publish that quietly falls back to a token — or, once the token is gone, a
// release that cannot publish npm at all.

const publishingWorkflow = "release.yml"

// workflowsThatPublishNpm returns every workflow file that runs an npm publish.
//
// Step bodies only, with comment lines stripped: ci.yml explains that its
// snapshot does "no GitHub release, tap push, npm publish or winget submission",
// and matching that sentence would have this test reporting the workflow that
// documents not publishing as one that publishes.
func workflowsThatPublishNpm(t *testing.T) []string {
	t.Helper()
	dir := "../.github/workflows"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("%s: %v — fix this test rather than deleting it", dir, err)
	}
	var found []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		var wf struct {
			Jobs map[string]struct {
				Steps []wfStep `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(raw, &wf); err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		for _, job := range wf.Jobs {
			for _, step := range job.Steps {
				if publishesNpm(step.Run) {
					found = append(found, e.Name())
					goto next
				}
			}
		}
	next:
	}
	if len(found) == 0 {
		t.Fatal("no workflow publishes npm — if publishing moved, point this test at it rather than deleting it")
	}
	return found
}

// publishesNpm reports whether a step body really publishes, ignoring comments
// and the --dry-publish form, which only packs.
func publishesNpm(run string) bool {
	for _, line := range strings.Split(run, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "--dry-publish") {
			continue
		}
		if strings.Contains(line, "build-packages.mjs") && strings.Contains(line, "--publish") {
			return true
		}
		if strings.Contains(line, "npm publish") && !strings.Contains(line, "--dry-run") {
			return true
		}
	}
	return false
}

func TestOnlyOneWorkflowPublishesNpm(t *testing.T) {
	found := workflowsThatPublishNpm(t)
	for _, name := range found {
		if name != publishingWorkflow {
			t.Errorf("%s publishes npm, but trusted publishing binds a publisher to one workflow "+
				"filename and npm allows one configuration per package. A second publishing workflow "+
				"cannot mint a credential and would need NPM_TOKEN kept alive for it — move the job "+
				"into %s instead", name, publishingWorkflow)
		}
	}
}

// Minting the OIDC token needs the permission, and without it npm silently
// falls back to a token instead of failing — so this is invisible until the
// secret is removed and a release cannot publish.
func TestPublishingWorkflowCanMintAnOIDCToken(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("../.github/workflows", publishingWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
			Steps       []wfStep          `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	for name, job := range wf.Jobs {
		publishes := false
		for _, s := range job.Steps {
			if strings.Contains(s.Run, "build-packages.mjs") && strings.Contains(s.Run, "--publish") {
				publishes = true
			}
		}
		if !publishes {
			continue
		}
		// A job's own permissions block replaces the workflow's, so either has
		// to grant it — but a job that narrows permissions without id-token
		// loses the ability silently.
		if job.Permissions != nil {
			if job.Permissions["id-token"] != "write" {
				t.Errorf("job %q publishes npm and sets its own permissions without id-token: write, "+
					"so it cannot mint the OIDC token and would fall back to NPM_TOKEN", name)
			}
			continue
		}
		if wf.Permissions["id-token"] != "write" {
			t.Errorf("job %q publishes npm but neither it nor the workflow grants id-token: write", name)
		}
	}
}

// npm only exchanges an OIDC token from 11.5.1; Node 22 ships npm 10. Without
// the upgrade the publish falls back to the token and the registration is never
// exercised — green, and still dependent on the secret.
var (
	npmUpgrade       = regexp.MustCompile(`npm install -g "?npm@`)
	npmUpgradeLatest = regexp.MustCompile(`npm install -g "?npm@latest\b`)
)

func TestPublishingWorkflowUpgradesNpm(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("../.github/workflows", publishingWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	// The spec may be quoted (`"npm@$NPM_VERSION"`, as zizmor prefers to a
	// template expansion) or bare.
	if !npmUpgrade.MatchString(body) {
		t.Error("the publishing workflow does not upgrade npm; Node 22 ships npm 10, which knows " +
			"nothing about trusted publishing and would quietly publish with NPM_TOKEN instead")
	}
	// Pinned rather than @latest, like the other tools here.
	if npmUpgradeLatest.MatchString(body) {
		t.Error("pin the npm version rather than tracking @latest: a release should not be the " +
			"first thing to meet a new npm")
	}
}

// Publishing is tokenless: every wrapper package has a trusted publisher for
// this workflow, so npm mints a credential from the job's own OIDC identity.
//
// A NODE_AUTH_TOKEN reappearing would not fail anything — npm would simply use
// it, quietly, and the trusted publishers would stop being exercised. The way
// that gets noticed is a token expiring a month later and taking the release
// with it, which is what started this whole migration.
func TestPublishingWorkflowCarriesNoNpmToken(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("../.github/workflows", publishingWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Env   map[string]string `yaml:"env"`
			Steps []struct {
				Name string            `yaml:"name"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatal(err)
	}
	for jobName, job := range wf.Jobs {
		if _, ok := job.Env["NODE_AUTH_TOKEN"]; ok {
			t.Errorf("job %q sets NODE_AUTH_TOKEN; npm would use it instead of the trusted "+
				"publisher, and the OIDC path would stop being exercised", jobName)
		}
		for _, step := range job.Steps {
			if _, ok := step.Env["NODE_AUTH_TOKEN"]; ok {
				t.Errorf("step %q in job %q sets NODE_AUTH_TOKEN; publishing is tokenless — npm "+
					"mints a credential from the job's OIDC identity", step.Name, jobName)
			}
		}
	}
}
