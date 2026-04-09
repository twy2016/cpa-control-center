package backend

import "testing"

func TestLauncherStartupShouldBlockAutoStartWhenUpdateAvailable(t *testing.T) {
	if !launcherStartupShouldBlockAutoStart(LauncherUpdateInfo{Available: true}) {
		t.Fatalf("expected startup auto start to be blocked when update is available")
	}
}

func TestLauncherStartupShouldNotBlockAutoStartWhenNoUpdate(t *testing.T) {
	if launcherStartupShouldBlockAutoStart(LauncherUpdateInfo{Available: false}) {
		t.Fatalf("expected startup auto start to continue when no update is available")
	}
}
