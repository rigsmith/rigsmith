package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

func TestStackEdgeEnvironmentCreateAndReadback(t *testing.T) {
	s, f, _ := edgeSetupFixture(t)
	f.environment, f.customBranches, f.policies = false, false, nil
	if err := s.checkEnvironment("lib"); err == nil {
		t.Fatal("missing environment passed")
	}
	if err := s.repairEnvironment("lib"); err != nil {
		t.Fatal(err)
	}
	if err := s.checkEnvironment("lib"); err != nil {
		t.Fatal(err)
	}
	if f.writes != 2 || len(f.policies) != 1 || f.policies[0].Name != "main" || f.policies[0].Type != "branch" {
		t.Fatalf("environment state: %v %v", f.requests, f.policies)
	}
	if !slicesContainEdgeRequest(f.requests, "GET /repos/publisher/edge/environments/edge-publish/deployment-branch-policies?per_page=100&page=1") {
		t.Fatal("branch rule not read back")
	}
	s.prompt = func(*huh.Confirm) error { t.Fatal("idempotent environment prompted"); return nil }
	if err := s.repairEnvironment("lib"); err != nil || f.writes != 2 {
		t.Fatalf("second run: %v", err)
	}
}

func TestStackEdgeEnvironmentRestrictsExistingRules(t *testing.T) {
	s, f, _ := edgeSetupFixture(t)
	f.environment = true
	f.customBranches = false
	f.policies = []stackEdgeBranchPolicy{{ID: 1, Name: "*", Type: "branch"}, {ID: 2, Name: "main", Type: "tag"}}
	if err := s.repairEnvironment("lib"); err != nil {
		t.Fatal(err)
	}
	if err := s.checkEnvironment("lib"); err != nil {
		t.Fatal(err)
	}
	if f.writes != 4 {
		t.Fatalf("expected environment update, two removals and creation: %v", f.requests)
	}
	f.policies = append(f.policies, stackEdgeBranchPolicy{ID: 99, Name: "main", Type: "tag"})
	if err := s.checkEnvironment("lib"); err == nil {
		t.Fatal("extra tag rule passed")
	}
}

func TestStackEdgeEnvironmentRejectsUnconfirmedChanges(t *testing.T) {
	for _, stage := range []string{"environment", "branch-rule"} {
		t.Run(stage, func(t *testing.T) {
			s, f, _ := edgeSetupFixture(t)
			f.environment, f.customBranches, f.policies = false, false, nil
			if stage == "branch-rule" {
				f.environment, f.customBranches = true, true
			}
			f.environmentStuck = true
			if err := s.repairEnvironment("lib"); err == nil {
				t.Fatal("unconfirmed environment accepted")
			}
		})
	}
}

func TestStackEdgeEnvironmentNonTTY(t *testing.T) {
	for _, stage := range []string{"environment", "create-rule", "remove-rule"} {
		t.Run(stage, func(t *testing.T) {
			s, f, _ := edgeSetupFixture(t)
			f.environment, f.customBranches, f.policies = false, false, nil
			if stage != "environment" {
				f.environment, f.customBranches = true, true
			}
			if stage == "remove-rule" {
				f.policies = []stackEdgeBranchPolicy{{ID: 1, Name: "*", Type: "branch"}}
			}
			s.tty = func() bool { return false }
			s.prompt = func(*huh.Confirm) error { t.Fatal("prompt reached without terminal"); return nil }
			if err := s.repairEnvironment("lib"); err == nil || !strings.Contains(err.Error(), "stdin and stdout on a terminal") {
				t.Fatalf("non-TTY refusal: %v", err)
			}
			if f.writes != 0 {
				t.Fatalf("non-TTY wrote: %v", f.requests)
			}
		})
	}
}

func TestStackEdgeEnvironmentSecretRepositoryCopy(t *testing.T) {
	s, f, out := edgeSetupFixture(t)
	f.repoSecret = true
	if err := s.repairSecret("lib"); err != nil {
		t.Fatal(err)
	}
	err := s.checkSecret("lib")
	if err == nil || !strings.Contains(err.Error(), "bypasses edge-publish branch protection") || !strings.Contains(err.Error(), "delete the repository secret yourself") {
		t.Fatalf("repository copy not reported: %v", err)
	}
	if !f.repoSecret || f.writes != 1 {
		t.Fatal("deleted repository secret")
	}
	if err := s.repairSecret("lib"); err != nil || f.writes != 1 {
		t.Fatal("overwrote existing environment secret")
	}
	assertEdgeSetupNoSecret(t, s, f, out, err)
}

func TestStackEdgeEnvironmentSecretRequiresProtection(t *testing.T) {
	s, f, _ := edgeSetupFixture(t)
	f.customBranches = false
	if err := s.repairSecret("lib"); err == nil || !strings.Contains(err.Error(), "protect edge-publish") {
		t.Fatalf("unprotected secret set: %v", err)
	}
	if f.sets != 0 {
		t.Fatal("set secret in unprotected environment")
	}
}
