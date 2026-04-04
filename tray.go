package main

import "strings"

const trayTooltipTitle = "CPA Control Center"

type trayLabels struct {
	Tooltip             string
	ShowLabel           string
	StartLabel          string
	StopLabel           string
	OpenManagementLabel string
	QuitLauncherLabel   string
}

type trayMenuState struct {
	CanStart          bool
	CanStop           bool
	CanOpenManagement bool
}

type trayActions struct {
	Show           func()
	Start          func()
	Stop           func()
	OpenManagement func()
	QuitLauncher   func()
	CurrentState   func() trayMenuState
}

type trayController interface {
	Ready() bool
	UpdateLabels(labels trayLabels)
	Close() error
}

func trayLabelsForLocale(locale string) trayLabels {
	normalized := strings.ToLower(strings.TrimSpace(locale))
	if strings.HasPrefix(normalized, "zh") {
		return trayLabels{
			Tooltip:             trayTooltipTitle,
			ShowLabel:           "显示主窗口",
			StartLabel:          "启动 CPA",
			StopLabel:           "停止 CPA",
			OpenManagementLabel: "打开管理页",
			QuitLauncherLabel:   "退出启动器",
		}
	}
	return trayLabels{
		Tooltip:             trayTooltipTitle,
		ShowLabel:           "Show Main Window",
		StartLabel:          "Start CPA",
		StopLabel:           "Stop CPA",
		OpenManagementLabel: "Open Management",
		QuitLauncherLabel:   "Exit Launcher",
	}
}
