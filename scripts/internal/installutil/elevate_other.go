//go:build !windows

package installutil

func shouldElevate(error) bool { return false }

func relaunchElevated() error {
	return nil
}
