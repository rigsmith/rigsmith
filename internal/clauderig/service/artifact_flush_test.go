package service_test

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

func TestCaptureArtifactScopesTranscriptFlush(t *testing.T) {
	for _, mode := range []string{queue.Normal, queue.Selected, queue.All, "multiple-normal", "mixed-all", "chunked"} {
		t.Run(mode, func(t *testing.T) {
			req := artifactCaptureFixture(t, "flush fixture")
			chunked := mode == "chunked"
			req.Sync.Config.ChunkTranscripts = &chunked
			req.Sync.Config.Retention.LargeFileBytes = 1024
			var err error
			req.Binding, err = service.CaptureBinding(req.Sync, nil)
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(req.Sync.Machine.Home, ".claude", "projects", "-workspace-acme")
			old := time.Now().Add(-5 * time.Minute)
			files := []string{"s.jsonl", "s/subagents/agent-s.jsonl", "extra.jsonl", "extra/subagents/agent-extra.jsonl", "other.jsonl", "other/subagents/agent-other.jsonl"}
			for _, rel := range files {
				id := strings.Split(strings.TrimSuffix(rel, ".jsonl"), "/")[0]
				body := fmt.Sprintf("{\"type\":\"user\",\"sessionId\":%q,\"uuid\":\"original\",\"cwd\":\"/workspace/acme\",\"message\":{\"role\":\"user\",\"content\":%q}}\n", id, strings.Repeat("fixture ", 400))
				put(t, root, rel, body)
				if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(rel)), old, old); err != nil {
					t.Fatal(err)
				}
			}
			svc := service.Service{ReadIdentity: func() (service.Identity, error) { return req.Identity, nil }}
			if _, err := svc.Capture(t.Context(), req.Sync); err != nil {
				t.Fatal(err)
			}
			for _, rel := range files {
				path := filepath.Join(root, filepath.FromSlash(rel))
				f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := fmt.Fprintln(f, `{"type":"progress","new_tail":true}`)
				closeErr := f.Close()
				if writeErr != nil || closeErr != nil {
					t.Fatal(writeErr, closeErr)
				}
				if err := os.Chtimes(path, old.Add(time.Minute), old.Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case queue.Selected:
				req.Work.Events[0].Request.Flush = queue.Flush{Mode: queue.Selected, Paths: []string{filepath.Join(root, "extra.jsonl")}}
			case queue.All:
				req.Work.Events[0].Request.Flush = queue.Flush{Mode: queue.All}
			case "multiple-normal", "mixed-all":
				second := req.Work.Events[0]
				second.Generation++
				second.Request.EventID = "event-b"
				second.Request.SessionID = "extra"
				second.Request.Flush = queue.Flush{Mode: queue.Normal}
				if mode == "mixed-all" {
					second.Request.Flush.Mode = queue.All
				}
				req.Work.Events = append(req.Work.Events, second)
				req.Work.Through = second.Generation
			}
			svc.ReadIdentity = func() (service.Identity, error) {
				t.Fatal("worker consulted live account")
				return service.Identity{}, nil
			}
			ref, err := svc.CaptureArtifact(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(t.TempDir(), "sealed")
			if err := req.Store.Extract(t.Context(), ref, dest); err != nil {
				t.Fatal(err)
			}
			for _, rel := range files {
				reader, err := transcript.Open(filepath.Join(dest, "cli", "projects", "-workspace-acme", filepath.FromSlash(rel)))
				if err != nil {
					t.Fatal(rel, err)
				}
				var body strings.Builder
				_, err = io.Copy(&body, reader)
				closeErr := reader.Close()
				if err != nil || closeErr != nil {
					t.Fatal(err, closeErr)
				}
				wantFresh := mode == queue.All || mode == "mixed-all" || chunked || strings.HasPrefix(rel, "s.") || strings.HasPrefix(rel, "s/") || ((mode == queue.Selected || mode == "multiple-normal") && strings.HasPrefix(rel, "extra"))
				if strings.Contains(body.String(), "new_tail") != wantFresh {
					t.Errorf("%s fresh=%t, want %t", rel, strings.Contains(body.String(), "new_tail"), wantFresh)
				}
			}
		})
	}
}
