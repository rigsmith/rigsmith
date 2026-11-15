package selfupdate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Every release publishes these beside its archives (.goreleaser.yaml):
// checksums.txt lists the SHA-256 of install.sh and of every archive, and
// checksums.txt.sigstore.json is a Sigstore bundle signing checksums.txt with
// the release workflow's GitHub OIDC identity (cosign keyless — no key to keep).
const (
	ChecksumsAsset = "checksums.txt"
	SignatureAsset = "checksums.txt.sigstore.json"
	InstallerAsset = "install.sh"
)

// RequireSignatureEnv set to 1 refuses to install when the signature can't be
// checked because cosign isn't installed, instead of warning and going on with
// the checksums alone. install.sh reads the same variable.
const RequireSignatureEnv = "RIGSMITH_REQUIRE_SIGNATURE"

// ErrNotFound is a release asset that isn't there.
var ErrNotFound = errors.New("not found")

// ReleaseAssetURL is the download URL of a file attached to a release. Pure.
func ReleaseAssetURL(repo, tag, name string) string {
	return "https://github.com/" + repo + "/releases/download/" + tag + "/" + name
}

// SigningIdentity is the certificate identity a release's signature must carry:
// the release workflow of repo, at any ref (a tag push or a manual run). Pure.
func SigningIdentity(repo string) string {
	return "^https://github.com/" + regexp.QuoteMeta(repo) + `/\.github/workflows/release\.yml@`
}

// SigningIssuer is the OIDC issuer of GitHub Actions identities.
const SigningIssuer = "https://token.actions.githubusercontent.com"

// VerifyChecksum checks content against the SHA-256 checksums lists for name,
// in the `sha256sum` format GoReleaser writes ("<hex>  <name>"). Pure.
func VerifyChecksum(checksums []byte, name string, content []byte) error {
	want := ""
	sc := bufio.NewScanner(bytes.NewReader(checksums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			want = strings.ToLower(fields[0])
			break
		}
	}
	if want == "" {
		return fmt.Errorf("%s is not listed in %s", name, ChecksumsAsset)
	}
	sum := sha256.Sum256(content)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("%s does not match %s (expected %s, got %s)", name, ChecksumsAsset, want, got)
	}
	return nil
}

// verifiedInstaller downloads tag's checksums, its signature and install.sh,
// and returns the script only once all of it checks out. Nothing is run here.
func verifiedInstaller(out io.Writer, e Env, tag string) ([]byte, error) {
	refuse := func(format string, a ...any) error {
		return fmt.Errorf("not updating: "+format, a...)
	}
	sums, err := e.Fetch(ReleaseAssetURL(e.Repo, tag, ChecksumsAsset))
	if err != nil {
		return nil, refuse("couldn't download %s for %s: %v", ChecksumsAsset, tag, err)
	}
	bundle, err := e.Fetch(ReleaseAssetURL(e.Repo, tag, SignatureAsset))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, refuse("%s has no signature for its %s (%s is missing)", tag, ChecksumsAsset, SignatureAsset)
		}
		return nil, refuse("couldn't download %s for %s: %v", SignatureAsset, tag, err)
	}
	checked, err := e.VerifySignature(sums, bundle)
	switch {
	case err != nil:
		return nil, refuse("the signature on %s for %s does not verify: %v", ChecksumsAsset, tag, err)
	case checked:
		fmt.Fprintln(out, dim.Render("verified the signature on "+ChecksumsAsset+" ("+tag+")"))
	case e.RequireSignature:
		return nil, refuse("cosign isn't installed, so the signature on %s can't be checked, and %s=1 requires it", ChecksumsAsset, RequireSignatureEnv)
	default:
		fmt.Fprintln(out, "warning: cosign isn't installed, so only the checksums are checked, not the signature on "+ChecksumsAsset+".")
		fmt.Fprintln(out, dim.Render("install cosign (https://docs.sigstore.dev/cosign/system_config/installation/) to verify it, or set "+RequireSignatureEnv+"=1 to refuse without it"))
	}
	script, err := e.Fetch(ReleaseAssetURL(e.Repo, tag, InstallerAsset))
	if err != nil {
		return nil, refuse("couldn't download %s for %s: %v", InstallerAsset, tag, err)
	}
	if err := VerifyChecksum(sums, InstallerAsset, script); err != nil {
		return nil, refuse("%v", err)
	}
	return script, nil
}

// fetchAsset GETs url, reporting a 404 as ErrNotFound.
func fetchAsset(url string) ([]byte, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub responded %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// cosignVerify checks bundle against checksums with cosign, for repo's release
// workflow identity. checked is false, with no error, when cosign isn't on PATH.
func cosignVerify(ctx context.Context, repo string, checksums, bundle []byte) (checked bool, err error) {
	cosign, err := exec.LookPath("cosign")
	if err != nil {
		return false, nil
	}
	dir, err := os.MkdirTemp("", "rigsmith-verify-*")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dir)
	sumsPath, bundlePath := filepath.Join(dir, ChecksumsAsset), filepath.Join(dir, SignatureAsset)
	if err := os.WriteFile(sumsPath, checksums, 0o600); err != nil {
		return false, err
	}
	if err := os.WriteFile(bundlePath, bundle, 0o600); err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := exec.CommandContext(ctx, cosign, "verify-blob",
		"--bundle", bundlePath,
		"--certificate-identity-regexp", SigningIdentity(repo),
		"--certificate-oidc-issuer", SigningIssuer,
		sumsPath).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return true, nil
}

// stageScript writes a verified installer to a private temp file to run it from.
func stageScript(script []byte) (string, func(), error) {
	dir, err := os.MkdirTemp("", "rigsmith-install-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	path := filepath.Join(dir, InstallerAsset)
	if err := os.WriteFile(path, script, 0o700); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}
