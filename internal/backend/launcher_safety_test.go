package backend

import (
	"path/filepath"
	"testing"
)

func TestLauncherConfigPathFromArgsResolvesRelativePath(t *testing.T) {
	workingDirectory := filepath.Join(t.TempDir(), "runtime")
	expectedConfigPath := normalizeLauncherArgumentPath(filepath.Join("configs", "config.yaml"), workingDirectory)

	configPath, ok := launcherConfigPathFromArgs(
		[]string{"cli-proxy-api.exe", "--config", filepath.Join("configs", "config.yaml")},
		workingDirectory,
	)
	if !ok {
		t.Fatal("expected config path to be detected from command arguments")
	}
	if configPath != expectedConfigPath {
		t.Fatalf("expected resolved config path %q, got %q", expectedConfigPath, configPath)
	}
}

func TestLauncherArgsMatchConfigRejectsDifferentConfig(t *testing.T) {
	workingDirectory := filepath.Join(t.TempDir(), "runtime")
	expectedConfigPath := normalizeLauncherArgumentPath(filepath.Join(workingDirectory, "config-a.yaml"), "")

	if launcherArgsMatchConfig(
		[]string{"cli-proxy-api.exe", "--config=config-b.yaml"},
		workingDirectory,
		expectedConfigPath,
	) {
		t.Fatal("expected different config paths not to match")
	}
}

func TestDetermineLauncherUpdatePlan(t *testing.T) {
	t.Run("managed process should stop and restart", func(t *testing.T) {
		plan := determineLauncherUpdatePlan(true, 0, 0)
		if !plan.ShouldStop || !plan.ShouldRestart || plan.BlockingProcessPID != 0 {
			t.Fatalf("unexpected update plan: %#v", plan)
		}
	})

	t.Run("matching configured external process should stop and restart", func(t *testing.T) {
		plan := determineLauncherUpdatePlan(false, 2468, 2468)
		if !plan.ShouldStop || !plan.ShouldRestart || plan.BlockingProcessPID != 0 {
			t.Fatalf("unexpected update plan: %#v", plan)
		}
	})

	t.Run("other process using same executable should block update", func(t *testing.T) {
		plan := determineLauncherUpdatePlan(false, 0, 1357)
		if plan.ShouldStop || plan.ShouldRestart || plan.BlockingProcessPID != 1357 {
			t.Fatalf("unexpected update plan: %#v", plan)
		}
	})
}

func TestResolveZipEntryDestination(t *testing.T) {
	targetDirectory := t.TempDir()

	t.Run("normal path stays inside target directory", func(t *testing.T) {
		destinationPath, err := resolveZipEntryDestination(targetDirectory, "bin/cli-proxy-api.exe")
		if err != nil {
			t.Fatalf("resolveZipEntryDestination: %v", err)
		}

		expectedPath := filepath.Join(targetDirectory, "bin", "cli-proxy-api.exe")
		if destinationPath != expectedPath {
			t.Fatalf("expected destination path %q, got %q", expectedPath, destinationPath)
		}
	})

	t.Run("parent traversal is rejected", func(t *testing.T) {
		if _, err := resolveZipEntryDestination(targetDirectory, "../evil.exe"); err == nil {
			t.Fatal("expected parent traversal entry to be rejected")
		}
	})

	t.Run("absolute path is rejected", func(t *testing.T) {
		if _, err := resolveZipEntryDestination(targetDirectory, "/windows/system32/evil.exe"); err == nil {
			t.Fatal("expected absolute path entry to be rejected")
		}
	})

	t.Run("volume path is rejected", func(t *testing.T) {
		if _, err := resolveZipEntryDestination(targetDirectory, "C:/windows/system32/evil.exe"); err == nil {
			t.Fatal("expected drive-qualified entry to be rejected")
		}
	})
}
