package mergepolicy

import "testing"

func TestResolveLeavesChunkIndexConflictsUnresolved(t *testing.T) {
	const p = "cli/projects/-p/s.jsonl"
	base := `{"type":"user","text":"base"}` + "\n"
	ours := `{"clauderig_chunked_transcript":1,"size":0,"parts":[]}` + "\n"
	theirs := `{"type":"user","text":"remote turn"}` + "\n"
	_, repo := diverged(t, map[string]string{p: base}, map[string]string{p: ours}, map[string]string{p: theirs})
	rep, err := Resolve(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Resolved) != 0 || len(rep.Unresolved) != 1 || rep.Unresolved[0] != p {
		t.Fatalf("chunk/native conflict incorrectly resolved: %+v", rep)
	}
	if paths, err := repo.Conflicts(t.Context()); err != nil || len(paths) != 1 {
		t.Fatalf("conflict not preserved: %v %v", paths, err)
	}
}
