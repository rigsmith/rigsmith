package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestStackDepsAction(t *testing.T) {
	data, err := os.ReadFile("../../../.github/actions/stack/action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Runs struct {
			Steps []struct {
				ID, Name, If, Run string
				Env               map[string]string
			}
		}
	}
	if err = yaml.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}
	var report, run string
	for _, step := range action.Runs.Steps {
		if step.ID == "run" {
			run = step.Run
			if step.Env["STACK_DEPENDENCIES"] != "${{ inputs.dependencies }}" || step.Env["STACK_STRICT"] != "${{ inputs.strict }}" {
				t.Fatal(step.Env)
			}
		}
		if step.Name == "Report topic dependencies" {
			report = step.Run
			if step.If != "${{ always() && steps.install.outcome == 'success' && inputs.dependencies == 'true' }}" {
				t.Fatal(step.If)
			}
		}
	}
	if report == "" || run == "" {
		t.Fatal("missing action steps")
	}
	for _, code := range []int{0, 1, 127} {
		t.Run(fmt.Sprintf("report exit %d", code), func(t *testing.T) {
			p := exec.Command("bash", "-e", "-c", fmt.Sprintf("rig() { return %d; };\n", code)+report)
			p.Env = append(os.Environ(), "STACK_MANIFEST=rig.stack.jsonc", "STACK_SEED=")
			out, err := p.CombinedOutput()
			if err != nil {
				t.Fatalf("report became fatal: %v %s", err, out)
			}
			if strings.Contains(string(out), "::warning::") != (code != 0) {
				t.Fatal(string(out))
			}
		})
	}
	for _, value := range []string{"true", "false", "TRUE", "yes", "", " true"} {
		t.Run(fmt.Sprintf("dependencies=%q", value), func(t *testing.T) {
			p := exec.Command("bash", "-e", "-c", "rig() { echo build-ran; };\n"+run)
			p.Env = append(stackCIActionDefaultEnv(t), "STACK_DEPENDENCIES="+value, "STACK_FORK_PR=false", "STACK_MANIFEST=rig.stack.jsonc", "STACK_MEMBER=app", "STACK_BUILD=true", "STACK_TEST=true")
			out, err := p.CombinedOutput()
			valid := value == "true" || value == "false"
			if valid {
				if err != nil || !strings.Contains(string(out), "build-ran") {
					t.Fatalf("valid input failed: %v %s", err, out)
				}
			} else if err == nil || !strings.Contains(string(out), "dependencies must be exactly true or false") || strings.Contains(string(out), "build-ran") {
				t.Fatalf("invalid input accepted: %v %s", err, out)
			}
		})
	}
}

func TestStackDepsActionSourceValidation(t *testing.T) {
	script := stackCIActionScript(t, "Report topic dependencies")
	for _, tc := range []struct {
		name, manifest, seed string
		valid                bool
	}{
		{"none", "", "", false},
		{"both", "rig.stack.jsonc", "acme/seed@main", false},
		{"manifest", "rig.stack.jsonc", "", true},
		{"seed", "", "acme/seed@main", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := exec.Command("bash", "-euo", "pipefail", "-c", "rig() { echo publication-invoked; };\n"+script)
			p.Env = append(os.Environ(), "STACK_MANIFEST="+tc.manifest, "STACK_SEED="+tc.seed, "STACK_ALLOWED_HOSTS=github.com")
			out, err := p.CombinedOutput()
			if tc.valid {
				if err != nil || !strings.Contains(string(out), "publication-invoked") {
					t.Fatalf("valid source blocked: %s %v", out, err)
				}
			} else {
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != 10 || !strings.Contains(string(out), "exactly one of manifest or seed") || strings.Contains(string(out), "publication-invoked") {
					t.Fatalf("invalid source reached publication: %s %v", out, err)
				}
			}
		})
	}
}
