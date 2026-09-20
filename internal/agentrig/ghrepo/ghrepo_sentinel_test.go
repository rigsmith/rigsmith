package ghrepo

import (
	"context"
	"errors"
	"testing"
)

// EnsurePrivate refuses everything it cannot confirm, which is right for a gate
// but leaves callers that REPORT unable to tell "cannot check" from "checked,
// and it is public". These sentinels are that distinction, and a reporter that
// gets them wrong softens a genuinely public repo into a warning.

func TestUnverifiableRemotesCarryErrUnsupportedRemote(t *testing.T) {
	ctx := context.Background()
	for _, remote := range []string{
		"/var/tmp/local/repo.git",              // a local path
		"https://git.example.com/you/repo.git", // a self-hosted host
		"not a url at all",
	} {
		err := EnsurePrivate(ctx, remote)
		if err == nil {
			t.Errorf("EnsurePrivate(%q) = nil, want a refusal", remote)
			continue
		}
		if !errors.Is(err, ErrUnsupportedRemote) {
			t.Errorf("EnsurePrivate(%q) = %v, want it to wrap ErrUnsupportedRemote so a "+
				"reporter can say \"cannot verify\" rather than \"public\"", remote, err)
		}
	}
}

// The bug this pins: gitlab.com remotes are supported, so they must NOT look
// unsupported. Gating on the GitHub-only ParseSlug made every GitLab remote —
// including a verifiably public one — take the "cannot verify" path.
func TestSupportedHostsAreNotReportedAsUnsupported(t *testing.T) {
	ctx := context.Background()
	for _, remote := range []string{
		"https://gitlab.com/you/repo.git",
		"git@gitlab.com:you/repo.git",
		"https://gitlab.com/group/subgroup/repo.git", // subgroups are valid GitLab
		"https://github.com/you/repo.git",
	} {
		err := EnsurePrivate(ctx, remote)
		if err != nil && errors.Is(err, ErrUnsupportedRemote) {
			t.Errorf("EnsurePrivate(%q) reported a SUPPORTED host as unsupported (%v); a "+
				"public repo there would be softened into a warning", remote, err)
		}
	}
}

// A supported host with no CLI and no token is "cannot check", not "public".
func TestMissingVerifierIsItsOwnAnswer(t *testing.T) {
	for _, k := range []string{"GITLAB_TOKEN", "GL_TOKEN"} {
		t.Setenv(k, "")
	}
	if have("glab") {
		t.Skip("glab is installed, so this machine can verify GitLab remotes")
	}
	err := EnsurePrivate(context.Background(), "https://gitlab.com/you/repo.git")
	if err == nil {
		t.Fatal("EnsurePrivate succeeded with no way to verify")
	}
	if !errors.Is(err, ErrVerifierUnavailable) {
		t.Errorf("err = %v, want it to wrap ErrVerifierUnavailable", err)
	}
	if errors.Is(err, ErrUnsupportedRemote) {
		t.Error("a missing verifier was reported as an unsupported remote")
	}
}

// The checker running and failing is a third answer, distinct from both "no
// checker" and "checked, it is public". An expired gh login must not be
// reported as a public repo.
func TestAFailingCheckerIsUnavailableNotPublic(t *testing.T) {
	boom := errors.New("gh: authentication token expired")
	err := verify(context.Background(), "you/repo", func(context.Context, string) (bool, error) {
		return false, boom
	})

	if err == nil {
		t.Fatal("verify succeeded although the checker failed")
	}
	if !errors.Is(err, ErrVerifierUnavailable) {
		t.Errorf("err = %v, want it to wrap ErrVerifierUnavailable; otherwise a reporter "+
			"tells the user their private repo is public because their login expired", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the underlying cause preserved too", err)
	}
}

// The control: a checker that successfully determines the repo is public is a
// genuine failure and must NOT be softened into "cannot verify".
func TestAConfirmedPublicRepoIsNotUnavailable(t *testing.T) {
	err := verify(context.Background(), "you/repo", func(context.Context, string) (bool, error) {
		return false, nil
	})

	if err == nil {
		t.Fatal("verify accepted a public repo")
	}
	if errors.Is(err, ErrVerifierUnavailable) {
		t.Errorf("a confirmed-public repo was reported as unverifiable (%v), which would "+
			"downgrade the one case the check exists for", err)
	}
}
