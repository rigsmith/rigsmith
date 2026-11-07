package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/auth"
	"golang.org/x/term"
)

type stackEdgeWorkflow struct {
	ID    int64  `json:"id"`
	Path  string `json:"path"`
	State string `json:"state"`
}

func stackEdgeMissing(err error) bool {
	var status *stackDepsHTTPError
	return errors.As(err, &status) && status.status == 404
}

func (s *stackEdgeSetup) publishingRepoInfo(slug string) (bool, error) {
	var repo struct {
		Fork *bool `json:"fork"`
	}
	if err := s.api.request(s.ctx, "GET", "/repos/"+slug, nil, &repo); err != nil {
		return false, err
	}
	if repo.Fork == nil {
		return false, fmt.Errorf("repository response omitted fork status")
	}
	return *repo.Fork, nil
}

func (s *stackEdgeSetup) workflows(slug string) ([]stackEdgeWorkflow, error) {
	var all []stackEdgeWorkflow
	for page := 1; page <= 100; page++ {
		var resp struct {
			Workflows []stackEdgeWorkflow `json:"workflows"`
		}
		path := fmt.Sprintf("/repos/%s/actions/workflows?per_page=100&page=%d", slug, page)
		if err := s.api.request(s.ctx, "GET", path, nil, &resp); err != nil {
			return nil, err
		}
		for _, workflow := range resp.Workflows {
			if workflow.ID <= 0 || workflow.Path == "" || workflow.State == "" {
				return nil, fmt.Errorf("incomplete workflow metadata")
			}
		}
		all = append(all, resp.Workflows...)
		if len(resp.Workflows) < 100 {
			return all, nil
		}
	}
	return nil, fmt.Errorf("workflow listing exceeded 100 pages; cannot check every workflow")
}

func stackEdgeWorkflowDisabled(w stackEdgeWorkflow) bool {
	return strings.HasPrefix(w.State, "disabled_")
}

func (s *stackEdgeSetup) checkPublishingRepo(name string) error {
	defaultHome := s.m.Edge[name].PublishFrom == ""
	slug, err := s.publishingRepo(name)
	if err != nil {
		if defaultHome {
			return fmt.Errorf("publishing home is not ready; configure the manifest home: `rig stack seed --publish`: %w", err)
		}
		return err
	}
	fork, err := s.publishingRepoInfo(slug)
	if err != nil {
		if defaultHome {
			return fmt.Errorf("publishing home is not ready; configure the manifest home: `rig stack seed --publish`: %w", err)
		}
		return err
	}
	if defaultHome || !fork {
		return nil
	}
	workflows, err := s.workflows(slug)
	if err != nil {
		return err
	}
	for _, w := range workflows {
		if w.Path != ".github/workflows/stack-edge.yml" && !stackEdgeWorkflowDisabled(w) {
			return fmt.Errorf("publishing fork workflow %s is %s", w.Path, w.State)
		}
	}
	return nil
}

func (s *stackEdgeSetup) repairPublishingRepo(name string) error {
	// The existing seed flow owns the default home. Edge may create only an
	// explicitly configured publishing repository.
	if s.m.Edge[name].PublishFrom == "" {
		return s.checkPublishingRepo(name)
	}
	slug, err := s.publishingRepo(name)
	if err != nil {
		return err
	}
	fork, err := s.publishingRepoInfo(slug)
	if stackEdgeMissing(err) {
		if err := s.confirm("create empty private publishing repository " + slug); err != nil {
			return err
		}
		owner, repo, _ := strings.Cut(slug, "/")
		var user struct {
			Login string `json:"login"`
		}
		if err := s.api.request(s.ctx, "GET", "/user", nil, &user); err != nil {
			return err
		}
		path := "/orgs/" + url.PathEscape(owner) + "/repos"
		if strings.EqualFold(owner, user.Login) {
			path = "/user/repos"
		}
		if err := s.api.request(s.ctx, "POST", path, map[string]any{"name": repo, "private": true}, nil); err != nil {
			return err
		}
		fork, err = s.publishingRepoInfo(slug)
	}
	if err != nil {
		return err
	}
	if !fork {
		return nil
	}
	workflows, err := s.workflows(slug)
	if err != nil {
		return err
	}
	for _, w := range workflows {
		if w.Path == ".github/workflows/stack-edge.yml" || stackEdgeWorkflowDisabled(w) {
			continue
		}
		if err := s.confirm(fmt.Sprintf("disable workflow %s in %s", w.Path, slug)); err != nil {
			return err
		}
		path := fmt.Sprintf("/repos/%s/actions/workflows/%d", slug, w.ID)
		if err := s.api.request(s.ctx, "PUT", path+"/disable", nil, nil); err != nil {
			return err
		}
		var readback stackEdgeWorkflow
		if err := s.api.request(s.ctx, "GET", path, nil, &readback); err != nil {
			return err
		}
		if readback.ID != w.ID || readback.Path != w.Path || !stackEdgeWorkflowDisabled(readback) {
			return fmt.Errorf("workflow disable was not confirmed by readback")
		}
	}
	return nil
}

