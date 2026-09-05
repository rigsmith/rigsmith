package merge

import "testing"

func TestChunkIndexesNeverLineUnion(t *testing.T) {
	a := []byte(`{"clauderig_chunked_transcript":1,"size":0,"parts":[]}`)
	b := []byte(`{"clauderig_chunked_transcript":1,"size":4,"parts":[]}`)
	if _, ok := Resolve(Sides{Path: "cli/projects/-p/s.jsonl", Ours: a, Theirs: b}); ok {
		t.Fatal("divergent indexes must remain unresolved")
	}
	if _, ok := Resolve(Sides{Path: "cli/projects/-p/s.jsonl", Ours: a, Theirs: []byte("native\n")}); ok {
		t.Fatal("mixed native/chunk conflict must remain unresolved")
	}
}
