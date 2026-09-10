//go:build !linux && !darwin && !windows

package commands

import (
	"fmt"
	"os"
)

func validateQueueRequestSingleLink(*os.File) error {
	return fmt.Errorf("queue request hard-link checks require Linux, macOS or Windows")
}
