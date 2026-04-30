//go:build windows

package tui

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
)

func configureProcessTree(_ *exec.Cmd) {}

func cancelProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	err := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
	if err == nil {
		return nil
	}
	if killErr := cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		return killErr
	}
	return nil
}
