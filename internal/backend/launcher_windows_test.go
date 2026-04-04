//go:build windows

package backend

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigureLauncherCommandHidesConsoleWindow(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "echo", "ok")

	configureLauncherCommand(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("expected SysProcAttr to be configured")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("expected HideWindow to be enabled")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("expected CREATE_NO_WINDOW flag to be set, got %#x", cmd.SysProcAttr.CreationFlags)
	}
}
