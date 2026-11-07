package cli

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

//go:embed stack-edge.yml
var stackEdgeWorkflowTemplate string

const stackEdgeWorkflowPath = ".github/workflows/stack-edge.yml"

func (s *stackEdgeSetup) workflow(name string) ([]byte, error) {
	var members []string
	for _, member := range s.m.names() {
		if s.m.Edge[member] != nil {
			members = append(members, member)
		}
	}
	if len(members) <= 1 {
		return s.memberWorkflow(name)
	}
	// One root workflow serves all edge members. Independent job pairs avoid
	// replacing another member's workflow on the next fact check or init run.
	var result strings.Builder
	for i, member := range members {
		data, err := s.memberWorkflow(member)
		if err != nil {
			return nil, err
		}
		header, jobs, ok := strings.Cut(string(data), "\njobs:\n")
		if !ok {
			return nil, fmt.Errorf("edge workflow template has no jobs")
		}
		if i == 0 {
			result.WriteString(strings.ReplaceAll(header, "group: stack-edge-"+member, "group: stack-edge"))
			result.WriteString("\njobs:\n")
		}
		build, publish := fmt.Sprintf("build_%d", i), fmt.Sprintf("publish_%d", i)
		result.WriteString(strings.NewReplacer("  build:\n", "  "+build+":\n", "  publish:\n", "  "+publish+":\n",
			"needs: build", "needs: "+build, "needs.build.", "needs."+build+".", "edge-packages", fmt.Sprintf("edge-packages-%d", i)).Replace(jobs))
	}
	return []byte(result.String()), nil
}

func (s *stackEdgeSetup) memberWorkflow(name string) ([]byte, error) {
	remote := stackHomeConfig(s.ctx, s.root, "URL")
	branch := stackHomeConfig(s.ctx, s.root, "Branch")
	if remote == "" || branch == "" || stackHomeConfig(s.ctx, s.root, "Pinned") == "true" {
		return nil, fmt.Errorf("configure a branch-based manifest home with rig stack seed --publish first; edge never creates the home")
	}
	if err := stackValidateHomeRemote(remote); err != nil {
		return nil, err
	}
	seed := remote
	if spec := stackNormalizeSpec(remote); strings.HasPrefix(spec, "github.com/") {
		seed = strings.TrimPrefix(spec, "github.com/") + "@" + branch
	}
	secret := s.m.Edge[name].PushKeySecret
	if !stackEdgeValidSecretName(secret) {
		return nil, fmt.Errorf("set edge.%s.pushKeySecret to a secret NAME", name)
	}
	// Seed/member arguments occur in shell, concurrency and comments. Keep their
	// shell spelling literal; JSON/YAML alone would not escape the run script.
	if strings.ContainsAny(seed+name, "'\"`$\\\r\n") {
		return nil, fmt.Errorf("home or member cannot be represented safely in the workflow")
	}
	return []byte(strings.NewReplacer(
		"acme/rigstack-app@main", "'"+seed+"'",
		"--member lib", "--member '"+name+"'",
		"stack-edge-lib", "stack-edge-"+name,
		"edge.lib.pushKeySecret", "edge."+name+".pushKeySecret",
		"EDGE_FEED_KEY", secret,
	).Replace(strings.ReplaceAll(stackEdgeWorkflowTemplate, "\r\n", "\n"))), nil
}

func (s *stackEdgeSetup) workflowPath() (string, error) {
	// A root path must never follow a link into a member (or outside the root).
	for _, rel := range []string{".github", ".github/workflows", stackEdgeWorkflowPath} {
		path := filepath.Join(s.root, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("root workflow path must not contain a symlink: %s", rel)
		}
	}
	if s.m.Repos[".github"] != nil {
		return "", fmt.Errorf("root workflow would be inside member .github")
	}
	return filepath.Join(s.root, filepath.FromSlash(stackEdgeWorkflowPath)), nil
}

func (s *stackEdgeSetup) checkWorkflowPublication(name string) error {
	want, err := s.workflow(name)
	if err != nil {
		return err
	}
	path, err := s.workflowPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), want) {
		return fmt.Errorf("root %s is missing or differs from the edge template", stackEdgeWorkflowPath)
	}
	committed, err := (&gitrepo.Repo{Dir: s.root}).ShowFile(s.ctx, "HEAD", stackEdgeWorkflowPath)
	if err != nil || !bytes.Equal(bytes.ReplaceAll(committed, []byte("\r\n"), []byte("\n")), want) {
		return fmt.Errorf("root edge workflow is not committed")
	}
	dir, err := os.MkdirTemp("", "rig-edge-workflow-home-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	remote, branch := stackHomeConfig(s.ctx, s.root, "URL"), stackHomeConfig(s.ctx, s.root, "Branch")
	if _, err := stackHomeGit(s.ctx, s.root, "clone", "--no-local", "--single-branch", "--branch", branch, "--", remote, dir); err != nil {
		return fmt.Errorf("cannot read published manifest home: %w", err)
	}
	if err := stackCheckHome(s.ctx, dir, s.m); err != nil {
		return err
	}
	published, err := (&gitrepo.Repo{Dir: dir}).ShowFile(s.ctx, "HEAD", stackEdgeWorkflowPath)
	if err != nil || !bytes.Equal(bytes.ReplaceAll(published, []byte("\r\n"), []byte("\n")), want) {
		return fmt.Errorf("edge workflow is not published to the manifest home")
	}
	return nil
}

func (s *stackEdgeSetup) repairWorkflowPublication(name string) error {
	want, err := s.workflow(name)
	if err != nil {
		return err
	}
	path, err := s.workflowPath()
	if err != nil {
		return err
	}
	current, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	if !bytes.Equal(bytes.ReplaceAll(current, []byte("\r\n"), []byte("\n")), want) {
		if err := s.confirm("write " + stackEdgeWorkflowPath + " at the stackspace root"); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, want, 0o644); err != nil {
			return err
		}
	}
	committed, err := (&gitrepo.Repo{Dir: s.root}).ShowFile(s.ctx, "HEAD", stackEdgeWorkflowPath)
	if err != nil || !bytes.Equal(bytes.ReplaceAll(committed, []byte("\r\n"), []byte("\n")), want) {
		if err := s.confirm("commit only " + stackEdgeWorkflowPath); err != nil {
			return err
		}
		if _, err := (&gitrepo.Repo{Dir: s.root}).CommitPaths(s.ctx, "stack: set up edge workflow", stackEdgeWorkflowPath); err != nil {
			return err
		}
	}
	if err := s.checkWorkflowPublication(name); err == nil {
		return nil
	}
	if err := s.confirm("run rig stack seed --publish to the configured manifest home"); err != nil {
		return err
	}
	cmd := newStackSeedCmd()
	remote := stackHomeConfig(s.ctx, s.root, "URL")
	if err := cmd.Flags().Set("publish", remote); err != nil {
		return err
	}
	cmd.SetContext(s.ctx)
	cmd.SetOut(s.out)
	cmd.SetErr(s.out)
	return cmd.RunE(cmd, nil)
}
