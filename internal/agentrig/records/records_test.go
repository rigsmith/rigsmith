package records_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/records"
)

type store struct {
	rows  map[string]records.Summary
	saves int
}

func (s *store) Fresh(id string, at time.Time, size int64) bool {
	r, ok := s.rows[id]
	return ok && r.Activity.Equal(at) && r.Bytes == size
}
func (s *store) Note(r records.Summary) bool { s.rows[r.ID] = r; return true }
func (s *store) Revoke(id string) bool {
	r, ok := s.rows[id]
	if !ok || r.Account == "" {
		return false
	}
	r.Account = ""
	s.rows[id] = r
	return true
}
func (s *store) Count() int  { return len(s.rows) }
func (s *store) Save() error { s.saves++; return nil }

func TestRecordSkipsFreshHydrationAndHonorsPolicyRefresh(t *testing.T) {
	at := time.Unix(1700000000, 0)
	s := &store{rows: map[string]records.Summary{
		"opaque/ID": {ID: "opaque/ID", Activity: at, Bytes: 12, Title: "kept"},
		"refresh":   {ID: "refresh", Activity: at, Bytes: 12},
		"contested": {ID: "contested", Account: "account-without-uuid"},
	}}
	var reads []string
	result, err := records.Record(s, func(yield func(records.Candidate) error) error {
		for _, c := range []records.Candidate{
			{Summary: records.Summary{ID: "opaque/ID", Activity: at, Bytes: 12}, Read: func(*records.Summary) error { t.Fatal("read fresh row"); return nil }},
			{Summary: records.Summary{ID: "refresh", Activity: at, Bytes: 12}, Refresh: true, Read: func(r *records.Summary) error { reads = append(reads, r.ID); r.Account = "better-evidence"; return nil }},
			{Summary: records.Summary{ID: "contested"}, Revoke: true, Read: func(*records.Summary) error { t.Fatal("read revoked row"); return nil }},
			{Summary: records.Summary{ID: "new", Title: "new session"}},
		} {
			if err := yield(c); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil || result.Added != 3 || result.Total != 4 || s.saves != 1 {
		t.Fatalf("record: %+v, %v, saves %d", result, err, s.saves)
	}
	if !reflect.DeepEqual(reads, []string{"refresh"}) || s.rows["opaque/ID"].Title != "kept" || s.rows["contested"].Account != "" || s.rows["refresh"].Account != "better-evidence" {
		t.Fatalf("rows: %+v; reads %v", s.rows, reads)
	}
}
func TestRecordFailureDoesNotSavePartialWalk(t *testing.T) {
	for _, where := range []string{"read", "walk"} {
		t.Run(where, func(t *testing.T) {
			s := &store{rows: map[string]records.Summary{}}
			want := errors.New("reader failed")
			result, err := records.Record(s, func(yield func(records.Candidate) error) error {
				if err := yield(records.Candidate{Summary: records.Summary{ID: "first"}}); err != nil {
					return err
				}
				if where == "walk" {
					return want
				}
				return yield(records.Candidate{Summary: records.Summary{ID: "second"}, Read: func(*records.Summary) error { return want }})
			})
			if !errors.Is(err, want) || s.saves != 0 || result.Added != 1 || result.Total != 1 {
				t.Fatalf("partial: %+v, %v, saves %d", result, err, s.saves)
			}
		})
	}
}
func TestQueriesKeepTitleAndVisibleFieldsSeparate(t *testing.T) {
	row := records.Summary{ID: "Opaque/Id", Title: "Plan Release", Cwd: "/work/api", LastPrompt: "check tests", Branch: "feature", Client: "editor", Account: "hidden-account"}
	if !records.TitleMatches(row, "release", false) || records.TitleMatches(row, "release", true) || records.TitleMatches(row, "/work", false) || records.TitleMatches(records.Summary{}, "", false) {
		t.Fatal("title semantics changed")
	}
	for _, query := range []string{"", "  ", " tests ", "OPAQUE/ID", "/work/api", "FEATURE", "EDITOR"} {
		if !records.MatchesText(row, query) {
			t.Errorf("missed %q", query)
		}
	}
	if records.MatchesText(row, "hidden-account") {
		t.Fatal("unselected attribution became searchable")
	}
}

func TestTitleQueryRejectsEmptyButKeepsLiteralWhitespace(t *testing.T) {
	row := records.Summary{Title: "Plan Release"}
	for _, caseSensitive := range []bool{false, true} {
		if records.TitleMatches(row, "", caseSensitive) {
			t.Fatal("empty title query matched a nonempty title")
		}
		if !records.TitleMatches(row, " ", caseSensitive) {
			t.Fatal("literal whitespace query was trimmed")
		}
	}
	if !records.MatchesText(row, "") {
		t.Fatal("blank list filter must still include rows")
	}
}
