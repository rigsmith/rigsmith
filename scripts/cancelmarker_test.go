package scripts

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The cheap Linux jobs each cancel the run when they fail, and each carries its
// own copy of the step that does it. The copies exist on purpose: a local
// composite action cannot load without a successful checkout, so a checkout
// failure would leave the expensive macOS and Windows tiers running — exactly
// the case the rule is there to stop.
//
// What duplication costs is drift, and here the thing that would drift is the
// wording. That wording is the whole point: a failing Linux job makes the RUN
// conclude "cancelled", which was read as a Blacksmith runner fault for a day
// before anyone opened the per-step conclusions. These tests hold the copies
// identical and keep the sentence that explains it.

const ciWorkflow = "../.github/workflows/ci.yml"

// cancelStep is one `stop the expensive tiers` step: the job name it announces,
// and the shell body, with the job name replaced so the bodies can be compared.
type cancelStep struct {
	job  string
	body string
}

// The trailing alternation matters: the last cancel step in the file is not
// followed by a blank line, and a pattern that only accepted one silently found
// three steps out of four.
var cancelStepRe = regexp.MustCompile(
	`(?s)- if: failure\(\)\n\s+name: stop the expensive tiers\n(.*?)(?:\n\n|\n?\z)`)

var jobEnvRe = regexp.MustCompile(`JOB: (.+)`)

func readCancelSteps(t *testing.T) []cancelStep {
	t.Helper()
	raw, err := os.ReadFile(ciWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	// Match against LF however git checked the file out; a CRLF checkout
	// otherwise reads no steps and the test passes vacuously.
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")

	var out []cancelStep
	for _, m := range cancelStepRe.FindAllStringSubmatch(text, -1) {
		block := m[1]
		j := jobEnvRe.FindStringSubmatch(block)
		if j == nil {
			t.Errorf("a cancel step has no JOB env:\n%s", block)
			continue
		}
		job := strings.TrimSpace(j[1])
		out = append(out, cancelStep{job: job, body: strings.ReplaceAll(block, job, "<JOB>")})
	}
	return out
}

func TestEveryCancelStepCarriesTheSameMarker(t *testing.T) {
	steps := readCancelSteps(t)

	// If the pattern stops matching, every assertion below passes vacuously.
	// The count is the guard: four cheap Linux jobs cancel the run.
	if len(steps) != 4 {
		t.Fatalf("found %d cancel steps in %s, want 4 — fix this test's pattern rather than deleting it",
			len(steps), ciWorkflow)
	}

	for _, s := range steps[1:] {
		if s.body != steps[0].body {
			t.Errorf("the %q cancel step has drifted from the %q one.\n\n%q:\n%s\n%q:\n%s",
				s.job, steps[0].job, steps[0].job, steps[0].body, s.job, s.body)
		}
	}
}

func TestEveryCancelStepNamesItsOwnJob(t *testing.T) {
	steps := readCancelSteps(t)

	var jobs []string
	for _, s := range steps {
		jobs = append(jobs, s.job)
	}
	sort.Strings(jobs)

	// The display names as the checks list shows them; the marker is useless if
	// it names something a reader cannot find in the UI.
	want := []string{"gitleaks", "govulncheck", "test (linux)", "vet + gofmt"}
	if strings.Join(jobs, "|") != strings.Join(want, "|") {
		t.Errorf("cancel steps announce %v, want %v", jobs, want)
	}
}

// The marker has one job: stop a reader concluding that "cancelled" means the
// runners or the concurrency group. If that sentence goes, the marker is
// decoration.
func TestTheMarkerExplainsWhatCancelledMeans(t *testing.T) {
	steps := readCancelSteps(t)
	if len(steps) == 0 {
		t.Fatal("no cancel steps found")
	}
	body := steps[0].body

	for _, want := range []string{"::error title=", "concurrency group", "cancelled"} {
		if !strings.Contains(body, want) {
			t.Errorf("the cancel marker no longer mentions %q:\n%s", want, body)
		}
	}
}

// Writing the marker must never be able to stop the cancel: the annotation is
// a nicety, the cancel is what saves the macOS and Windows tiers. A composite
// step runs under `set -e`, so an unguarded write that fails takes the step
// down before `gh run cancel` is reached.
func TestTheMarkerCannotSuppressTheCancel(t *testing.T) {
	steps := readCancelSteps(t)
	if len(steps) == 0 {
		t.Fatal("no cancel steps found")
	}
	body := steps[0].body

	echo := strings.Index(body, "echo \"::error")
	cancel := strings.Index(body, "gh run cancel")
	switch {
	case echo < 0 || cancel < 0:
		t.Fatalf("cancel step no longer has both an annotation and a cancel:\n%s", body)
	case cancel < echo:
		t.Errorf("the cancel runs before the annotation, so a cancelled step loses the marker:\n%s", body)
	}

	line := body[echo:]
	if i := strings.Index(line, "\n"); i >= 0 {
		line = line[:i]
	}
	if !strings.HasSuffix(strings.TrimSpace(line), "|| true") {
		t.Errorf("the annotation is not best-effort; under `set -e` a failed write "+
			"would skip `gh run cancel` and the expensive tiers would keep running:\n%s", line)
	}
}

// A workflow command is a single line. A newline in the middle of one turns the
// rest into plain log output, and the marker silently stops appearing on the
// run page.
func TestTheAnnotationIsOneLine(t *testing.T) {
	steps := readCancelSteps(t)
	if len(steps) == 0 {
		t.Fatal("no cancel steps found")
	}
	for _, s := range steps {
		for _, line := range strings.Split(s.body, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "echo \"::error") {
				continue
			}
			if strings.Count(line, `"`) < 2 || !strings.Contains(line, "|| true") {
				t.Errorf("the annotation looks split across lines, which would stop it "+
					"being read as a workflow command:\n%s", line)
			}
		}
	}
}
