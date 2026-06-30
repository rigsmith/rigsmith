package dsstore

import (
	"bytes"
	"testing"
)

// TestBuildStoreRoundTrips builds the install-window records, writes them through
// the container, reads them back, and checks the structure survives — the app +
// Applications are positioned where asked and .background is parked off-screen.
func TestBuildStoreRoundTrips(t *testing.T) {
	lay := Layout{
		WindowWidth: 640, WindowHeight: 400, IconSize: 128,
		AppName: "Foo.app", BgFile: "background.tiff",
		AppX: 160, AppY: 200, AppsX: 480, AppsY: 200, HiddenY: 700,
	}
	s := buildStore("MyVol", "background.tiff", lay, 453, 452, 1700000000, 1690000000)

	var buf bytes.Buffer
	if err := s.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	var got Store
	if err := got.Read(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("read: %v", err)
	}

	if len(got.Records) != 6 {
		t.Fatalf("got %d records, want 6: %+v", len(got.Records), got.Records)
	}
	pos := map[string][2]int{}
	seen := map[string]bool{}
	for _, r := range got.Records {
		seen[r.FileName] = true
		if r.Extra == fourCC("Iloc") {
			x := int(int32(uint32(r.Data[0])<<24 | uint32(r.Data[1])<<16 | uint32(r.Data[2])<<8 | uint32(r.Data[3])))
			y := int(int32(uint32(r.Data[4])<<24 | uint32(r.Data[5])<<16 | uint32(r.Data[6])<<8 | uint32(r.Data[7])))
			pos[r.FileName] = [2]int{x, y}
		}
	}
	for _, fn := range []string{".", ".background", "Applications", "Foo.app"} {
		if !seen[fn] {
			t.Errorf("missing record for %q", fn)
		}
	}
	if pos["Foo.app"] != [2]int{160, 200} {
		t.Errorf("Foo.app pos = %v, want [160 200]", pos["Foo.app"])
	}
	if pos["Applications"] != [2]int{480, 200} {
		t.Errorf("Applications pos = %v, want [480 200]", pos["Applications"])
	}
	if pos[".background"][1] != 700 {
		t.Errorf(".background should be parked at y=700 (off-screen), got %v", pos[".background"])
	}
}

// TestBackgroundAliasStructure checks the Alias Manager v2 header + that the
// resolution-critical paths are present (so it resolves by volume name + path).
func TestBackgroundAliasStructure(t *testing.T) {
	a := buildBackgroundAlias("MyVol", ".background", "background.tiff", 1690000000, 1700000000, 452, 453)
	if len(a) < 150 {
		t.Fatalf("alias too short: %d", len(a))
	}
	if rec := int(a[4])<<8 | int(a[5]); rec != len(a) {
		t.Errorf("record size field = %d, want %d (len)", rec, len(a))
	}
	if a[7] != 2 {
		t.Errorf("alias version = %d, want 2", a[7])
	}
	for _, want := range []string{"MyVol", "background.tiff", "/Volumes/MyVol", "/.background/background.tiff"} {
		if !bytes.Contains(a, []byte(want)) {
			t.Errorf("alias missing %q", want)
		}
	}
}

func TestEncodeBplistAndIloc(t *testing.T) {
	b := encodeBplist(map[string]any{"k": 1, "f": 1.0, "s": "x", "ok": true})
	if !bytes.HasPrefix(b, []byte("bplist00")) {
		t.Errorf("not a binary plist: % x", b[:8])
	}
	if d := ilocData(160, 200); len(d) != 16 {
		t.Errorf("Iloc data len = %d, want 16", len(d))
	}
	if fourCC("Iloc") != 0x496c6f63 {
		t.Errorf("fourCC(Iloc) = %#x", fourCC("Iloc"))
	}
}
