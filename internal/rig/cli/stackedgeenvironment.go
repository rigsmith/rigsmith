package cli

import (
	"fmt"
)

const stackEdgePublishEnvironment = "edge-publish"

type stackEdgeEnvironment struct {
	Name   string `json:"name"`
	Policy *struct {
		Protected bool `json:"protected_branches"`
		Custom    bool `json:"custom_branch_policies"`
	} `json:"deployment_branch_policy"`
}

type stackEdgeBranchPolicy struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

func stackEdgeEnvironmentPath(slug string) string {
	return "/repos/" + slug + "/environments/" + stackEdgePublishEnvironment
}

func (s *stackEdgeSetup) defaultBranch(slug string) (string, error) {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := s.api.request(s.ctx, "GET", "/repos/"+slug, nil, &repo); err != nil {
		return "", err
	}
	if repo.DefaultBranch == "" {
		return "", fmt.Errorf("publishing repo has no default branch")
	}
	return repo.DefaultBranch, nil
}

func (s *stackEdgeSetup) environment(slug string) (stackEdgeEnvironment, error) {
	var env stackEdgeEnvironment
	err := s.api.request(s.ctx, "GET", stackEdgeEnvironmentPath(slug), nil, &env)
	return env, err
}

func (s *stackEdgeSetup) branchPolicies(slug string) ([]stackEdgeBranchPolicy, error) {
	var all []stackEdgeBranchPolicy
	for page := 1; page <= 100; page++ {
		var response struct {
			Policies []stackEdgeBranchPolicy `json:"branch_policies"`
		}
		path := fmt.Sprintf("%s/deployment-branch-policies?per_page=100&page=%d", stackEdgeEnvironmentPath(slug), page)
		if err := s.api.request(s.ctx, "GET", path, nil, &response); err != nil {
			return nil, err
		}
		for _, p := range response.Policies {
			if p.ID <= 0 || p.Name == "" || (p.Type != "branch" && p.Type != "tag") {
				return nil, fmt.Errorf("incomplete deployment-branch policy metadata")
			}
		}
		all = append(all, response.Policies...)
		if len(response.Policies) < 100 {
			return all, nil
		}
	}
	return nil, fmt.Errorf("deployment-branch policy listing exceeded 100 pages")
}

func (s *stackEdgeSetup) checkEnvironment(name string) error {
	slug, err := s.publishingRepo(name)
	if err != nil {
		return err
	}
	branch, err := s.defaultBranch(slug)
	if err != nil {
		return err
	}
	env, err := s.environment(slug)
	if err != nil {
		return err
	}
	if env.Name != stackEdgePublishEnvironment || env.Policy == nil || env.Policy.Protected || !env.Policy.Custom {
		return fmt.Errorf("edge-publish must use custom deployment-branch rules")
	}
	policies, err := s.branchPolicies(slug)
	if err != nil {
		return err
	}
	if len(policies) != 1 || policies[0].Name != branch || policies[0].Type != "branch" {
		return fmt.Errorf("edge-publish must permit only default branch %s, with no tag rules", branch)
	}
	return nil
}

func (s *stackEdgeSetup) repairEnvironment(name string) error {
	slug, err := s.publishingRepo(name)
	if err != nil {
		return err
	}
	branch, err := s.defaultBranch(slug)
	if err != nil {
		return err
	}
	env, err := s.environment(slug)
	if err != nil && !stackEdgeMissing(err) {
		return err
	}
	if err != nil || env.Name != stackEdgePublishEnvironment || env.Policy == nil || env.Policy.Protected || !env.Policy.Custom {
		if err := s.confirm("create or update edge-publish in " + slug + " to require custom deployment-branch rules"); err != nil {
			return err
		}
		body := map[string]any{"deployment_branch_policy": map[string]bool{"protected_branches": false, "custom_branch_policies": true}}
		if err := s.api.request(s.ctx, "PUT", stackEdgeEnvironmentPath(slug), body, nil); err != nil {
			return err
		}
		env, err = s.environment(slug)
		if err != nil {
			return err
		}
		if env.Name != stackEdgePublishEnvironment || env.Policy == nil || env.Policy.Protected || !env.Policy.Custom {
			return fmt.Errorf("environment update was not confirmed by readback")
		}
	}
	policies, err := s.branchPolicies(slug)
	if err != nil {
		return err
	}
	found := false
	for _, p := range policies {
		if p.Name == branch && p.Type == "branch" && !found {
			found = true
			continue
		}
		if err := s.confirm(fmt.Sprintf("remove deployment %s rule %s from edge-publish in %s", p.Type, p.Name, slug)); err != nil {
			return err
		}
		path := fmt.Sprintf("%s/deployment-branch-policies/%d", stackEdgeEnvironmentPath(slug), p.ID)
		if err := s.api.request(s.ctx, "DELETE", path, nil, nil); err != nil {
			return err
		}
	}
	if !found {
		if err := s.confirm("allow only default branch " + branch + " to deploy to edge-publish in " + slug); err != nil {
			return err
		}
		body := map[string]string{"name": branch, "type": "branch"}
		if err := s.api.request(s.ctx, "POST", stackEdgeEnvironmentPath(slug)+"/deployment-branch-policies", body, nil); err != nil {
			return err
		}
	}
	return s.checkEnvironment(name)
}
