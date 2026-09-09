//go:build !linux && !darwin && !windows

package process

import (
	"errors"
	"os/exec"
)

type ownership struct{}

func prepare(*exec.Cmd) (*ownership, error) {
	return nil, errors.New("queued process ownership unsupported on this platform")
}
func (*ownership) started(*exec.Cmd) error { return nil }
func (*ownership) wait() error             { return nil }
func (*ownership) stop() error             { return nil }
func (*ownership) finish() error           { return nil }
func (*ownership) close() error            { return nil }
