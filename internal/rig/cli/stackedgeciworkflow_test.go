package cli

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func TestStackEdgeCIWorkflow(t *testing.T) {
	b, err := os.ReadFile("../../../examples/github-workflows/stack-edge.yml")
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		On          map[string]yaml.Node
		Permissions map[string]string
		Concurrency struct {
			Group  string
			Cancel bool `yaml:"cancel-in-progress"`
		}
		Jobs map[string]struct {
			Needs, If            string
			Environment          string
			RunsOn               string `yaml:"runs-on"`
			Permissions, Outputs map[string]string
			Steps                []struct {
				Uses, Run, ID string
				With          map[string]any
				Env           map[string]string
			}
		}
	}
	if err = yaml.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	if len(w.On) != 2 || w.On["workflow_dispatch"].Kind == 0 || w.On["schedule"].Kind == 0 || w.On["pull_request_target"].Kind != 0 {
		t.Fatal("unsafe workflow triggers")
	}
	if !reflect.DeepEqual(w.Permissions, map[string]string{"contents": "read"}) || w.Concurrency.Group == "" || w.Concurrency.Cancel {
		t.Fatal("unsafe permissions or missing serialization")
	}
	if len(w.Jobs) != 2 || w.Jobs["publish"].Needs != "build" || w.Jobs["publish"].If != "${{ needs.build.result == 'success' && needs.build.outputs.status == 'packed' }}" {
		t.Fatal("publisher must skip inconclusive and failed builds")
	}
	if w.Jobs["build"].Outputs["status"] != "${{ steps.report.outputs.status }}" {
		t.Fatal("missing report status output")
	}
	if w.Jobs["publish"].Environment != "edge-publish" || w.Jobs["build"].Environment != "" {
		t.Fatal("only publisher must use the protected edge-publish environment")
	}
	pin := regexp.MustCompile(`@[0-9a-f]{40}$`)
	for name, job := range w.Jobs {
		if job.RunsOn != "ubuntu-latest" || !reflect.DeepEqual(job.Permissions, map[string]string{"contents": "read"}) {
			t.Fatal("jobs must use fresh hosted runners with read-only contents")
		}
		if name == "publish" && len(job.Steps) != 4 {
			t.Fatal("publisher must only set up Go, install rig, download artifacts and publish")
		}
		install, command, transfer, checkout := -1, -1, -1, 0
		for i, s := range job.Steps {
			if s.Uses != "" && !pin.MatchString(s.Uses) {
				t.Fatal("unpinned action", s.Uses)
			}
			if strings.Contains(s.Uses, "cache@") || strings.HasPrefix(s.Uses, "./") {
				t.Fatal("cache or local action crosses trust boundary")
			}
			if strings.HasPrefix(s.Uses, "actions/checkout@") {
				checkout++
				if name != "build" || s.With["persist-credentials"] != false {
					t.Fatal("unsafe checkout")
				}
			}
			if strings.HasPrefix(s.Uses, "actions/setup-go@") && s.With["cache"] != false {
				t.Fatal("Go cache must be disabled")
			}
			for k, v := range s.Env {
				if strings.Contains(v, "secrets.") || strings.Contains(v, "github.token") {
					if name != "publish" || k != "EDGE_FEED_KEY" || v != "${{ secrets.EDGE_FEED_KEY }}" || !strings.HasPrefix(s.Run, "rig stack edge ci ") {
						t.Fatal("credential outside publish command")
					}
				}
			}
			if strings.HasPrefix(s.Run, "go install ") {
				install = i
				if s.Run != "go install github.com/rigsmith/rigsmith/cmd/rig@vX.Y.Z" || s.Env["GOSUMDB"] != "sum.golang.org" || s.Env["GONOSUMDB"] != "" || s.Env["GOPRIVATE"] != "" {
					t.Fatal("missing independent pinned trusted rig install")
				}
			}
			if strings.HasPrefix(s.Run, "rig stack edge ci ") {
				command = i
				line := strings.Split(s.Run, "\n")[0]
				script, err := syntax.NewParser().Parse(strings.NewReader(line), "")
				if err != nil {
					t.Fatal(err)
				}
				if len(script.Stmts) != 1 || script.Stmts[0].Background || script.Stmts[0].Negated || len(script.Stmts[0].Redirs) != 0 {
					t.Fatal("expected one plain edge command")
				}
				call, ok := script.Stmts[0].Cmd.(*syntax.CallExpr)
				if !ok || len(call.Assigns) != 0 {
					t.Fatal("expected edge command call")
				}
				args, err := expand.Fields(&expand.Config{Env: expand.ListEnviron("EDGE_NUMBER=42")}, call.Args...)
				if err != nil {
					t.Fatal(err)
				}
				want := []string{"rig", "stack", "edge", "ci", "--seed", "acme/rigstack-app@main", "--member", "lib"}
				if name == "build" {
					want = append(want, "--number", "42", "--build", "rig build", "--test", "rig test", "--report", "edge.json", "-o", "edge-out")
					if s.ID != "report" || !reflect.DeepEqual(s.Env, map[string]string{"EDGE_NUMBER": "${{ github.run_number }}"}) || !strings.Contains(s.Run, `jq -r '.status' edge.json`) || !strings.Contains(s.Run, `>> "$GITHUB_OUTPUT"`) {
						t.Fatal("missing run number or status output")
					}
				} else {
					want = append(want, "--number", "42", "--publish", "report/edge.json", "-o", "report/edge-out")
					if !reflect.DeepEqual(s.Env, map[string]string{"EDGE_FEED_KEY": "${{ secrets.EDGE_FEED_KEY }}", "EDGE_NUMBER": "${{ github.run_number }}"}) {
						t.Fatal("publisher must receive only feed secret and trusted run number")
					}
				}
				if !reflect.DeepEqual(args, want) {
					t.Fatalf("%s command: %q, want %q", name, args, want)
				}
			}
			if strings.HasPrefix(s.Uses, "actions/upload-artifact@") {
				transfer = i
				if name != "build" || s.With["name"] != "edge-packages" || strings.TrimSpace(s.With["path"].(string)) != "edge.json\nedge-out/*.nupkg" || s.With["if-no-files-found"] != "error" {
					t.Fatal("wrong upload inventory")
				}
			}
			if strings.HasPrefix(s.Uses, "actions/download-artifact@") {
				transfer = i
				if name != "publish" || s.With["name"] != "edge-packages" || s.With["path"] != "report" {
					t.Fatal("wrong artifact download")
				}
			}
		}
		if install < 0 || command <= install || transfer < 0 || name == "build" && (checkout != 1 || transfer <= command) || name == "publish" && (checkout != 0 || transfer >= command) {
			t.Fatal("unsafe job ordering", name)
		}
	}
	if !strings.Contains(string(b), "to opt in") || !strings.Contains(string(b), "--allow-tip-publish") {
		t.Fatal("missing tip opt-in guidance")
	}
	if !strings.Contains(string(b), "deployment-branch rule limited to the default") || !strings.Contains(string(b), "environment secret, not a repository secret") || !strings.Contains(string(b), "remove any repository copy") {
		t.Fatal("missing environment branch protection and secret placement guidance")
	}
}
