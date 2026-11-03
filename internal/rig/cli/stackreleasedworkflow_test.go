package cli

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestStackReleasedWorkflow(t *testing.T) {
	data, err := os.ReadFile("../../../examples/github-workflows/stack-released.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		On          map[string]yaml.Node
		Permissions map[string]string
		Concurrency struct {
			Group  string
			Cancel bool `yaml:"cancel-in-progress"`
		}
		Jobs map[string]struct {
			Needs, If   string
			RunsOn      string `yaml:"runs-on"`
			Permissions map[string]string
			Strategy    struct {
				Matrix struct{ Include []map[string]string }
			}
			Steps []struct {
				Uses, Run string
				With      map[string]any
				Env       map[string]string
			}
		}
	}
	if err = yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	if _, ok := workflow.On["workflow_dispatch"]; !ok {
		t.Fatal("missing manual trigger")
	}
	var schedules []struct{ Cron string }
	schedule := workflow.On["schedule"]
	if err = schedule.Decode(&schedules); err != nil || len(schedules) == 0 || schedules[0].Cron == "" {
		t.Fatal("missing schedule:", err)
	}
	if !reflect.DeepEqual(workflow.Permissions, map[string]string{"contents": "read"}) {
		t.Fatal("unsafe workflow permissions")
	}
	if workflow.Concurrency.Group == "" || workflow.Concurrency.Cancel {
		t.Fatal("release reconciliations must serialize")
	}
	build, publish := workflow.Jobs["build"], workflow.Jobs["publish"]
	if len(workflow.Jobs) != 2 || build.Needs != "" || publish.Needs != "build" || publish.If != "${{ needs.build.result == 'success' }}" {
		t.Fatal("publish must follow a successful report job")
	}
	if !reflect.DeepEqual(build.Permissions, map[string]string{"contents": "read"}) {
		t.Fatal("build must be read-only")
	}
	if !reflect.DeepEqual(publish.Permissions, map[string]string{"contents": "write", "pull-requests": "write"}) {
		t.Fatal("unexpected publisher permissions")
	}
	if len(build.Strategy.Matrix.Include) == 0 || !reflect.DeepEqual(build.Strategy.Matrix.Include, publish.Strategy.Matrix.Include) {
		t.Fatal("report and publish matrices differ")
	}
	seen := map[string]bool{}
	for _, row := range build.Strategy.Matrix.Include {
		pair := row["member"] + "/" + row["sibling"]
		if row["member"] == "" || row["sibling"] == "" || row["package"] == "" || seen[pair] {
			t.Fatal("missing/duplicate member sibling package selection")
		}
		seen[pair] = true
	}
	artifact := "released-${{ matrix.member }}-${{ matrix.sibling }}"
	pinned := regexp.MustCompile(`@[0-9a-f]{40}$`)
	for name, job := range workflow.Jobs {
		if job.RunsOn != "ubuntu-latest" {
			t.Fatal("jobs need separate fresh hosted runners")
		}
		install, command, transfer := -1, -1, -1
		for i, step := range job.Steps {
			if step.Uses != "" && !pinned.MatchString(step.Uses) {
				t.Fatal("unpinned action:", step.Uses)
			}
			if strings.Contains(step.Uses, "checkout") || strings.Contains(step.Uses, "cache@") {
				t.Fatal("release job must not reuse a checkout or cache")
			}
			if strings.HasPrefix(step.Uses, "actions/setup-go@") && step.With["cache"] != false {
				t.Fatal("Go cache enabled")
			}
			if strings.HasPrefix(step.Run, "go install ") {
				install = i
				if step.Run != "go install github.com/rigsmith/rigsmith/cmd/rig@vX.Y.Z" || step.Env["GOSUMDB"] != "sum.golang.org" || step.Env["GONOSUMDB"] != "" || step.Env["GOPRIVATE"] != "" {
					t.Fatal("missing trusted module verification")
				}
			}
			if strings.HasPrefix(step.Run, "rig stack released ") {
				command = i
				expected := map[string]string{"GITHUB_TOKEN": "${{ github.token }}", "MEMBER": "${{ matrix.member }}", "SIBLING": "${{ matrix.sibling }}", "PACKAGE": "${{ matrix.package }}", "VIA": "${{ matrix.via }}", "WARNING": "${{ matrix.warning }}"}
				if !reflect.DeepEqual(step.Env, expected) {
					t.Fatal("matrix/token environment not propagated:", name)
				}
				for _, arg := range []string{`"$MEMBER" "$SIBLING"`, `--seed acme/rigstack-app@main`, `--package "$PACKAGE"`, `--via-package "$VIA"`, `--warning "$WARNING"`} {
					if !strings.Contains(step.Run, arg) {
						t.Fatalf("missing trusted %s argument in %s", arg, name)
					}
				}
				flag := "--report released.json"
				if name == "publish" {
					flag = "--publish report/released.json"
				}
				if !strings.Contains(step.Run, flag) {
					t.Fatalf("wrong report handoff command in %s: %s", name, step.Run)
				}
			}
			if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
				if name != "build" || step.With["name"] != artifact || step.With["path"] != "released.json" || step.With["if-no-files-found"] != "error" {
					t.Fatal("unsafe report upload")
				}
				transfer = i
			}
			if strings.HasPrefix(step.Uses, "actions/download-artifact@") {
				if name != "publish" || step.With["name"] != artifact || step.With["path"] != "report" {
					t.Fatal("incorrect report download")
				}
				transfer = i
			}
		}
		if install < 0 || command <= install || transfer < 0 || (name == "build" && transfer <= command) || (name == "publish" && transfer >= command) {
			t.Fatalf("bad install/report/artifact order in %s", name)
		}
	}
}
