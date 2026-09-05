package devices

import (
	"os"
	"path/filepath"
	"strings"
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
	r.Touch("work-pc", "windows", "2.1.170", nil, t0)
	r.Touch("mbp", "macos", "2.1.175", &Account{AccountUUID: "acct-1", OrganizationUUID: "org-1", Email: "john@example.com"}, t1)
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
	got.Touch("mbp", "macos", "2.1.176", nil, t1.Add(time.Hour))
	if len(got.Devices) != 2 || got.Devices["mbp"].ClaudeVersion != "2.1.176" {
		t.Errorf("re-touch should update in place: %+v", got.Devices)
	}
}

// The account a device synced as is the repo's only account provenance — CLI
// transcripts carry no account field at all — so it has to survive the round
// trip, and a later sync that can't read the identity must not erase it.
func TestTouch_RecordsAccountAndKeepsItWhenUnknown(t *testing.T) {
	dir := t.TempDir()
	when := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	acct := &Account{AccountUUID: "456fc32e", OrganizationUUID: "f1eab509", Email: "john@example.com"}

	r, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.Touch("mbp", "macos", "2.1.237", acct, when)
	if err := r.Save(dir); err != nil {
		t.Fatal(err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a := got.Devices["mbp"].Account; a == nil || *a != *acct {
		t.Fatalf("account did not survive the round trip: %+v", got.Devices["mbp"].Account)
	}

	// A sync that couldn't read the identity is not evidence of a switch.
	got.Touch("mbp", "macos", "2.1.238", nil, when.Add(time.Hour))
	if a := got.Devices["mbp"].Account; a == nil || a.Email != "john@example.com" {
		t.Errorf("nil acct erased the recorded account: %+v", got.Devices["mbp"].Account)
	}

	// A real switch does replace it.
	other := &Account{AccountUUID: "03d1c0c9", OrganizationUUID: "e3055f13", Email: "john@other.com"}
	got.Touch("mbp", "macos", "2.1.238", other, when.Add(2*time.Hour))
	if a := got.Devices["mbp"].Account; a == nil || a.Email != "john@other.com" {
		t.Errorf("a real switch should replace the account: %+v", got.Devices["mbp"].Account)
	}
}

// Only the three identity fields travel — never the plan, tier, or anything
// else that shares the oauthAccount block with them.
func TestAccount_SerialisesIdentityFieldsOnly(t *testing.T) {
	dir := t.TempDir()
	r, _ := Load(dir)
	r.Touch("mbp", "macos", "2.1.237",
		&Account{AccountUUID: "a", OrganizationUUID: "o", Email: "e@x.com"},
		time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC))
	if err := r.Save(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"seatTier", "rateLimitTier", "accessToken", "refreshToken", "billingType", "organizationName", "subscription"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("registry leaked non-identity field %q", banned)
		}
	}
	for _, want := range []string{"accountUuid", "organizationUuid", "email"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("registry missing identity field %q", want)
		}
	}
}

// The registry had no removal path at all, which is why a ghost device named
// "this" survived in the synced file from June 2026 until it was deleted by
// hand on 2026-08-07.
func TestRemove(t *testing.T) {
	dir := t.TempDir()
	reg := &Registry{Schema: schemaVersion, Devices: map[string]Device{}}
	reg.Touch("Air13", "macos", "2.1.212", nil, time.Now())
	reg.Touch("this", "macos", "", nil, time.Now()) // the ghost

	if !reg.Has("this") {
		t.Fatal("fixture did not register the ghost")
	}
	if !reg.Remove("this") {
		t.Fatal("Remove reported the ghost was absent")
	}
	if reg.Has("this") {
		t.Error("ghost survived removal")
	}
	if !reg.Has("Air13") {
		t.Error("removal took an unrelated device with it")
	}

	// Removing something absent is a no-op, not an error — the command layer
	// turns "not there" into a clear message before ever calling this.
	if reg.Remove("never-existed") {
		t.Error("Remove claimed to remove a device that was not registered")
	}

	// It survives the round trip, so the removal actually syncs.
	if err := reg.Save(dir); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Has("this") {
		t.Error("ghost came back after save/load")
	}
	if !reloaded.Has("Air13") {
		t.Error("real device lost in save/load")
	}
}

// Remove on a freshly-constructed registry must not panic on a nil map.
func TestRemoveNilMap(t *testing.T) {
	reg := &Registry{}
	if reg.Remove("anything") {
		t.Error("Remove on an empty registry reported success")
	}
	if reg.Has("anything") {
		t.Error("Has on an empty registry reported true")
	}
}
