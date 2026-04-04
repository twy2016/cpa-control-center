package backend

import "testing"

func TestLauncherDescribeStatusTreatsMatchingExistingProcessAsManagedRunning(t *testing.T) {
	service := &launcherService{}
	runtimeInfo := &LauncherRuntimeInfo{
		ExecutablePath: "C:\\ai\\CLIProxyAPI\\cli-proxy-api.exe",
	}

	status, _, _, managed, managedProcessID := service.describeStatus(
		"zh-CN",
		runtimeInfo,
		true,
		"",
		27904,
	)

	if status != launcherStatusRunning {
		t.Fatalf("expected status %q, got %q", launcherStatusRunning, status)
	}
	if !managed {
		t.Fatalf("expected managed to be true")
	}
	if managedProcessID != 27904 {
		t.Fatalf("expected managedProcessID to be 27904, got %d", managedProcessID)
	}
}

func TestLauncherDescribeStatusKeepsExternalWhenNoMatchingProcessExists(t *testing.T) {
	service := &launcherService{}
	runtimeInfo := &LauncherRuntimeInfo{
		ExecutablePath: "C:\\ai\\CLIProxyAPI\\cli-proxy-api.exe",
	}

	status, _, _, managed, managedProcessID := service.describeStatus(
		"zh-CN",
		runtimeInfo,
		true,
		"",
		0,
	)

	if status != launcherStatusExternal {
		t.Fatalf("expected status %q, got %q", launcherStatusExternal, status)
	}
	if managed {
		t.Fatalf("expected managed to be false")
	}
	if managedProcessID != 0 {
		t.Fatalf("expected managedProcessID to be 0, got %d", managedProcessID)
	}
}

func TestLauncherDescribeStatusTreatsMatchingExistingProcessAsManagedStarting(t *testing.T) {
	service := &launcherService{}
	runtimeInfo := &LauncherRuntimeInfo{
		ExecutablePath: "C:\\ai\\CLIProxyAPI\\cli-proxy-api.exe",
	}

	status, _, _, managed, managedProcessID := service.describeStatus(
		"zh-CN",
		runtimeInfo,
		false,
		"",
		27904,
	)

	if status != launcherStatusStarting {
		t.Fatalf("expected status %q, got %q", launcherStatusStarting, status)
	}
	if !managed {
		t.Fatalf("expected managed to be true")
	}
	if managedProcessID != 27904 {
		t.Fatalf("expected managedProcessID to be 27904, got %d", managedProcessID)
	}
}