type stackEdgeSecretMetadata struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *stackEdgeSetup) secretLocation(name string) (string, string, error) {
	secret := s.m.Edge[name].PushKeySecret
	if secret == "" || !stackEdgeValidSecretName(secret) {
		return "", "", fmt.Errorf("manifest edit needed: set edge.%s.pushKeySecret to a secret NAME", name)
	}
	slug, err := s.publishingRepo(name)
	return slug, secret, err
}

func (s *stackEdgeSetup) secretMetadata(slug, secret string) (stackEdgeSecretMetadata, error) {
	var meta stackEdgeSecretMetadata
	err := s.api.request(s.ctx, "GET", "/repos/"+slug+"/actions/secrets/"+url.PathEscape(secret), nil, &meta)
	if err != nil {
		// A malformed response may echo a credential in a timestamp or JSON error.
		var status *stackDepsHTTPError
		if errors.As(err, &status) {
			return meta, status
		}
		return meta, fmt.Errorf("could not read push secret metadata")
	}
	if !strings.EqualFold(meta.Name, secret) || meta.UpdatedAt.IsZero() {
		return meta, fmt.Errorf("push secret metadata omitted name or updated_at")
	}
	return meta, nil
}

func (s *stackEdgeSetup) checkSecret(name string) error {
	slug, secret, err := s.secretLocation(name)
	if err != nil {
		return err
	}
	_, err = s.secretMetadata(slug, secret)
	return err
}

var stackEdgeSecretHTTP5xx = regexp.MustCompile(`(?i)\bHTTP(?:/\S+)?[ :()]+5[0-9][0-9]\b`)

func stackEdgeSetupExec(ctx context.Context, name string, args []string, input io.Reader) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = input
	return cmd.CombinedOutput()
}

var stackEdgeSetupNow = time.Now

func stackEdgeSetupSleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *stackEdgeSetup) pushCredential() (string, error) {
	if s.authRef != "" {
		cred, err := auth.Resolve(s.ctx, auth.Request{Ref: s.authRef})
		if err != nil || !cred.Resolved() {
			return "", fmt.Errorf("could not resolve push credential; check the configured auth reference")
		}
		return cred.Token, nil
	}
	fmt.Fprintln(s.out, "Read push credential from stdin (terminal input hidden; press Enter):")
	var data []byte
	var err error
	if file, ok := s.in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		data, err = term.ReadPassword(int(file.Fd()))
	} else {
		data, err = io.ReadAll(io.LimitReader(s.in, (1<<20)+1))
	}
	if err != nil || len(data) > 1<<20 {
		return "", fmt.Errorf("could not read push credential from stdin")
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("push credential from stdin is empty")
	}
	return value, nil
}

func (s *stackEdgeSetup) repairSecret(name string) error {
	slug, secret, err := s.secretLocation(name)
	if err != nil {
		return err
	}
	// A repeated init never overwrites an existing secret or resolves its value.
	if _, err = s.secretMetadata(slug, secret); err == nil {
		return nil
	} else if !stackEdgeMissing(err) {
		return err
	}
	if err := s.confirm("set push secret " + secret + " in " + slug + " from stdin or the publish-auth reference"); err != nil {
		return err
	}
	if _, err := s.secretMetadata(slug, secret); err == nil {
		fmt.Fprintf(s.out, "Another process created push secret %s in %s; leaving it unchanged.\n", secret, slug)
		return nil
	} else if !stackEdgeMissing(err) {
		return err
	}
	value, err := s.pushCredential()
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		// Credential input and retry delays can outlive the absence check.
		// GitHub's create-or-update API cannot make this check/write atomic.
		if _, err := s.secretMetadata(slug, secret); err == nil {
			fmt.Fprintf(s.out, "Another process created push secret %s in %s; leaving it unchanged.\n", secret, slug)
			return nil
		} else if !stackEdgeMissing(err) {
			return err
		}
		started := s.now().UTC()
		output, setErr := s.run(s.ctx, "gh", []string{"secret", "set", secret, "--repo", "github.com/" + slug}, strings.NewReader(value))
		if setErr == nil {
			meta, err := s.secretMetadata(slug, secret)
			if err != nil {
				return fmt.Errorf("push secret set could not be confirmed by metadata")
			}
			// GitHub reports whole seconds, so an update in the attempt's
			// second is fresh even when the attempt began mid-second.
			if meta.UpdatedAt.Before(started.Truncate(time.Second)) {
				return fmt.Errorf("push secret set was not confirmed: updated_at predates the attempt's second")
			}
			return nil
		}
		if attempt == 2 || !stackEdgeSecretHTTP5xx.Match(output) {
			return fmt.Errorf("gh secret set failed; credential and subprocess output withheld")
		}
		if err := s.sleep(s.ctx, time.Second<<attempt); err != nil {
			return fmt.Errorf("push secret retry cancelled")
		}
	}
	return fmt.Errorf("push secret set failed")
}
