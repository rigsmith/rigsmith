package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/codexrig/rolloutstore"
)

// A symlink in a staging tree is not data the audit can scan. Once restore
// learned to skip symlinks, the listing that fed the audit dropped them too —
// so a link in a cloned tree passed the gate by never being shown to it.
func TestCheckPublishRefusesASymlinkInTheTree(t *testing.T) {
	stage := t.TempDir()
	if err := os.WriteFile(filepath.Join(stage, "config.toml"), []byte("model = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckPublish(stage); err != nil {
		t.Fatalf("a clean tree was refused: %v", err)
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(stage, "AGENTS.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := CheckPublish(stage); err == nil {
		t.Fatal("a symlink in the tree was not refused")
	}
	if !hasFinding(t, stage, "AGENTS.md", "not a regular file") {
		t.Error("the symlink is not named as a finding")
	}
}

func hasFinding(t *testing.T, stage, path, kind string) bool {
	t.Helper()
	findings, err := Audit(stage)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Path == path && f.Kind == kind {
			return true
		}
	}
	t.Logf("findings: %+v", findings)
	return false
}

// A .part with a well-formed name is exempt from the allowlist and from the
// audit's own scan because the index that references it is scanned instead.
// One that no index references is bytes nobody looked at.
func TestCheckPublishRefusesAPartNoIndexVouchesFor(t *testing.T) {
	stage := t.TempDir()
	rel := "cli/sessions/2026/09/05/rollout-2026-09-05T11-22-59-01a0722a-7356-7592-922a-336289bdc101.jsonl"
	p := filepath.Join(stage, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte(`{"type":"response_item","payload":{"text":"yyyyyyyy"}}`+"\n"), 200000)
	if err := rolloutstore.Write(p, bytes.NewReader(body), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := CheckPublish(stage); err != nil {
		t.Fatalf("a properly chunked rollout was refused: %v", err)
	}
	// An orphan with a perfectly good name, holding something no scan saw.
	orphan := filepath.Join(p+rolloutstore.Suffix, strings.Repeat("ab", 32)+".part")
	if err := os.WriteFile(orphan, []byte("ghp_"+strings.Repeat("a", 40)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckPublish(stage); err == nil {
		t.Fatal("an unreferenced part was not refused")
	}
	if !hasFinding(t, stage, rel+rolloutstore.Suffix+"/"+strings.Repeat("ab", 32)+".part", "chunk part no index references") {
		t.Error("the orphan part is not named as a finding")
	}
}

// Decode keeps the index fields it knows and drops the rest. A credential in
// an unknown field would be in the committed bytes and in nothing the logical
// read ever showed the scanner.
func TestCheckPublishScansTheRawIndexBytesToo(t *testing.T) {
	stage := t.TempDir()
	rel := "cli/sessions/2026/09/05/rollout-2026-09-05T11-22-59-01a0722a-7356-7592-922a-336289bdc101.jsonl"
	p := filepath.Join(stage, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte(`{"type":"response_item","payload":{"text":"yyyyyyyy"}}`+"\n"), 200000)
	if err := rolloutstore.Write(p, bytes.NewReader(body), time.Now()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// A field the index format does not define, holding something the
	// conversation never held.
	tampered := bytes.Replace(raw, []byte(`{"codexrig_chunked_rollout":`), []byte(`{"codexrig_chunked_rollout":`), 1)
	tampered = bytes.TrimRight(tampered, "\n}")
	tampered = append(tampered, []byte(`,"note":"ghp_`+strings.Repeat("a", 40)+`"}`+"\n")...)
	if err := os.WriteFile(p, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rolloutstore.Open(p); err != nil {
		t.Fatalf("fixture: the tampered index no longer decodes: %v", err)
	}
	if err := CheckPublish(stage); err == nil {
		t.Fatal("a credential in an unknown index field was published unscanned")
	}
}
