package backend

import "testing"

func TestLauncherStartupShouldNotBlockAutoStartWhenUpdateAvailable(t *testing.T) {
	t.Parallel()

	if launcherStartupShouldBlockAutoStart(LauncherUpdateInfo{Available: true}) {
		t.Fatal("expected startup auto start to continue when update is available")
	}
}

func TestLauncherStartupShouldContinueAutoStartWhenNoUpdate(t *testing.T) {
	t.Parallel()

	cases := []LauncherUpdateInfo{
		{Available: false},
		{Available: false, CurrentVersion: "v1.0.0"},
		{Available: false, Message: "CPA 已是最新版本。"},
	}

	for _, update := range cases {
		if launcherStartupShouldBlockAutoStart(update) {
			t.Fatalf("expected startup auto start to continue, got blocked for update=%+v", update)
		}
	}
}
