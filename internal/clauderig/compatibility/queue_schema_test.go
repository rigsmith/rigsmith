package compatibility

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
)

// Last merged v2 before receipt compaction; its actual reader supports schemas
// 1 and 2. This is separate from the unchanged v1 command-compatibility baseline.
const preCompactionRef = "4ccf5e5e59f9b92576e40ffa1d50a2984d6e417f"

func TestLegacyQueueReaderRejectsCompaction(t *testing.T) {
	if os.Getenv("CLAUDERIG_COMPAT") != "1" {
		t.Skip("set CLAUDERIG_COMPAT=1; requires the pinned pre-compaction v2 revision")
	}
	repo := strings.TrimSpace(command(t, "", nil, "git", "rev-parse", "--show-toplevel"))
	src := exportSource(t, repo, preCompactionRef, "go.mod", "go.sum",
		"internal/agentrig/queue/queue.go", "internal/agentrig/queue/storage.go",
		"internal/agentrig/storelock", "internal/agentrig/durable")
	probeDir := filepath.Join(src, "cmd", "legacyqueueprobe")
	must(t, os.MkdirAll(probeDir, 0700))
	must(t, os.WriteFile(filepath.Join(probeDir, "main.go"), []byte(legacyQueueProbe), 0600))
	bin := filepath.Join(t.TempDir(), "legacyqueueprobe")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	command(t, src, nil, "go", "build", "-o", bin, "./cmd/legacyqueueprobe")
	for _, compact := range []bool{false, true} {
		t.Run(map[bool]string{false: "schema-2-readable", true: "schema-3-refused"}[compact], func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "queue")
			binding := queue.Binding{Vendor: "fixture", StoreID: "store", RootID: "root", RemoteID: "remote", ConfigID: "config"}
			q, err := queue.Create(t.Context(), dir, binding)
			must(t, err)
			at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
			r := queue.Request{EventID: "done", SessionID: "session", ProvenanceID: "origin", Flush: queue.Flush{Mode: queue.Normal}}
			_, err = q.Enqueue(t.Context(), r, at)
			must(t, err)
			w, err := q.Worker(t.Context())
			must(t, err)
			b, err := w.Next(t.Context(), at)
			must(t, err)
			must(t, w.Progress(t.Context(), b.ID, queue.Captured, "capture"))
			must(t, w.Progress(t.Context(), b.ID, queue.Committed, "commit"))
			must(t, w.Progress(t.Context(), b.ID, queue.Pushed, ""))
			must(t, w.Acknowledge(t.Context(), b.ID))
			r.EventID = "pending"
			_, err = q.Enqueue(t.Context(), r, at)
			must(t, err)
			w.Close()
			want := "supported"
			if compact {
				_, err = q.CompactReceipts(t.Context(), at.Add(time.Hour))
				must(t, err)
				want = "unsupported"
			}
			before, err := os.ReadFile(filepath.Join(dir, "queue.json"))
			must(t, err)
			identity, err := json.Marshal(binding)
			must(t, err)
			command(t, repo, nil, bin, dir, string(identity), want)
			after, err := os.ReadFile(filepath.Join(dir, "queue.json"))
			must(t, err)
			if !bytes.Equal(before, after) {
				t.Fatal("legacy reader changed queue bytes")
			}
			jobs, err := q.Snapshot(t.Context())
			must(t, err)
			if len(jobs) != 1 || jobs[0].Events[0].Request.EventID != "pending" {
				t.Fatal("legacy reader changed pending work", jobs)
			}
		})
	}
}

const legacyQueueProbe = `package main
import (
 "context"
 "encoding/json"
 "fmt"
 "os"
 "strings"
 "github.com/rigsmith/rigsmith/internal/agentrig/queue"
)
func main() {
 var binding queue.Binding
 if err := json.Unmarshal([]byte(os.Args[2]), &binding); err != nil { panic(err) }
 for _, open := range []func(context.Context,string,queue.Binding)(*queue.Queue,error){queue.Open,queue.Create} {
  _, err := open(context.Background(),os.Args[1],binding)
  if os.Args[3] == "unsupported" {
   if err == nil || !strings.Contains(err.Error(),"unsupported queue schema 3") { panic(fmt.Sprintf("legacy reader failed to reject schema 3: %v",err)) }
  } else if err != nil { panic(err) }
 }
}
`
