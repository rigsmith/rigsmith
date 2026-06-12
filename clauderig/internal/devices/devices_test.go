package devices

import (
	"testing"
	"time"
)

func TestRegistry_TouchSaveLoadList(t *testing.T) {
	dir := t.TempDir()

	// absent → empty
	r, err := Load(dir)
	if err != nil || len(r.Devices) != 0 {
		t.Fatalf("empty load: %v %+v", err, r)
	}

	t0 := time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	r.Touch("work-pc", "windows", "2.1.170", t0)
	r.Touch("mbp", "macos", "2.1.175", t1)
	if err := r.Save(dir); err != nil {
		t.Fatal(err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Devices) != 2 {
		t.Fatalf("want 2 devices, got %d", len(got.Devices))
	}
	if got.Devices["mbp"].ClaudeVersion != "2.1.175" {
		t.Errorf("mbp version = %q", got.Devices["mbp"].ClaudeVersion)
	}
	// List is most-recent first
	list := got.List()
	if list[0].Name != "mbp" || list[1].Name != "work-pc" {
		t.Fatalf("List order = %v", []string{list[0].Name, list[1].Name})
	}
	// re-touch updates in place (no duplicate)
	got.Touch("mbp", "macos", "2.1.176", t1.Add(time.Hour))
	if len(got.Devices) != 2 || got.Devices["mbp"].ClaudeVersion != "2.1.176" {
		t.Errorf("re-touch should update in place: %+v", got.Devices)
	}
}
