package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type stackCIError struct {
	step string
	code int
	err  error
}

func (e *stackCIError) Error() string { return fmt.Sprintf("stack ci %s: %v", e.step, e.err) }
func (e *stackCIError) Unwrap() error { return e.err }
func (e *stackCIError) ExitCode() int { return e.code }

// StackCIExitCode recognizes only the explicit stack CI phase status contract.
// Other subprocess failures retain the CLI's default exit status.
func StackCIExitCode(err error) (int, bool) {
	var phase *stackCIError
	if errors.As(err, &phase) {
		return phase.ExitCode(), true
	}
	return 0, false
}

var stackCIExecutable = os.Executable

func newStackCICmd() *cobra.Command {
	var manifest, member, build, test, allowedHosts string
	var tip bool
	c := &cobra.Command{Use: "ci", Short: "Build and test a CI checkout inside an isolated stackspace", Long: "Rebuilds a committed manifest in a temporary directory, replaces one member\nwith the committed CI checkout tree, wires sibling sources, then runs build\nand test from that member. Commands use bash. Prints resolved commits and\nstep results; appends them to GITHUB_STEP_SUMMARY when set. Exit codes:\n10 setup, 11 wire, 12 build, 13 test. The checkout is never modified. Hosts must be allowed by --allowed-hosts;\nLFS members are refused. CI uses the built-in checksum-pinned josh engine.", Args: cobra.NoArgs}
	c.Flags().StringVar(&allowedHosts, "allowed-hosts", "github.com", "Comma-separated allowed fork/upstream hosts")
	_ = c.RegisterFlagCompletionFunc("allowed-hosts", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})
	c.Flags().StringVar(&manifest, "manifest", "", "Path to a committed rig.stack.jsonc")
	c.Flags().StringVar(&member, "member", "", "Member prefix (default: match origin URL)")
	c.Flags().StringVar(&build, "build", "rig build", "Build command, run from the member")
	c.Flags().StringVar(&test, "test", "rig test", "Test command, run from the member")
	c.Flags().BoolVar(&tip, "upstream-tip", false, "Use upstream tips instead of manifest cursors")
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		resolved := map[string]string{}
		results := map[string]string{}
		var summary strings.Builder
		summary.WriteString("## Stackspace CI\n\n")
		defer func() {
			if p := os.Getenv("GITHUB_OUTPUT"); p != "" {
				b, _ := json.Marshal(resolved)
				f, e := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
				if e == nil {
					fmt.Fprintf(f, "commits=%s\ntest-result=%s\n", b, results["test"])
					f.Close()
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(), "writing outputs: %v\n", e)
				}
			}
			if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
				f, e := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
				if e == nil {
					_, e = f.WriteString(summary.String())
					f.Close()
				}
				if e != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "writing summary: %v\n", e)
				}
			}
		}()
		step := func(name string, code int, fn func() error) error {
			fmt.Fprintf(cmd.OutOrStdout(), "· %s\n", name)
			e := fn()
			result := "passed"
			if e != nil {
				result = "failed: " + e.Error()
			}
			results[name] = result
			fmt.Fprintf(&summary, "- %s: %s\n", name, result)
			if e != nil {
				return &stackCIError{name, code, e}
			}
			return nil
		}
		checkout, e := stackRoot(cmd.Context())
		if e != nil {
			return step("setup", 10, func() error { return e })
		}
		var temp, root, exe string
		defer func() {
			if temp != "" {
				_ = os.RemoveAll(temp)
			}
		}()

		run := func(dir string, args ...string) error {
			p := exec.CommandContext(cmd.Context(), exe, args...)
			p.Dir = dir
			p.Stdout = cmd.OutOrStdout()
			p.Stderr = cmd.ErrOrStderr()
			p.Env = append(os.Environ(), "GIT_AUTHOR_NAME=rig CI", "GIT_AUTHOR_EMAIL=ci@localhost", "GIT_COMMITTER_NAME=rig CI", "GIT_COMMITTER_EMAIL=ci@localhost", "RIG_STACK_CI_VERIFIED=1")
			return p.Run()
		}
		e = step("setup", 10, func() error {
			temp, e = os.MkdirTemp("", "rig-stack-ci-")
			if e != nil {
				return e
			}
			root = filepath.Join(temp, "stack")
			exe, e = stackCIExecutable()
			if e != nil {
				return e
			}

			if manifest == "" {
				return fmt.Errorf("--manifest is required")
			}

			if e := os.Mkdir(root, 0700); e != nil {
				return e
			}
			p := manifest
			if !filepath.IsAbs(p) {
				p = filepath.Join(checkout, p)
			}
			rel, e := filepath.Rel(checkout, p)
			if e != nil {
				return e
			}
			data, e := stackCIGit(cmd.Context(), checkout, "show", "HEAD:"+filepath.ToSlash(rel))
			if e != nil {
				return fmt.Errorf("manifest must be committed: %w", e)
			}
			if e = os.WriteFile(filepath.Join(root, "rig.stack.jsonc"), data, 0600); e != nil {
				return e
			}
			if _, e = stackCIGit(cmd.Context(), root, "init", "-q"); e != nil {
				return e
			}
			m, _, e := loadStackManifest(root)
			if e != nil {
				return e
			}
			if m == nil {
				return fmt.Errorf("no stack manifest")
			}
			if e := stackCIValidate(m, allowedHosts); e != nil {
				return e
			}
			if member == "" {
				b, e := stackCIGit(cmd.Context(), checkout, "remote", "get-url", "origin")
				if e != nil {
					return e
				}
				spec := stackNormalizeSpec(strings.TrimSpace(string(b)))
				for _, n := range m.names() {
					r := m.Repos[n]
					if strings.EqualFold(spec, r.Upstream) || strings.EqualFold(spec, r.Fork) {
						if member != "" {
							return fmt.Errorf("origin matches multiple members; set --member")
						}
						member = n
					}
				}
			}
			if m.Repos[member] == nil {
				return fmt.Errorf("cannot match member; set --member to a manifest prefix")
			}
			// CI cursor mode must not silently select a proposed fork branch.
			for _, n := range m.names() {
				r := m.Repos[n]
				r.TrackBranch = ""
				if tip {
					r.UpstreamCommit = ""
					r.UpstreamTag = ""
					delete(m.LastSync, n)
				} else if n != member {
					if !stackIsSHA(m.cursor(n)) {
						return fmt.Errorf("member %s has no full manifest cursor", n)
					}
					r.UpstreamBranch = ""
					r.Branch = ""
					r.UpstreamTag = ""
					r.UpstreamCommit = m.cursor(n)
				}
			}
			m.LastPropose = nil
			data, e = json.MarshalIndent(m, "", "  ")
			if e != nil {
				return e
			}
			if e = os.WriteFile(filepath.Join(root, "rig.stack.jsonc"), data, 0600); e != nil {
				return e
			}
			if e = run(root, "stack", "init"); e != nil {
				return fmt.Errorf("rebuilding members (private upstreams may be unavailable to fork PRs): %w", e)
			}
			m, _, e = loadStackManifest(root)
			if e != nil {
				return e
			}
			if err := stackCIGuardPush(cmd.Context(), root); err != nil {
				return err
			}
			for _, n := range m.names() {
				resolved[n] = m.cursor(n)
			}
			sha, e := stackCIReplace(cmd.Context(), checkout, root, member)
			if e != nil {
				return e
			}
			if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() && d.Name() == ".git" {
					return filepath.SkipDir
				}
				if !d.IsDir() && d.Name() == ".gitattributes" {
					data, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					if bytes.Contains(data, []byte("filter=lfs")) {
						return fmt.Errorf("member uses Git LFS (%s); stack ci does not fetch LFS objects", path)
					}
				}
				return nil
			}); err != nil {
				return err
			}
			resolved[member] = sha
			summary.WriteString("\n| Member | Resolved commit | Josh filter |\n| --- | --- | --- |\n")
			for _, n := range m.names() {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", n, resolved[n])
				fmt.Fprintf(&summary, "| %s | `%s` | `%s` |\n", n, resolved[n], stackPrefixFilter(n))
			}
			summary.WriteString("\n")
			if p := os.Getenv("RIG_STACK_PROVENANCE"); p != "" {
				records := map[string]map[string]string{}
				for _, n := range m.names() {
					records[n] = map[string]string{"commit": resolved[n], "filter": stackPrefixFilter(n)}
				}
				b, err := json.MarshalIndent(records, "", "  ")
				if err != nil {
					return err
				}
				if err = os.WriteFile(p, b, 0600); err != nil {
					return err
				}
			}
			return nil
		})
		if e != nil {
			return e
		}
		if e = step("wire", 11, func() error { return run(root, "stack", "wire") }); e != nil {
			return e
		}
		shell := func(s string) error {
			if s == "" {
				return nil
			}
			p := exec.CommandContext(cmd.Context(), "bash", "-eo", "pipefail", "-c", s)
			p.Dir = filepath.Join(root, member)
			p.Stdout = cmd.OutOrStdout()
			p.Stderr = cmd.ErrOrStderr()
			return p.Run()
		}
		if e = step("build", 12, func() error { return shell(build) }); e != nil {
			return e
		}
		return step("test", 13, func() error { return shell(test) })
	}
	return c
}

func stackCIGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	p := exec.CommandContext(ctx, "git", append([]string{"--no-replace-objects"}, args...)...)
	p.Dir = dir
	p.Env = append(os.Environ(), "GIT_AUTHOR_NAME=rig CI", "GIT_AUTHOR_EMAIL=ci@localhost", "GIT_COMMITTER_NAME=rig CI", "GIT_COMMITTER_EMAIL=ci@localhost")
	var stderr bytes.Buffer
	p.Stderr = &stderr
	b, e := p.Output()
	if e != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), e, stderr.String())
	}
	return b, nil
}

// Read raw tree blobs: archive export attributes and checkout filters must not
// remove or rewrite the committed source. No index or worktree config is used.
func stackCIMaterialize(ctx context.Context, checkout, dest string) error {
	entries, err := stackCIGit(ctx, checkout, "ls-tree", "-rz", "--full-tree", "HEAD")
	if err != nil {
		return err
	}
	batch := exec.CommandContext(ctx, "git", "--no-replace-objects", "cat-file", "--batch")
	batch.Dir = checkout
	input, err := batch.StdinPipe()
	if err != nil {
		return err
	}
	output, err := batch.StdoutPipe()
	if err != nil {
		return err
	}
	if err = batch.Start(); err != nil {
		return err
	}
	defer func() { input.Close(); batch.Process.Kill(); batch.Wait() }()
	reader := bufio.NewReader(output)
	for _, entry := range bytes.Split(entries, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		meta, path, ok := strings.Cut(string(entry), "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return fmt.Errorf("invalid HEAD tree entry")
		}
		if path == ".gitmodules" || fields[0] == "160000" {
			return fmt.Errorf("member contains submodules (.gitmodules or gitlink %s); stack ci cannot materialize them", path)
		}
		target := filepath.Join(dest, filepath.FromSlash(path))
		if !insideRoot(dest, filepath.Dir(target)) {
			return fmt.Errorf("member path escapes prefix: %s", path)
		}
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if _, err = fmt.Fprintln(input, fields[2]); err != nil {
			return err
		}
		header, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		parts := strings.Fields(header)
		if len(parts) != 3 || parts[1] != "blob" {
			return fmt.Errorf("invalid cat-file response: %s", header)
		}
		size, err := strconv.Atoi(parts[2])
		if err != nil || size < 0 {
			return fmt.Errorf("invalid blob size")
		}
		data := make([]byte, size)
		if _, err = io.ReadFull(reader, data); err != nil {
			return err
		}
		if _, err = reader.ReadByte(); err != nil {
			return err
		}
		if filepath.Base(path) == ".gitattributes" && bytes.Contains(data, []byte("filter=lfs")) {
			return fmt.Errorf("member uses Git LFS (%s); stack ci does not fetch LFS objects", path)
		}
		switch fields[0] {
		case "120000":
			link := string(data)
			if filepath.IsAbs(link) || !insideRoot(dest, filepath.Dir(target)+string(filepath.Separator)+link) {
				return fmt.Errorf("member symlink escapes prefix: %s", path)
			}
			if err = os.Symlink(link, target); err != nil {
				return err
			}
		case "100644", "100755":
			mode := os.FileMode(0644)
			if fields[0] == "100755" {
				mode = 0755
			}
			if err = os.WriteFile(target, data, mode); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported member tree mode %s", fields[0])
		}
	}
	return nil
}

