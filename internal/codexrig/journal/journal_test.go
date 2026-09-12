package journal

import "testing"

// "a/b" and "a-b" sanitised to one file, and a refusal now publishes one
// machine's file as its own — so two machines could publish each other's.
func TestSanitisedMachineNamesDoNotShareAJournal(t *testing.T) {
	if RelPathFor("a/b") == RelPathFor("a-b") {
		t.Errorf("two machines map to %s", RelPathFor("a/b"))
	}
	if RelPathFor("mbp") != DirName+"/mbp.jsonl" {
		t.Errorf("an ordinary name moved: %s", RelPathFor("mbp"))
	}
}
