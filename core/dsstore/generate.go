package dsstore

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
)

// Layout describes the install-window arrangement baked into the .DS_Store. All
// coordinates are icon-view points; positions are icon centres.
type Layout struct {
	WindowWidth, WindowHeight int    // window content size
	IconSize                  int    // icon size, e.g. 128
	AppName                   string // "<App>.app"
	BgFile                    string // background filename in .background/, e.g. "background.tiff"
	AppX, AppY                int    // app icon centre
	AppsX, AppsY              int    // Applications icon centre
	HiddenY                   int    // y to park .background below the visible row
}

// Write generates a complete drag-to-install .DS_Store and writes it into
// mountPoint (a mounted, writable HFS+ volume). The background image must already
// exist at mountPoint/.background/<lay.BgFile>. No Finder/AppleScript is used, so
// it works headless (CI). The background alias is minted for volName (the dmg's
// canonical volume name, independent of the build-time mount name), so it resolves
// on any machine that mounts the dmg under that name.
func Write(mountPoint, volName string, lay Layout) error {
	bgName := lay.BgFile
	if bgName == "" {
		bgName = "background.tiff"
	}
	bgDir := filepath.Join(mountPoint, ".background")
	bgFile := filepath.Join(bgDir, bgName)
	fileIno, fileBirth, err := statInfo(bgFile)
	if err != nil {
		return err
	}
	dirIno, _, err := statInfo(bgDir)
	if err != nil {
		return err
	}
	_, volBirth, err := statInfo(mountPoint)
	if err != nil {
		return err
	}

	s := buildStore(volName, bgName, lay, fileIno, dirIno, fileBirth, volBirth)
	return s.WriteFile(filepath.Join(mountPoint, ".DS_Store"), 0o644)
}

// buildStore assembles the .DS_Store records from already-resolved metadata. Split
// out from Write so it is unit-testable without a mounted volume.
func buildStore(volName, bgName string, lay Layout, fileIno, dirIno uint32, fileBirth, volBirth int64) *Store {
	alias := buildBackgroundAlias(volName, ".background", bgName, volBirth, fileBirth, dirIno, fileIno)

	icvp := encodeBplist(map[string]any{
		"viewOptionsVersion": 1, "backgroundType": 2,
		"backgroundColorRed": 1.0, "backgroundColorGreen": 1.0, "backgroundColorBlue": 1.0,
		"backgroundImageAlias": alias,
		"iconSize":             float64(lay.IconSize), "arrangeBy": "none", "gridSpacing": 100.0,
		"gridOffsetX": 0.0, "gridOffsetY": 0.0, "textSize": 12.0,
		"labelOnBottom": true, "showIconPreview": true, "showItemInfo": false,
	})
	bwsp := encodeBplist(map[string]any{
		"ShowStatusBar": false, "ShowToolbar": false, "ShowTabView": false,
		"ShowSidebar": false, "ContainerShowSidebar": false,
		"WindowBounds": fmt.Sprintf("{{200, 120}, {%d, %d}}", lay.WindowWidth, lay.WindowHeight),
	})

	return &Store{Records: []Record{
		blobRec(".", "bwsp", bwsp),
		blobRec(".", "icvp", icvp),
		{FileName: ".", Extra: fourCC("vSrn"), Type: "long", DataLen: 0, Data: []byte{0, 0, 0, 1}},
		blobRec(".background", "Iloc", ilocData(lay.AppsX, lay.HiddenY)), // hidden: parked below
		blobRec("Applications", "Iloc", ilocData(lay.AppsX, lay.AppsY)),
		blobRec(lay.AppName, "Iloc", ilocData(lay.AppX, lay.AppY)),
	}}
}

func fourCC(s string) uint32 { return binary.BigEndian.Uint32([]byte(s)) }

func blobRec(name, structID string, data []byte) Record {
	return Record{FileName: name, Extra: fourCC(structID), Type: "blob", DataLen: uint32(len(data)), Data: data}
}

func ilocData(x, y int) []byte {
	b := make([]byte, 16)
	binary.BigEndian.PutUint32(b[0:], uint32(x))
	binary.BigEndian.PutUint32(b[4:], uint32(y))
	copy(b[8:], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00, 0x00})
	return b
}

// statInfo (inode/CNID + creation time) is platform-specific; see stat_darwin.go
// and stat_other.go.
