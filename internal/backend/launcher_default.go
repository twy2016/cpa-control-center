//go:build !windows

package backend

import (
	"os/exec"
	"strings"
	"syscall"
)

func configureLauncherCommand(cmd *exec.Cmd) {
}

func setLauncherWindowsStartup(enabled bool) error {
	return nil
}

func findLauncherProcessPID(executablePath string, configPath string) (int, error) {
	return 0, nil
}

func findLauncherProcessPIDByExecutablePath(executablePath string) (int, error) {
	return 0, nil
}

func launcherCommandLineArguments(commandLine string) ([]string, error) {
	return strings.Fields(commandLine), nil
}

func killProcessTreeByPID(pid int) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
