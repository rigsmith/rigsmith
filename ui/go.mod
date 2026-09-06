// The window is its own module so it can carry its own version. It is a
// different product from the command line tools beside it — new, moving fast,
// and at 0.x while they are at 1.x — and a single version line would either
// hold it back or drag them forward.
//
// It still lives in this repository and imports its internal packages: the Go
// internal rule is about import paths, not module boundaries, and
// github.com/rigsmith/rigsmith/ui is under github.com/rigsmith/rigsmith.
// go.work at the repo root is what resolves that locally.
module github.com/rigsmith/rigsmith/ui // rigsmith:version 0.1.0

go 1.26.7

require github.com/wailsapp/wails/v3 v3.0.0-beta.15

require (
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	golang.org/x/sys v0.46.0 // indirect
)
