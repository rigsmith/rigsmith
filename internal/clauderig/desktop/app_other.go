//go:build !darwin && !windows

package desktop

import "time"

// Anthropic ships Claude Desktop for macOS and Windows only. Rather than guess
// at a community build's install layout and process shape, this reports the
// platform as unsupported and says so once, clearly, at the command surface.
type unsupportedApp struct{}

func newApp() App { return unsupportedApp{} }

func (unsupportedApp) Installed() (string, bool)        { return "", false }
func (unsupportedApp) Launch(string) error              { return ErrUnsupported }
func (unsupportedApp) Running(string) ([]int, error)    { return nil, nil }
func (unsupportedApp) Focus(string) error               { return ErrUnsupported }
func (unsupportedApp) Quit(string, time.Duration) error { return ErrUnsupported }
func (unsupportedApp) OpenURL(string) error             { return ErrUnsupported }

func (unsupportedApp) RunningDefault() ([]int, error) { return nil, nil }
func (unsupportedApp) Instances() ([]Instance, error) { return nil, nil }

// Supported reports whether Anthropic ships Claude Desktop for this platform.
func Supported() bool { return false }