func stackCIReplace(ctx context.Context, checkout, root, member string) (string, error) {
	if e := stackValidPrefix(member); e != nil {
		return "", e
	}
	sha, e := stackCIGit(ctx, checkout, "rev-parse", "HEAD")
	if e != nil {
		return "", e
	}
	dest := filepath.Join(root, member)
	if e = os.RemoveAll(dest); e != nil {
		return "", e
	}
	if e = os.MkdirAll(dest, 0755); e != nil {
		return "", e
	}
	if e = stackCIMaterialize(ctx, checkout, dest); e != nil {
		return "", e
	}
	if _, e = stackCIGit(ctx, root, "add", "-A", "--", member); e != nil {
		return "", e
	}
	_, e = stackCIGit(ctx, root, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "stack ci: checkout "+strings.TrimSpace(string(sha)))
	return strings.TrimSpace(string(sha)), e
}

func stackCIValidate(m *stackManifest, allowed string) error {
	if m.joshVersion() != stackJoshVersion {
		return fmt.Errorf("stack ci requires checksum-pinned josh %s", stackJoshVersion)
	}
	hosts := map[string]bool{}
	for _, h := range strings.Split(allowed, ",") {
		hosts[strings.TrimSpace(h)] = true
	}
	for n, r := range m.Repos {
		for _, spec := range []string{r.Fork, r.Upstream} {
			if spec == "" {
				continue
			}
			host, _ := stackSplitHost(spec)
			if !hosts[host] {
				return fmt.Errorf("member %s host %s is not in --allowed-hosts", n, host)
			}
		}
	}
	return nil
}

func stackCIGuardPush(ctx context.Context, root string) error {
	remotes, err := stackCIGit(ctx, root, "remote")
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(remotes)) != 0 {
		return fmt.Errorf("refusing fused history with configured push destinations")
	}
	if _, err = stackCIGit(ctx, root, "config", "push.default", "nothing"); err != nil {
		return err
	}
	hooks := filepath.Join(root, ".git", "hooks")
	if err = os.MkdirAll(hooks, 0700); err != nil {
		return err
	}
	if _, err = stackCIGit(ctx, root, "config", "core.hooksPath", hooks); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(hooks, "pre-push"), []byte("#!/bin/sh\necho 'Fused history must never be pushed' >&2\nexit 1\n"), 0700)
}
