package velopack

import _ "embed"

// defaultDmgBackground is the install-DMG backdrop used when macos.dmgBackground
// is not configured — a soft gradient with a "drag to the Applications folder"
// arrow, so every Velopack app gets a presentable installer out of the box. Apps
// override it with their own (e.g. branded) art via macos.dmgBackground. It is a
// HiDPI TIFF (1×+2× representations) sized for a 640×400-point window.
//
//go:embed assets/dmg-background.tiff
var defaultDmgBackground []byte

// Logical size (points) the default backdrop is drawn for; the icons are placed
// over it at these coordinates.
const (
	defaultDmgWidth  = 640
	defaultDmgHeight = 400
)
