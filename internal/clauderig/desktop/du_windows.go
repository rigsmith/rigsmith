//go:build windows

package desktop

import "io/fs"

// diskUsage is the space a file takes on disk. Windows does not carry an
// allocated-size in the stat Go exposes, so this is the logical length: an
// over-estimate for a sparse VM image, never an under-estimate.
func diskUsage(_ string, fi fs.FileInfo) int64 { return fi.Size() }
