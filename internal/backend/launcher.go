package backend

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	cpamanager "cpa-control-center/internal/cpamanager"
	"github.com/pkg/browser"
	"golang.org/x/net/http/httpproxy"
)

const (
	launcherStatusUnconfigured = "unconfigured"
	launcherStatusStopped      = "stopped"
	launcherStatusStarting     = "starting"
	launcherStatusRunning      = "running"
	launcherStatusExternal     = "external"
	launcherStatusStopping     = "stopping"
	launcherStatusStartFailed  = "start_failed"

	launcherLogLimit        = 400
	launcherRefreshInterval = 2500 * time.Millisecond
	launcherProbeTimeout    = 2 * time.Second
	launcherDownloadTimeout = 10 * time.Minute
	defaultCPAListenPort    = 8317

	launcherUpdateSourceStartup = "startup"
	launcherUpdateSourceManual  = "manual"
)

type launcherService struct {
	store          *Store
	logger         *Logger
	emitter        EventEmitter
	probeClient    *http.Client
	downloadClient *http.Client
	cpaManager     *cpamanager.Service

	mu               sync.Mutex
	cmd              *exec.Cmd
	doneCh           chan struct{}
	stopping         bool
	lastStartError   string
	logs             []LogEntry
	update           LauncherUpdateInfo
	cpaManagerUpdate LauncherUpdateInfo
	stopCh           chan struct{}
}

type launcherReleaseInfo struct {
	TagName          string
	AssetDownloadURL string
	AssetSize        int64
	ReleaseURL       string
}

type launcherProxyDiagnostics struct {
	ConfiguredConfigPath string
	ResolvedConfigPath   string
	ConfigProxyURL       string
	EnvironmentProxyURL  string
	EffectiveProxyLabel  string
	EffectiveProxyURL    string
}

func newLauncherService(store *Store, logger *Logger, emitter EventEmitter) *launcherService {
	cpaManagerDataDir := filepathJoin(".", "cpa-manager")
	if store != nil && strings.TrimSpace(store.dataDir) != "" {
		cpaManagerDataDir = filepathJoin(store.dataDir, "cpa-manager")
	}
	return &launcherService{
		store:   store,
		logger:  logger,
		emitter: emitter,
		probeClient: &http.Client{
			Timeout: launcherProbeTimeout,
		},
		downloadClient: &http.Client{
			Timeout: launcherDownloadTimeout,
		},
		cpaManager: cpamanager.New(cpaManagerDataDir),
		stopCh:     make(chan struct{}),
	}
}

func (l *launcherService) Start() {
	go func() {
		l.refreshAndEmit()
		l.runStartupActions()

		ticker := time.NewTicker(launcherRefreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				l.refreshAndEmit()
			case <-l.stopCh:
				return
			}
		}
	}()
}

func (l *launcherService) Close() {
	select {
	case <-l.stopCh:
	default:
		close(l.stopCh)
	}
	if l.cpaManager != nil {
		_ = l.cpaManager.Stop(context.Background())
	}
}

func (l *launcherService) runStartupActions() {
	settings, err := l.store.LoadSettings()
	if err != nil {
		l.appendLog("warning", fmt.Sprintf("加载启动器设置失败: %v", err))
		return
	}

	if err := setLauncherWindowsStartup(settings.Launcher.LaunchOnWindowsStartup); err != nil {
		l.appendLog("warning", err.Error())
	}

	if settings.Launcher.CheckForUpdatesOnStartup {
		if strings.TrimSpace(settings.Launcher.ExecutablePath) != "" {
			update, updateErr := l.checkForUpdate(settings, true, launcherUpdateSourceStartup)
			if updateErr != nil {
				l.appendLog("warning", updateErr.Error())
			} else if update.Available {
				l.appendLog("info", fmt.Sprintf("%s 自动启动 CPA 将继续执行。", update.Message))
			}
		}
		if _, managerUpdateErr := l.checkCPAManagerForUpdate(settings, true, launcherUpdateSourceStartup); managerUpdateErr != nil {
			l.appendLog("warning", managerUpdateErr.Error())
		}
	}

	if !settings.Launcher.AutoStartService {
		return
	}

	snapshot, err := l.Refresh()
	if err != nil {
		l.appendLog("warning", fmt.Sprintf("启动前刷新 CPA 状态失败: %v", err))
		return
	}
	if snapshot.Status != launcherStatusStopped {
		if snapshot.ServiceReachable && snapshot.Runtime != nil {
			if _, err := l.startCPAManager(context.Background(), settings, *snapshot.Runtime); err != nil {
				l.appendLog("warning", err.Error())
			}
		}
		return
	}

	delay := settings.Launcher.AutoStartDelaySeconds
	if delay > 0 {
		timer := time.NewTimer(time.Duration(delay) * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-l.stopCh:
			return
		}
	}

	if _, err := l.StartService(); err != nil {
		l.appendLog("error", err.Error())
	}
}

func (l *launcherService) GetStatus() (LauncherStatusSnapshot, error) {
	return l.Refresh()
}

func (l *launcherService) Refresh() (LauncherStatusSnapshot, error) {
	settings, err := l.store.LoadSettings()
	if err != nil {
		return LauncherStatusSnapshot{}, err
	}

	update := l.currentUpdate(settings.Launcher.LastInstalledVersion)
	cpaManagerUpdate := l.currentCPAManagerUpdate()

	var (
		runtimeInfo       *LauncherRuntimeInfo
		serviceReachable  bool
		inspectErrMessage string
		matchedProcessID  int
	)

	if hasLauncherPaths(settings.Launcher) {
		info, err := inspectLauncherRuntime(settings.Launcher.ExecutablePath, settings.Launcher.ConfigPath)
		if err != nil {
			inspectErrMessage = err.Error()
		} else {
			runtimeInfo = &info
			serviceReachable = l.probeService(info.ServiceProbeURL)
			matchedProcessID, _ = findLauncherProcessPID(info.ExecutablePath, info.ConfigPath)
			l.attachCPAManagerRuntime(runtimeInfo)
		}
	}

	status, statusText, statusDetail, managed, managedProcessID := l.describeStatus(
		settings.Locale,
		runtimeInfo,
		serviceReachable,
		inspectErrMessage,
		matchedProcessID,
	)

	return LauncherStatusSnapshot{
		Status:           status,
		StatusText:       statusText,
		StatusDetail:     statusDetail,
		Managed:          managed,
		ServiceReachable: serviceReachable,
		ManagedProcessID: managedProcessID,
		Settings:         settings.Launcher,
		Runtime:          runtimeInfo,
		Update:           update,
		CPAManagerUpdate: cpaManagerUpdate,
		Logs:             l.snapshotLogs(),
	}, nil
}

func (l *launcherService) refreshAndEmit() {
	snapshot, err := l.Refresh()
	if err != nil {
		l.appendLog("warning", fmt.Sprintf("刷新 CPA 启动器状态失败: %v", err))
		return
	}
	l.emitStatus(snapshot)
}

func (l *launcherService) SaveSettings(input LauncherSettings) (LauncherStatusSnapshot, error) {
	settings, err := l.store.LoadSettings()
	if err != nil {
		return LauncherStatusSnapshot{}, err
	}

	settings.Launcher = normalizeLauncherSettings(input)
	if err := setLauncherWindowsStartup(settings.Launcher.LaunchOnWindowsStartup); err != nil {
		return LauncherStatusSnapshot{}, err
	}
	if _, err := l.store.SaveSettings(settings); err != nil {
		return LauncherStatusSnapshot{}, err
	}

	l.appendLog("info", "已保存 CPA 启动器设置。")
	snapshot, err := l.Refresh()
	if err == nil {
		l.emitStatus(snapshot)
	}
	return snapshot, err
}

func (l *launcherService) StartService() (LauncherStatusSnapshot, error) {
	settings, err := l.store.LoadSettings()
	if err != nil {
		return LauncherStatusSnapshot{}, err
	}

	info, err := inspectLauncherRuntime(settings.Launcher.ExecutablePath, settings.Launcher.ConfigPath)
	if err != nil {
		return LauncherStatusSnapshot{}, err
	}
	l.ensureCPAManagerPanel(info)

	l.mu.Lock()
	if l.cmd != nil {
		l.mu.Unlock()
		return l.Refresh()
	}
	l.mu.Unlock()

	existingProcessID, _ := findLauncherProcessPID(info.ExecutablePath, info.ConfigPath)
	if existingProcessID > 0 {
		l.appendLog("info", fmt.Sprintf("检测到 CPA 已在运行，PID=%d。", existingProcessID))
		if _, err := l.startCPAManager(context.Background(), settings, info); err != nil {
			l.appendLog("warning", err.Error())
		}
		snapshot, refreshErr := l.Refresh()
		if refreshErr == nil {
			l.emitStatus(snapshot)
		}
		return snapshot, refreshErr
	}

	cmd := exec.Command(filepath.Clean(info.ExecutablePath), "--config", filepath.Clean(info.ConfigPath))
	cmd.Dir = info.ExecutableDirectory
	configureLauncherCommand(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return LauncherStatusSnapshot{}, fmt.Errorf("创建 CPA stdout 管道失败: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return LauncherStatusSnapshot{}, fmt.Errorf("创建 CPA stderr 管道失败: %w", err)
	}
	if err := cmd.Start(); err != nil {
		l.mu.Lock()
		l.lastStartError = fmt.Sprintf("启动 CPA 失败: %v", err)
		l.mu.Unlock()
		l.appendLog("error", l.lastStartError)
		snapshot, refreshErr := l.Refresh()
		if refreshErr == nil {
			l.emitStatus(snapshot)
		}
		return snapshot, err
	}

	doneCh := make(chan struct{})
	l.mu.Lock()
	l.cmd = cmd
	l.doneCh = doneCh
	l.stopping = false
	l.lastStartError = ""
	l.mu.Unlock()

	l.appendLog("info", fmt.Sprintf("已启动 CPA 进程，PID=%d。", cmd.Process.Pid))

	go l.captureProcessOutput("stdout", stdout)
	go l.captureProcessOutput("stderr", stderr)
	go l.waitForProcessExit(cmd, doneCh)
	go l.waitForServiceReady(settings, info, settings.Launcher.OpenManagementPageAfterStart)

	snapshot, err := l.Refresh()
	if err == nil {
		l.emitStatus(snapshot)
	}
	return snapshot, err
}

func (l *launcherService) StopService() (LauncherStatusSnapshot, error) {
	if err := l.stopCPAManager(); err != nil {
		l.appendLog("warning", err.Error())
	}

	l.mu.Lock()
	cmd := l.cmd
	doneCh := l.doneCh
	pid := 0
	if cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	if cmd != nil && doneCh != nil {
		l.stopping = true
		l.mu.Unlock()

		l.appendLog("info", fmt.Sprintf("正在停止 CPA 进程，PID=%d。", pid))
		if err := killProcessTree(cmd); err != nil {
			select {
			case <-doneCh:
			default:
				return LauncherStatusSnapshot{}, fmt.Errorf("停止 CPA 失败: %w", err)
			}
		}

		select {
		case <-doneCh:
		case <-time.After(5 * time.Second):
			return LauncherStatusSnapshot{}, errors.New("等待 CPA 退出超时")
		}

		snapshot, err := l.Refresh()
		if err == nil {
			l.emitStatus(snapshot)
		}
		return snapshot, err
	}
	l.mu.Unlock()

	settings, err := l.store.LoadSettings()
	if err != nil {
		return LauncherStatusSnapshot{}, err
	}

	info, err := inspectLauncherRuntime(settings.Launcher.ExecutablePath, settings.Launcher.ConfigPath)
	if err != nil {
		snapshot, refreshErr := l.Refresh()
		if refreshErr == nil {
			l.emitStatus(snapshot)
		}
		return snapshot, refreshErr
	}

	externalProcessID, _ := findLauncherProcessPID(info.ExecutablePath, info.ConfigPath)
	if externalProcessID <= 0 {
		snapshot, refreshErr := l.Refresh()
		if refreshErr == nil {
			l.emitStatus(snapshot)
		}
		return snapshot, refreshErr
	}

	l.mu.Lock()
	l.stopping = true
	l.mu.Unlock()

	l.appendLog("info", fmt.Sprintf("正在停止 CPA 进程，PID=%d。", externalProcessID))
	stopErr := killProcessTreeByPID(externalProcessID)

	l.mu.Lock()
	l.stopping = false
	l.mu.Unlock()

	if stopErr != nil {
		return LauncherStatusSnapshot{}, fmt.Errorf("停止 CPA 失败: %w", stopErr)
	}
	if !waitForProcessExitByPID(externalProcessID, 5*time.Second) {
		return LauncherStatusSnapshot{}, errors.New("等待 CPA 退出超时")
	}

	snapshot, err := l.Refresh()
	if err == nil {
		l.emitStatus(snapshot)
	}
	return snapshot, err
}

func (l *launcherService) ClearLogs() (LauncherStatusSnapshot, error) {
	l.mu.Lock()
	l.logs = nil
	l.mu.Unlock()

	snapshot, err := l.Refresh()
	if err == nil {
		l.emitStatus(snapshot)
	}
	return snapshot, err
}

func (l *launcherService) CheckForUpdate() (LauncherStatusSnapshot, error) {
	settings, err := l.store.LoadSettings()
	if err != nil {
		return LauncherStatusSnapshot{}, err
	}
	_, cpaErr := l.checkForUpdate(settings, false, launcherUpdateSourceManual)
	_, managerErr := l.checkCPAManagerForUpdate(settings, false, launcherUpdateSourceManual)
	if cpaErr != nil {
		return LauncherStatusSnapshot{}, cpaErr
	}
	if managerErr != nil {
		return LauncherStatusSnapshot{}, managerErr
	}
	snapshot, err := l.Refresh()
	if err == nil {
		l.emitStatus(snapshot)
	}
	return snapshot, err
}

func (l *launcherService) InstallLatest(targetDirectory string) (LauncherStatusSnapshot, error) {
	settings, err := l.store.LoadSettings()
	if err != nil {
		return LauncherStatusSnapshot{}, err
	}

	targetDirectory = strings.TrimSpace(targetDirectory)
	if targetDirectory == "" {
		return LauncherStatusSnapshot{}, errors.New("必须选择 CPA 安装目录")
	}

	diagnostics, err := launcherProxyDiagnosticsFromSettings(settings.Launcher, fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", strings.TrimSpace(stringOr(settings.Launcher.GitHubRepo, defaultLauncherRepo))))
	if err != nil {
		return LauncherStatusSnapshot{}, err
	}
	l.appendLog("info", launcherProxyDiagnosticsMessage(diagnostics))

	release, err := l.fetchLatestRelease(settings.Launcher.GitHubRepo, "", diagnostics.EffectiveProxyURL)
	if err != nil {
		return LauncherStatusSnapshot{}, err
	}
	if release == nil {
		return LauncherStatusSnapshot{}, errors.New("未找到可安装的 CPA 版本")
	}

	l.appendLog("info", fmt.Sprintf("开始下载 CPA %s。", release.TagName))
	executablePath, err := l.downloadAndExtract(targetDirectory, release, diagnostics.EffectiveProxyURL)
	if err != nil {
		return LauncherStatusSnapshot{}, err
	}

	settings.Launcher.ExecutablePath = executablePath
	settings.Launcher.LastInstalledVersion = release.TagName
	if strings.TrimSpace(settings.Launcher.ConfigPath) == "" {
		candidate := filepath.Join(targetDirectory, "config.yaml")
		if _, statErr := os.Stat(candidate); statErr == nil {
			settings.Launcher.ConfigPath = candidate
		}
	}
	if _, err := l.store.SaveSettings(settings); err != nil {
		return LauncherStatusSnapshot{}, err
	}

	l.mu.Lock()
	l.update = LauncherUpdateInfo{
		Available:      false,
		CurrentVersion: release.TagName,
		TagName:        release.TagName,
		AssetSize:      release.AssetSize,
		ReleaseURL:     release.ReleaseURL,
		CheckedAt:      nowISO(),
		Message:        fmt.Sprintf("CPA %s 安装完成。", release.TagName),
	}
	l.mu.Unlock()
	l.appendLog("info", fmt.Sprintf("CPA %s 安装完成，路径：%s。", release.TagName, executablePath))

	snapshot, err := l.Refresh()
	if err == nil {
		l.emitStatus(snapshot)
	}
	return snapshot, err
}

func (l *launcherService) UpdateCPA() (LauncherStatusSnapshot, error) {
	settings, err := l.store.LoadSettings()
	if err != nil {
		return LauncherStatusSnapshot{}, err
	}
	if strings.TrimSpace(settings.Launcher.ExecutablePath) == "" {
		return LauncherStatusSnapshot{}, errors.New("请先配置 CPA 可执行文件路径")
	}

	diagnostics, err := launcherProxyDiagnosticsFromSettings(settings.Launcher, fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", strings.TrimSpace(stringOr(settings.Launcher.GitHubRepo, defaultLauncherRepo))))
	if err != nil {
		return LauncherStatusSnapshot{}, err
	}
	l.appendLog("info", launcherProxyDiagnosticsMessage(diagnostics))

	release, err := l.fetchLatestRelease(settings.Launcher.GitHubRepo, settings.Launcher.LastInstalledVersion, diagnostics.EffectiveProxyURL)
	if err != nil {
		return LauncherStatusSnapshot{}, err
	}
	if release == nil {
		l.mu.Lock()
		l.update = LauncherUpdateInfo{
			Available:      false,
			CurrentVersion: settings.Launcher.LastInstalledVersion,
			CheckedAt:      nowISO(),
			Message:        "CPA 已是最新版本。",
		}
		l.mu.Unlock()
		snapshot, refreshErr := l.Refresh()
		if refreshErr == nil {
			l.emitStatus(snapshot)
		}
		return snapshot, refreshErr
	}

	matchingConfiguredProcessID := 0
	if resolvedConfigPath, resolveErr := resolveLauncherConfigPathForRead(settings.Launcher.ConfigPath, settings.Launcher.ExecutablePath); resolveErr == nil && strings.TrimSpace(resolvedConfigPath) != "" {
		matchingConfiguredProcessID, _ = findLauncherProcessPID(settings.Launcher.ExecutablePath, resolvedConfigPath)
	}
	executableInUsePID, _ := findLauncherProcessPIDByExecutablePath(settings.Launcher.ExecutablePath)

	wasRunning := false
	l.mu.Lock()
	wasRunning = l.cmd != nil
	l.mu.Unlock()
	updatePlan := determineLauncherUpdatePlan(wasRunning, matchingConfiguredProcessID, executableInUsePID)
	if updatePlan.BlockingProcessPID > 0 {
		return LauncherStatusSnapshot{}, fmt.Errorf("检测到外部 CPA 进程正在占用当前可执行文件，PID=%d。请先手动停止该进程后再更新。", updatePlan.BlockingProcessPID)
	}
	if updatePlan.ShouldStop {
		if _, err := l.StopService(); err != nil {
			return LauncherStatusSnapshot{}, err
		}
	}

	l.appendLog("info", fmt.Sprintf("开始更新 CPA 到 %s。", release.TagName))
	if err := l.downloadAndReplaceExecutable(settings.Launcher.ExecutablePath, release, diagnostics.EffectiveProxyURL); err != nil {
		return LauncherStatusSnapshot{}, err
	}

	settings.Launcher.LastInstalledVersion = release.TagName
	if _, err := l.store.SaveSettings(settings); err != nil {
		return LauncherStatusSnapshot{}, err
	}

	l.mu.Lock()
	l.update = LauncherUpdateInfo{
		Available:      false,
		CurrentVersion: release.TagName,
		TagName:        release.TagName,
		AssetSize:      release.AssetSize,
		ReleaseURL:     release.ReleaseURL,
		CheckedAt:      nowISO(),
		Message:        fmt.Sprintf("CPA 已更新到 %s。", release.TagName),
	}
	l.mu.Unlock()
	l.appendLog("info", fmt.Sprintf("CPA 已更新到 %s。", release.TagName))

	if updatePlan.ShouldRestart {
		if _, err := l.StartService(); err != nil {
			return LauncherStatusSnapshot{}, err
		}
	}

	snapshot, err := l.Refresh()
	if err == nil {
		l.emitStatus(snapshot)
	}
	return snapshot, err
}

func (l *launcherService) GenerateDefaultConfig(input LauncherConfigTemplateInput) (LauncherStatusSnapshot, error) {
	settings, err := l.store.LoadSettings()
	if err != nil {
		return LauncherStatusSnapshot{}, err
	}

	configPath := strings.TrimSpace(input.ConfigPath)
	if configPath == "" {
		configPath = strings.TrimSpace(settings.Launcher.ConfigPath)
	}
	if configPath == "" && strings.TrimSpace(settings.Launcher.ExecutablePath) != "" {
		configPath = filepath.Join(filepath.Dir(settings.Launcher.ExecutablePath), "config.yaml")
	}
	if configPath == "" {
		return LauncherStatusSnapshot{}, errors.New("必须提供 config.yaml 保存路径")
	}

	host := strings.TrimSpace(input.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	port := input.Port
	if port <= 0 {
		port = defaultCPAListenPort
	}

	if err := writeDefaultLauncherConfig(configPath, host, port, input.ProxyURL, input.SecretKey); err != nil {
		return LauncherStatusSnapshot{}, err
	}

	settings.Launcher.ConfigPath = configPath
	if _, err := l.store.SaveSettings(settings); err != nil {
		return LauncherStatusSnapshot{}, err
	}

	l.appendLog("info", fmt.Sprintf("已生成默认 CPA 配置：%s。", configPath))
	snapshot, err := l.Refresh()
	if err == nil {
		l.emitStatus(snapshot)
	}
	return snapshot, err
}

func (l *launcherService) InspectRuntime() (*LauncherRuntimeInfo, error) {
	settings, err := l.store.LoadSettings()
	if err != nil {
		return nil, err
	}
	if !hasLauncherPaths(settings.Launcher) {
		return nil, nil
	}
	info, err := inspectLauncherRuntime(settings.Launcher.ExecutablePath, settings.Launcher.ConfigPath)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

func (l *launcherService) ManagementURL() (string, error) {
	settings, err := l.store.LoadSettings()
	if err != nil {
		return "", err
	}
	if !hasLauncherPaths(settings.Launcher) {
		return "", errors.New("当前未配置本地 CPA 运行时")
	}
	info, err := inspectLauncherRuntime(settings.Launcher.ExecutablePath, settings.Launcher.ConfigPath)
	if err != nil {
		return "", err
	}

	l.ensureCPAManagerPanel(info)
	if _, startErr := l.startCPAManager(context.Background(), settings, info); startErr != nil {
		l.appendLog("warning", startErr.Error())
	}
	return info.ManagementURL, nil
}

func (l *launcherService) emitStatus(snapshot LauncherStatusSnapshot) {
	if l.emitter != nil {
		l.emitter.Emit("launcher:status", snapshot)
	}
}

func (l *launcherService) currentUpdate(currentVersion string) LauncherUpdateInfo {
	l.mu.Lock()
	defer l.mu.Unlock()

	update := l.update
	if strings.TrimSpace(update.CurrentVersion) == "" {
		update.CurrentVersion = strings.TrimSpace(currentVersion)
	}
	return update
}

func (l *launcherService) currentCPAManagerUpdate() LauncherUpdateInfo {
	l.mu.Lock()
	defer l.mu.Unlock()

	update := l.cpaManagerUpdate
	if strings.TrimSpace(update.CurrentVersion) == "" {
		update.CurrentVersion = embeddedCPAManagerVersion
	}
	return update
}

func launcherStartupShouldBlockAutoStart(update LauncherUpdateInfo) bool {
	return false
}

func (l *launcherService) describeStatus(
	locale string,
	runtimeInfo *LauncherRuntimeInfo,
	serviceReachable bool,
	inspectErrMessage string,
	matchedProcessID int,
) (string, string, string, bool, int) {
	l.mu.Lock()
	cmd := l.cmd
	stopping := l.stopping
	lastStartError := l.lastStartError
	l.mu.Unlock()

	managed := cmd != nil
	managedProcessID := 0
	if managed && cmd.Process != nil {
		managedProcessID = cmd.Process.Pid
	}
	attachedExistingProcess := false
	if !managed && matchedProcessID > 0 {
		managed = true
		managedProcessID = matchedProcessID
		attachedExistingProcess = true
	}

	if runtimeInfo == nil {
		detail := launcherText(locale,
			"Choose the CPA executable and config file first.",
			"请先配置 CPA 可执行文件和 config.yaml 路径。",
		)
		if inspectErrMessage != "" {
			detail = inspectErrMessage
		}
		return launcherStatusUnconfigured,
			launcherText(locale, "Unconfigured", "未配置"),
			detail,
			managed,
			managedProcessID
	}

	if managed && serviceReachable {
		detail := launcherText(locale, "CPA is running under launcher management.", "CPA 已运行，当前由启动器托管。")
		if attachedExistingProcess {
			detail = launcherText(
				locale,
				"CPA is running and the existing process matches the saved runtime.",
				"CPA 已运行，且已识别为当前配置对应的现有进程。",
			)
		}
		return launcherStatusRunning,
			launcherText(locale, "Running", "运行中"),
			detail,
			true,
			managedProcessID
	}
	if managed && !serviceReachable {
		if stopping {
			return launcherStatusStopping,
				launcherText(locale, "Stopping", "停止中"),
				launcherText(locale, "The launcher is stopping the CPA process.", "启动器正在停止 CPA 进程。"),
				true,
				managedProcessID
		}
		detail := launcherText(
			locale,
			"CPA process is running and waiting for the service to become ready.",
			"CPA 进程已启动，正在等待服务就绪。",
		)
		if attachedExistingProcess {
			detail = launcherText(
				locale,
				"Detected a matching CPA process, but the service is not reachable yet.",
				"已检测到与当前配置匹配的 CPA 进程，但服务暂时还不可访问。",
			)
		}
		return launcherStatusStarting,
			launcherText(locale, "Starting", "启动中"),
			detail,
			true,
			managedProcessID
	}
	if !managed && serviceReachable {
		return launcherStatusExternal,
			launcherText(locale, "External Running", "外部运行"),
			launcherText(locale, "CPA is reachable but not managed by this launcher.", "CPA 正在运行，但不由当前启动器托管。"),
			false,
			0
	}
	if lastStartError != "" {
		return launcherStatusStartFailed,
			launcherText(locale, "Start Failed", "启动失败"),
			lastStartError,
			false,
			0
	}
	return launcherStatusStopped,
		launcherText(locale, "Stopped", "已停止"),
		launcherText(locale, "CPA is not running.", "CPA 当前未运行。"),
		false,
		0
}

func (l *launcherService) probeService(url string) bool {
	if strings.TrimSpace(url) == "" {
		return false
	}
	response, err := l.probeClient.Get(url)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 400
}

func (l *launcherService) appendLog(level string, message string) {
	entry := LogEntry{
		Kind:      "launcher",
		Level:     level,
		Message:   message,
		Timestamp: nowISO(),
	}

	l.mu.Lock()
	l.logs = append(l.logs, entry)
	if len(l.logs) > launcherLogLimit {
		l.logs = append([]LogEntry(nil), l.logs[len(l.logs)-launcherLogLimit:]...)
	}
	l.mu.Unlock()

	if l.logger != nil {
		_ = l.logger.Write(entry)
	}
	if l.emitter != nil {
		l.emitter.Emit("launcher:log", entry)
	}
}

func (l *launcherService) snapshotLogs() []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	logs := make([]LogEntry, 0, len(l.logs))
	for index := len(l.logs) - 1; index >= 0; index-- {
		logs = append(logs, l.logs[index])
	}
	return logs
}

func (l *launcherService) captureProcessOutput(source string, reader io.ReadCloser) {
	defer reader.Close()
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		l.appendLog("info", fmt.Sprintf("[%s] %s", source, line))
	}
	if err := scanner.Err(); err != nil {
		l.appendLog("warning", fmt.Sprintf("读取 CPA %s 输出失败: %v", source, err))
	}
}

func (l *launcherService) waitForProcessExit(cmd *exec.Cmd, doneCh chan struct{}) {
	err := cmd.Wait()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	l.mu.Lock()
	if l.cmd == cmd {
		l.cmd = nil
		l.doneCh = nil
	}
	l.stopping = false
	close(doneCh)
	l.mu.Unlock()

	l.appendLog("info", fmt.Sprintf("CPA 进程已退出，ExitCode=%d。", exitCode))
	if err := l.stopCPAManager(); err != nil {
		l.appendLog("warning", err.Error())
	}
	l.refreshAndEmit()
}

func (l *launcherService) waitForServiceReady(settings AppSettings, info LauncherRuntimeInfo, openManagement bool) {
	for attempt := 0; attempt < 30; attempt++ {
		time.Sleep(500 * time.Millisecond)
		if l.probeService(info.ServiceProbeURL) {
			managementURL := info.ManagementURL
			if _, err := l.startCPAManager(context.Background(), settings, info); err != nil {
				l.appendLog("warning", err.Error())
			}
			if openManagement {
				_ = browser.OpenURL(managementURL)
			}
			l.refreshAndEmit()
			return
		}
	}
	l.refreshAndEmit()
}

func (l *launcherService) startCPAManager(ctx context.Context, settings AppSettings, info LauncherRuntimeInfo) (cpamanager.RuntimeInfo, error) {
	if l.cpaManager == nil {
		return cpamanager.RuntimeInfo{}, errors.New("CPA-Manager 服务未初始化")
	}

	previousRuntime, wasRunning := l.cpaManager.Runtime()
	managementKey, _, skippedHashedKey := resolveLauncherManagementKey(settings, info)

	runtimeInfo, err := l.cpaManager.Start(ctx, cpamanager.StartConfig{
		CPAUpstreamURL: info.BaseURL,
		ManagementKey:  managementKey,
	})
	if err != nil {
		return cpamanager.RuntimeInfo{}, fmt.Errorf("启动 CPA-Manager 失败: %w", err)
	}

	unchangedRuntime := wasRunning && previousRuntime.ManagementURL == runtimeInfo.ManagementURL
	if unchangedRuntime {
		return runtimeInfo, nil
	}

	if managementKey == "" {
		if skippedHashedKey {
			l.appendLog("warning", "检测到 CPA 配置中的 remote-management.secret-key 是哈希值，不能作为 Usage Service 管理密钥使用；请在设置页保存登录 CPA 面板时使用的明文管理令牌，或在 CPA 面板中首次配置 Usage Service。")
		} else {
			l.appendLog("warning", "CPA-Manager 已启动，但当前没有可自动注入的 Management Key；首次打开需要在页面中完成 setup。")
		}
	}
	if !info.UsageStatisticsEnabled {
		l.appendLog("warning", "当前 CPA config.yaml 未启用 usage-statistics-enabled，CPA-Manager 请求统计可能没有数据。")
	}

	l.appendLog("info", fmt.Sprintf("CPA-Manager Usage Service 已启动：%s。", runtimeInfo.BaseURL))
	return runtimeInfo, nil
}

func resolveLauncherManagementKey(settings AppSettings, info LauncherRuntimeInfo) (string, string, bool) {
	settingsToken := strings.TrimSpace(settings.ManagementToken)
	skippedHashedKey := false
	if settingsToken != "" {
		if !isBcryptManagementSecret(settingsToken) {
			return settingsToken, "settings", false
		}
		skippedHashedKey = true
	}

	configKey := strings.TrimSpace(info.ManagementSecretKey)
	if configKey != "" {
		if !isBcryptManagementSecret(configKey) {
			return configKey, "config", skippedHashedKey
		}
		skippedHashedKey = true
	}

	return "", "", skippedHashedKey
}

func isBcryptManagementSecret(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 7 {
		return false
	}
	return strings.HasPrefix(value, "$2a$") ||
		strings.HasPrefix(value, "$2b$") ||
		strings.HasPrefix(value, "$2x$") ||
		strings.HasPrefix(value, "$2y$")
}

func (l *launcherService) ensureCPAManagerPanel(info LauncherRuntimeInfo) {
	if strings.TrimSpace(info.ConfigPath) != "" {
		changed, err := ensureCPAManagerPanelRepository(info.ConfigPath)
		if err != nil {
			l.appendLog("warning", fmt.Sprintf("更新 CPA 管理面板仓库配置失败: %v", err))
		} else if changed {
			l.appendLog("info", fmt.Sprintf("已将 CPA 管理面板仓库切换为 %s。", defaultCPAManagerPanelRepo))
		}
	}

	action, err := ensureCPAManagerPanelAsset(info.ExecutableDirectory)
	if err != nil {
		l.appendLog("warning", fmt.Sprintf("准备 CPA-Manager 管理面板失败: %v", err))
		return
	}
	switch action {
	case "copied":
		l.appendLog("info", "已将 CPA-Manager 管理面板安装到 CPA static 目录。")
	case "removed":
		l.appendLog("info", "已移除旧 CPA 管理面板缓存，CPA 下次启动会重新下载 CPA-Manager 面板。")
	}
}

func ensureCPAManagerPanelRepository(configPath string) (bool, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return false, nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false, err
	}
	next, changed := replaceCPAManagerPanelRepository(string(data))
	if !changed {
		return false, nil
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(configPath); statErr == nil {
		mode = info.Mode()
	}
	if err := os.WriteFile(configPath, []byte(next), mode); err != nil {
		return false, err
	}
	return true, nil
}

func replaceCPAManagerPanelRepository(configText string) (string, bool) {
	if strings.TrimSpace(configText) == "" {
		return configText, false
	}

	newline := "\n"
	if strings.Contains(configText, "\r\n") {
		newline = "\r\n"
	}
	normalized := strings.ReplaceAll(configText, "\r\n", "\n")
	hasTrailingNewline := strings.HasSuffix(normalized, "\n")
	lines := strings.Split(normalized, "\n")
	if hasTrailingNewline && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}

	inRemoteManagement := false
	remoteIndent := 0
	insertIndex := -1
	changed := false

	for index := 0; index < len(lines); index++ {
		line := lines[index]
		key, value, ok := yamlScalarLine(line)
		if !ok {
			continue
		}
		indent := leadingSpaces(line)
		if inRemoteManagement && indent <= remoteIndent {
			if insertIndex >= 0 {
				lines = insertString(lines, insertIndex, strings.Repeat(" ", remoteIndent+2)+cpamanagerPanelRepositoryYAML())
				changed = true
			}
			inRemoteManagement = false
			insertIndex = -1
		}

		if key == "remote-management" && strings.TrimSpace(value) == "" {
			inRemoteManagement = true
			remoteIndent = indent
			insertIndex = index + 1
			continue
		}
		if !inRemoteManagement {
			continue
		}
		if key == "disable-control-panel" {
			insertIndex = index + 1
			continue
		}
		if key == "panel-github-repository" {
			if unquoteYAMLValue(value) == defaultCPAManagerPanelRepo {
				return configText, false
			}
			lines[index] = strings.Repeat(" ", indent) + cpamanagerPanelRepositoryYAML()
			changed = true
			insertIndex = -1
			break
		}
	}

	if !changed && inRemoteManagement && insertIndex >= 0 {
		lines = insertString(lines, insertIndex, strings.Repeat(" ", remoteIndent+2)+cpamanagerPanelRepositoryYAML())
		changed = true
	}
	if !changed {
		return configText, false
	}

	result := strings.Join(lines, newline)
	if hasTrailingNewline {
		result += newline
	}
	return result, true
}

func cpamanagerPanelRepositoryYAML() string {
	return fmt.Sprintf("panel-github-repository: %q", defaultCPAManagerPanelRepo)
}

func yamlScalarLine(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(stripYAMLComment(strings.TrimRight(line, "\r")))
	if trimmed == "" || strings.HasPrefix(trimmed, "-") {
		return "", "", false
	}
	colonIndex := findYAMLColon(trimmed)
	if colonIndex <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(trimmed[:colonIndex]), strings.TrimSpace(trimmed[colonIndex+1:]), true
}

func insertString(values []string, index int, value string) []string {
	if index < 0 || index > len(values) {
		index = len(values)
	}
	values = append(values, "")
	copy(values[index+1:], values[index:])
	values[index] = value
	return values
}

func ensureCPAManagerPanelAsset(executableDirectory string) (string, error) {
	executableDirectory = strings.TrimSpace(executableDirectory)
	if executableDirectory == "" {
		return "", nil
	}

	staticPanelPath := filepath.Join(executableDirectory, "static", "management.html")
	if panelSupportsUsageService(staticPanelPath) {
		return "", nil
	}

	localPanelPath := filepath.Join(executableDirectory, "management.html")
	if panelSupportsUsageService(localPanelPath) {
		if err := ensureDir(filepath.Dir(staticPanelPath)); err != nil {
			return "", err
		}
		data, err := os.ReadFile(localPanelPath)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(staticPanelPath, data, 0o644); err != nil {
			return "", err
		}
		return "copied", nil
	}

	if _, err := os.Stat(staticPanelPath); err == nil {
		if removeErr := os.Remove(staticPanelPath); removeErr != nil {
			return "", removeErr
		}
		return "removed", nil
	}
	return "", nil
}

func panelSupportsUsageService(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Contains(data, []byte("externalUsageService")) ||
		bytes.Contains(data, []byte("Usage Service")) ||
		bytes.Contains(data, []byte("CPA-Manager"))
}

func (l *launcherService) stopCPAManager() error {
	if l.cpaManager == nil {
		return nil
	}
	if _, ok := l.cpaManager.Runtime(); !ok {
		return nil
	}
	if err := l.cpaManager.Stop(context.Background()); err != nil {
		return fmt.Errorf("停止 CPA-Manager 失败: %w", err)
	}
	l.appendLog("info", "CPA-Manager 已停止。")
	return nil
}

func (l *launcherService) attachCPAManagerRuntime(info *LauncherRuntimeInfo) {
	if info == nil || l.cpaManager == nil {
		return
	}
	runtimeInfo, ok := l.cpaManager.Runtime()
	if !ok {
		return
	}
	info.CPAManagerURL = runtimeInfo.BaseURL
	info.CPAManagerHealthURL = runtimeInfo.HealthURL
	info.CPAManagerDBPath = runtimeInfo.DBPath
}

func (l *launcherService) checkForUpdate(settings AppSettings, silent bool, source string) (LauncherUpdateInfo, error) {
	diagnostics, err := launcherProxyDiagnosticsFromSettings(settings.Launcher, fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", strings.TrimSpace(stringOr(settings.Launcher.GitHubRepo, defaultLauncherRepo))))
	if err != nil {
		return LauncherUpdateInfo{}, err
	}
	if !silent {
		l.appendLog("info", launcherProxyDiagnosticsMessage(diagnostics))
	}

	release, err := l.fetchLatestRelease(settings.Launcher.GitHubRepo, settings.Launcher.LastInstalledVersion, diagnostics.EffectiveProxyURL)
	update := LauncherUpdateInfo{
		CurrentVersion: strings.TrimSpace(settings.Launcher.LastInstalledVersion),
		CheckedAt:      nowISO(),
		CheckSource:    source,
	}
	if err != nil {
		update.Message = err.Error()
		l.mu.Lock()
		l.update = update
		l.mu.Unlock()
		l.emitStatusIfPossible()
		return update, err
	}

	if release == nil {
		update.Message = "CPA 已是最新版本。"
		l.mu.Lock()
		l.update = update
		l.mu.Unlock()
		if !silent {
			l.appendLog("info", update.Message)
		}
		l.emitStatusIfPossible()
		return update, nil
	}

	update.Available = true
	update.TagName = release.TagName
	update.AssetSize = release.AssetSize
	update.ReleaseURL = release.ReleaseURL
	update.Message = fmt.Sprintf("发现 CPA 新版本 %s。", release.TagName)

	l.mu.Lock()
	l.update = update
	l.mu.Unlock()
	if !silent {
		l.appendLog("info", update.Message)
	}
	l.emitStatusIfPossible()
	return update, nil
}

func (l *launcherService) checkCPAManagerForUpdate(settings AppSettings, silent bool, source string) (LauncherUpdateInfo, error) {
	diagnostics, err := launcherProxyDiagnosticsFromSettings(settings.Launcher, fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", defaultCPAManagerRepo))
	if err != nil {
		return LauncherUpdateInfo{}, err
	}

	release, err := l.fetchLatestReleaseMetadata(defaultCPAManagerRepo, embeddedCPAManagerVersion, diagnostics.EffectiveProxyURL, "CPA-Manager")
	update := LauncherUpdateInfo{
		CurrentVersion: embeddedCPAManagerVersion,
		CheckedAt:      nowISO(),
		CheckSource:    source,
	}
	if err != nil {
		update.Message = err.Error()
		l.mu.Lock()
		l.cpaManagerUpdate = update
		l.mu.Unlock()
		l.emitStatusIfPossible()
		return update, err
	}

	if release == nil {
		update.Message = "CPA-Manager 已是最新版本。"
		l.mu.Lock()
		l.cpaManagerUpdate = update
		l.mu.Unlock()
		if !silent {
			l.appendLog("info", update.Message)
		}
		l.emitStatusIfPossible()
		return update, nil
	}

	update.Available = true
	update.TagName = release.TagName
	update.ReleaseURL = release.ReleaseURL
	update.Message = fmt.Sprintf("CPA-Manager 上游最新版本为 %s；当前 Usage Service 随 Control Center 内嵌更新。", release.TagName)

	l.mu.Lock()
	l.cpaManagerUpdate = update
	l.mu.Unlock()
	if !silent {
		l.appendLog("info", update.Message)
	}
	l.emitStatusIfPossible()
	return update, nil
}

func (l *launcherService) emitStatusIfPossible() {
	snapshot, err := l.Refresh()
	if err == nil {
		l.emitStatus(snapshot)
	}
}

func (l *launcherService) fetchLatestRelease(repo string, currentVersion string, proxyURL string) (*launcherReleaseInfo, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		repo = defaultLauncherRepo
	}

	request, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "CPA-Control-Center")
	request.Header.Set("Accept", "application/vnd.github+json")

	client, proxyLabel, err := l.downloadClientForProxy(proxyURL, request.URL.String())
	if err != nil {
		return nil, err
	}

	response, err := client.Do(request)
	if err != nil {
		if proxyLabel != "" {
			return nil, fmt.Errorf("获取 CPA 最新版本失败（%s）: %w", proxyLabel, err)
		}
		return nil, fmt.Errorf("获取 CPA 最新版本失败: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("获取 CPA 最新版本失败: HTTP %d", response.StatusCode)
	}

	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析 GitHub 发布信息失败: %w", err)
	}
	if strings.TrimSpace(payload.TagName) == "" {
		return nil, errors.New("GitHub 发布信息缺少版本号")
	}

	if compareVersions(strings.TrimSpace(currentVersion), strings.TrimSpace(payload.TagName)) >= 0 {
		return nil, nil
	}

	asset := selectReleaseAsset(payload.Assets)
	if asset == nil {
		return nil, errors.New("未找到适用于当前平台的 CPA 安装包")
	}

	return &launcherReleaseInfo{
		TagName:          payload.TagName,
		AssetDownloadURL: asset.BrowserDownloadURL,
		AssetSize:        asset.Size,
		ReleaseURL:       payload.HTMLURL,
	}, nil
}

func (l *launcherService) fetchLatestReleaseMetadata(repo string, currentVersion string, proxyURL string, label string) (*launcherReleaseInfo, error) {
	repo = strings.TrimSpace(repo)
	label = strings.TrimSpace(label)
	if label == "" {
		label = repo
	}

	requestURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	request, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "CPA-Control-Center")
	request.Header.Set("Accept", "application/vnd.github+json")

	client, proxyLabel, err := l.downloadClientForProxy(proxyURL, request.URL.String())
	if err != nil {
		return nil, err
	}

	response, err := client.Do(request)
	if err != nil {
		if proxyLabel != "" {
			return nil, fmt.Errorf("获取 %s 最新版本失败（%s）: %w", label, proxyLabel, err)
		}
		return nil, fmt.Errorf("获取 %s 最新版本失败: %w", label, err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("获取 %s 最新版本失败: HTTP %d", label, response.StatusCode)
	}

	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析 %s 发布信息失败: %w", label, err)
	}
	if strings.TrimSpace(payload.TagName) == "" {
		return nil, fmt.Errorf("%s 发布信息缺少版本号", label)
	}

	if compareVersions(strings.TrimSpace(currentVersion), strings.TrimSpace(payload.TagName)) >= 0 {
		return nil, nil
	}

	return &launcherReleaseInfo{
		TagName:    payload.TagName,
		ReleaseURL: payload.HTMLURL,
	}, nil
}

func selectReleaseAsset(assets []struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}) *struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
} {
	targetOSTokens := []string{currentGOOS()}
	targetArchTokens := []string{currentGOARCH()}

	switch currentGOOS() {
	case "windows":
		targetOSTokens = []string{"windows", "win"}
	case "darwin":
		targetOSTokens = []string{"darwin", "mac", "macos"}
	case "linux":
		targetOSTokens = []string{"linux"}
	}

	switch currentGOARCH() {
	case "amd64":
		targetArchTokens = []string{"amd64", "x64", "x86_64"}
	case "arm64":
		targetArchTokens = []string{"arm64", "aarch64"}
	}

	for index := range assets {
		name := strings.ToLower(strings.TrimSpace(assets[index].Name))
		if !strings.HasSuffix(name, ".zip") {
			continue
		}
		if !containsAnyToken(name, targetOSTokens) || !containsAnyToken(name, targetArchTokens) {
			continue
		}
		return &assets[index]
	}

	for index := range assets {
		name := strings.ToLower(strings.TrimSpace(assets[index].Name))
		if strings.HasSuffix(name, ".zip") {
			return &assets[index]
		}
	}
	return nil
}

func containsAnyToken(name string, tokens []string) bool {
	for _, token := range tokens {
		if strings.Contains(name, token) {
			return true
		}
	}
	return false
}

func compareVersions(left string, right string) int {
	left = strings.TrimSpace(strings.TrimPrefix(left, "v"))
	right = strings.TrimSpace(strings.TrimPrefix(right, "v"))
	if left == "" && right == "" {
		return 0
	}
	if left == "" {
		return -1
	}
	if right == "" {
		return 1
	}

	leftParts := versionParts(left)
	rightParts := versionParts(right)
	size := len(leftParts)
	if len(rightParts) > size {
		size = len(rightParts)
	}
	for index := 0; index < size; index++ {
		leftValue := 0
		if index < len(leftParts) {
			leftValue = leftParts[index]
		}
		rightValue := 0
		if index < len(rightParts) {
			rightValue = rightParts[index]
		}
		switch {
		case leftValue < rightValue:
			return -1
		case leftValue > rightValue:
			return 1
		}
	}
	return 0
}

func versionParts(value string) []int {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == '.' || r == '-' || r == '_'
	})
	parts := make([]int, 0, len(fields))
	for _, field := range fields {
		number, err := strconv.Atoi(field)
		if err != nil {
			break
		}
		parts = append(parts, number)
	}
	return parts
}

func (l *launcherService) downloadAndExtract(targetDirectory string, release *launcherReleaseInfo, proxyURL string) (string, error) {
	archivePath, err := l.downloadReleaseArchive(release, proxyURL)
	if err != nil {
		return "", err
	}
	defer tryDeleteFile(archivePath)

	if err := ensureDir(targetDirectory); err != nil {
		return "", err
	}

	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("打开 CPA 安装包失败: %w", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		destinationPath, err := resolveZipEntryDestination(targetDirectory, file.Name)
		if err != nil {
			return "", err
		}
		if file.FileInfo().IsDir() {
			if err := ensureDir(destinationPath); err != nil {
				return "", err
			}
			continue
		}
		if err := ensureDir(filepath.Dir(destinationPath)); err != nil {
			return "", err
		}
		if err := extractZipFile(file, destinationPath); err != nil {
			return "", err
		}
	}

	return findCPAExecutable(targetDirectory)
}

type launcherUpdatePlan struct {
	ShouldStop         bool
	ShouldRestart      bool
	BlockingProcessPID int
}

func determineLauncherUpdatePlan(managedByLauncher bool, matchingConfiguredProcessID int, executableInUsePID int) launcherUpdatePlan {
	switch {
	case managedByLauncher:
		return launcherUpdatePlan{ShouldStop: true, ShouldRestart: true}
	case matchingConfiguredProcessID > 0:
		return launcherUpdatePlan{ShouldStop: true, ShouldRestart: true}
	case executableInUsePID > 0:
		return launcherUpdatePlan{BlockingProcessPID: executableInUsePID}
	default:
		return launcherUpdatePlan{}
	}
}

func (l *launcherService) downloadAndReplaceExecutable(targetExecutablePath string, release *launcherReleaseInfo, proxyURL string) error {
	archivePath, err := l.downloadReleaseArchive(release, proxyURL)
	if err != nil {
		return err
	}
	defer tryDeleteFile(archivePath)

	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("打开 CPA 更新包失败: %w", err)
	}
	defer reader.Close()

	executableEntry := findExecutableEntry(reader.File)
	if executableEntry == nil {
		return errors.New("更新包中未找到 cli-proxy-api 可执行文件")
	}

	targetDirectory := filepath.Dir(targetExecutablePath)
	tempExecutable := filepath.Join(targetDirectory, fmt.Sprintf("cpa-update-%d%s", time.Now().UnixNano(), filepath.Ext(targetExecutablePath)))
	backupExecutable := targetExecutablePath + ".bak"

	if err := extractZipFile(executableEntry, tempExecutable); err != nil {
		return err
	}
	defer tryDeleteFile(tempExecutable)

	if _, err := os.Stat(targetExecutablePath); err == nil {
		tryDeleteFile(backupExecutable)
		if err := os.Rename(targetExecutablePath, backupExecutable); err != nil {
			return fmt.Errorf("备份旧的 CPA 可执行文件失败: %w", err)
		}
	}

	if err := os.Rename(tempExecutable, targetExecutablePath); err != nil {
		if _, statErr := os.Stat(backupExecutable); statErr == nil {
			_ = os.Rename(backupExecutable, targetExecutablePath)
		}
		return fmt.Errorf("替换 CPA 可执行文件失败: %w", err)
	}

	tryDeleteFile(backupExecutable)
	return nil
}

func (l *launcherService) downloadReleaseArchive(release *launcherReleaseInfo, proxyURL string) (string, error) {
	request, err := http.NewRequest(http.MethodGet, release.AssetDownloadURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "CPA-Control-Center")

	client, proxyLabel, err := l.downloadClientForProxy(proxyURL, request.URL.String())
	if err != nil {
		return "", err
	}

	response, err := client.Do(request)
	if err != nil {
		if proxyLabel != "" {
			return "", fmt.Errorf("下载 CPA 安装包失败（%s）: %w", proxyLabel, err)
		}
		return "", fmt.Errorf("下载 CPA 安装包失败: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("下载 CPA 安装包失败: HTTP %d", response.StatusCode)
	}

	archivePath := filepath.Join(os.TempDir(), fmt.Sprintf("cpa-launcher-%d.zip", time.Now().UnixNano()))
	file, err := os.Create(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	if _, err := io.Copy(file, response.Body); err != nil {
		return "", fmt.Errorf("写入 CPA 安装包失败: %w", err)
	}
	return archivePath, nil
}

func launcherProxyURLFromSettings(settings LauncherSettings) (string, error) {
	configPath, err := resolveLauncherConfigPathForRead(settings.ConfigPath, settings.ExecutablePath)
	if err != nil {
		return "", err
	}
	if configPath == "" {
		return "", nil
	}

	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("读取 CPA 配置文件失败: %w", err)
	}

	values := parseYAMLScalarValues(string(configBytes))
	return strings.TrimSpace(values["proxy-url"]), nil
}

func launcherProxyDiagnosticsFromSettings(settings LauncherSettings, requestURL string) (launcherProxyDiagnostics, error) {
	diagnostics := launcherProxyDiagnostics{
		ConfiguredConfigPath: strings.TrimSpace(settings.ConfigPath),
	}

	configPath, err := resolveLauncherConfigPathForRead(settings.ConfigPath, settings.ExecutablePath)
	if err != nil {
		return diagnostics, err
	}
	diagnostics.ResolvedConfigPath = configPath

	configProxyURL := ""
	if configPath != "" {
		configBytes, err := os.ReadFile(configPath)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return diagnostics, fmt.Errorf("读取 CPA 配置文件失败: %w", err)
			}
		} else {
			values := parseYAMLScalarValues(string(configBytes))
			configProxyURL = strings.TrimSpace(values["proxy-url"])
			diagnostics.ConfigProxyURL = redactLauncherProxyURL(configProxyURL)
		}
	}

	var envProxyURL *url.URL
	if strings.TrimSpace(requestURL) != "" {
		envProxyURL, err = environmentProxyURL(requestURL)
		if err != nil {
			return diagnostics, fmt.Errorf("解析环境代理失败: %w", err)
		}
		if envProxyURL != nil {
			diagnostics.EnvironmentProxyURL = envProxyURL.Redacted()
		}
	}

	switch {
	case configProxyURL != "":
		diagnostics.EffectiveProxyLabel = "CPA 配置代理"
		diagnostics.EffectiveProxyURL = configProxyURL
	case envProxyURL != nil:
		diagnostics.EffectiveProxyLabel = "环境代理"
		diagnostics.EffectiveProxyURL = envProxyURL.String()
	default:
		diagnostics.EffectiveProxyLabel = "直连"
		diagnostics.EffectiveProxyURL = ""
	}

	return diagnostics, nil
}

func launcherProxyDiagnosticsMessage(diagnostics launcherProxyDiagnostics) string {
	configuredPath := stringOr(strings.TrimSpace(diagnostics.ConfiguredConfigPath), "(空)")
	resolvedPath := stringOr(strings.TrimSpace(diagnostics.ResolvedConfigPath), "(未命中)")
	configProxy := stringOr(strings.TrimSpace(diagnostics.ConfigProxyURL), "(空)")
	envProxy := stringOr(strings.TrimSpace(diagnostics.EnvironmentProxyURL), "(空)")
	effective := diagnostics.EffectiveProxyLabel
	if strings.TrimSpace(diagnostics.EffectiveProxyURL) != "" {
		effective = fmt.Sprintf("%s %s", effective, redactLauncherProxyURL(diagnostics.EffectiveProxyURL))
	}
	return fmt.Sprintf("CPA 更新代理诊断：设置配置路径=%s；实际配置路径=%s；config代理=%s；环境代理=%s；最终=%s。", configuredPath, resolvedPath, configProxy, envProxy, effective)
}

func redactLauncherProxyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return parsed.Redacted()
}

func resolveLauncherConfigPathForRead(configPath string, executablePath string) (string, error) {
	candidates := make([]string, 0, 2)

	if trimmed := strings.TrimSpace(configPath); trimmed != "" {
		resolved, err := filepath.Abs(expandHomeDirectory(trimmed))
		if err != nil {
			return "", err
		}
		candidates = append(candidates, resolved)
	}

	if trimmedExecutable := strings.TrimSpace(executablePath); trimmedExecutable != "" {
		resolvedExecutable, err := filepath.Abs(expandHomeDirectory(trimmedExecutable))
		if err != nil {
			return "", err
		}
		fallback := filepath.Join(filepath.Dir(resolvedExecutable), "config.yaml")
		if !containsString(candidates, fallback) {
			candidates = append(candidates, fallback)
		}
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	if len(candidates) > 0 {
		return candidates[0], nil
	}
	return "", nil
}

func (l *launcherService) downloadClientForProxy(proxyURL string, requestURL string) (*http.Client, string, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		envProxyURL, err := environmentProxyURL(requestURL)
		if err != nil {
			return nil, "", fmt.Errorf("解析环境代理失败: %w", err)
		}
		if envProxyURL == nil {
			return l.downloadClient, "", nil
		}

		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(envProxyURL)

		return &http.Client{
			Timeout:   launcherDownloadTimeout,
			Transport: transport,
		}, fmt.Sprintf("使用环境代理 %s", envProxyURL.Redacted()), nil
	}

	parsedProxyURL, err := url.Parse(proxyURL)
	if err != nil || parsedProxyURL.Scheme == "" || parsedProxyURL.Host == "" {
		return nil, "", fmt.Errorf("CPA 配置中的代理地址无效: %s", proxyURL)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(parsedProxyURL)

	return &http.Client{
		Timeout:   launcherDownloadTimeout,
		Transport: transport,
	}, fmt.Sprintf("使用 CPA 配置代理 %s", parsedProxyURL.Redacted()), nil
}

func environmentProxyURL(requestURL string) (*url.URL, error) {
	request, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}

	proxyFunc := httpproxy.FromEnvironment().ProxyFunc()
	return proxyFunc(request.URL)
}

func extractZipFile(file *zip.File, destinationPath string) error {
	reader, err := file.Open()
	if err != nil {
		return fmt.Errorf("打开压缩包条目失败: %w", err)
	}
	defer reader.Close()

	targetFile, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
	if err != nil {
		return fmt.Errorf("创建目标文件失败: %w", err)
	}
	defer targetFile.Close()

	if _, err := io.Copy(targetFile, reader); err != nil {
		return fmt.Errorf("解压文件失败: %w", err)
	}
	return nil
}

func resolveZipEntryDestination(targetDirectory string, entryName string) (string, error) {
	targetDirectory = strings.TrimSpace(targetDirectory)
	if targetDirectory == "" {
		return "", errors.New("目标目录不能为空")
	}

	targetRoot, err := filepath.Abs(targetDirectory)
	if err != nil {
		return "", err
	}

	normalizedEntryName := strings.ReplaceAll(strings.TrimSpace(entryName), "\\", "/")
	cleanEntry := path.Clean(normalizedEntryName)
	switch {
	case cleanEntry == "." || cleanEntry == "":
		return "", fmt.Errorf("压缩包条目路径为空: %q", entryName)
	case strings.HasPrefix(cleanEntry, "/"):
		return "", fmt.Errorf("压缩包条目路径非法: %s", entryName)
	case cleanEntry == ".." || strings.HasPrefix(cleanEntry, "../"):
		return "", fmt.Errorf("压缩包条目超出目标目录: %s", entryName)
	case strings.Contains(cleanEntry, ":"):
		return "", fmt.Errorf("压缩包条目路径非法: %s", entryName)
	}

	destinationPath := filepath.Join(targetRoot, filepath.FromSlash(cleanEntry))
	relativePath, err := filepath.Rel(targetRoot, destinationPath)
	if err != nil {
		return "", fmt.Errorf("解析压缩包条目路径失败: %w", err)
	}
	if relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("压缩包条目超出目标目录: %s", entryName)
	}
	return destinationPath, nil
}

func launcherProcessMatchesConfig(commandLine string, workingDirectory string, expectedConfigPath string) bool {
	commandLine = strings.TrimSpace(commandLine)
	expectedConfigPath = normalizeLauncherArgumentPath(expectedConfigPath, "")
	if commandLine == "" || expectedConfigPath == "" {
		return false
	}

	args, err := launcherCommandLineArguments(commandLine)
	if err != nil {
		return false
	}
	return launcherArgsMatchConfig(args, workingDirectory, expectedConfigPath)
}

func launcherArgsMatchConfig(args []string, workingDirectory string, expectedConfigPath string) bool {
	configPath, ok := launcherConfigPathFromArgs(args, workingDirectory)
	if !ok {
		return false
	}
	return configPath == normalizeLauncherArgumentPath(expectedConfigPath, "")
}

func launcherConfigPathFromArgs(args []string, workingDirectory string) (string, bool) {
	for index := 1; index < len(args); index++ {
		argument := strings.TrimSpace(args[index])
		switch {
		case argument == "--config" || argument == "-config":
			if index+1 >= len(args) {
				return "", false
			}
			configPath := normalizeLauncherArgumentPath(args[index+1], workingDirectory)
			return configPath, configPath != ""
		case strings.HasPrefix(argument, "--config="):
			configPath := normalizeLauncherArgumentPath(strings.TrimPrefix(argument, "--config="), workingDirectory)
			return configPath, configPath != ""
		case strings.HasPrefix(argument, "-config="):
			configPath := normalizeLauncherArgumentPath(strings.TrimPrefix(argument, "-config="), workingDirectory)
			return configPath, configPath != ""
		}
	}
	return "", false
}

func normalizeLauncherArgumentPath(rawPath string, workingDirectory string) string {
	rawPath = strings.Trim(strings.TrimSpace(rawPath), `"'`)
	if rawPath == "" {
		return ""
	}

	resolvedPath := expandHomeDirectory(rawPath)
	if !filepath.IsAbs(resolvedPath) {
		workingDirectory = strings.TrimSpace(workingDirectory)
		if workingDirectory == "" {
			return ""
		}
		resolvedPath = filepath.Join(workingDirectory, resolvedPath)
	}

	absolutePath, err := filepath.Abs(resolvedPath)
	if err != nil {
		return ""
	}
	return normalizeLauncherProcessPath(absolutePath)
}

func findCPAExecutable(root string) (string, error) {
	var matches []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if isCPAExecutableName(entry.Name()) {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", errors.New("安装包中未找到 cli-proxy-api 可执行文件")
	}
	return matches[0], nil
}

func findExecutableEntry(files []*zip.File) *zip.File {
	for _, file := range files {
		if file.FileInfo().IsDir() {
			continue
		}
		if isCPAExecutableName(filepath.Base(file.Name)) {
			return file
		}
	}
	return nil
}

func isCPAExecutableName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == "cli-proxy-api.exe" || lower == "cli-proxy-api"
}

func tryDeleteFile(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	_ = os.Remove(path)
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return killProcessTreeByPID(cmd.Process.Pid)
}

func waitForProcessExitByPID(pid int, timeout time.Duration) bool {
	if pid <= 0 {
		return true
	}

	deadline := time.Now().Add(timeout)
	for {
		if !processExists(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func hasLauncherPaths(settings LauncherSettings) bool {
	return strings.TrimSpace(settings.ExecutablePath) != "" || strings.TrimSpace(settings.ConfigPath) != ""
}

func inspectLauncherRuntime(executablePath string, configPath string) (LauncherRuntimeInfo, error) {
	executablePath = strings.TrimSpace(executablePath)
	configPath = strings.TrimSpace(configPath)
	if executablePath == "" {
		return LauncherRuntimeInfo{}, errors.New("未提供 CPA 可执行文件路径")
	}
	if configPath == "" {
		return LauncherRuntimeInfo{}, errors.New("未提供 CPA 配置文件路径")
	}

	executableFullPath, err := filepath.Abs(executablePath)
	if err != nil {
		return LauncherRuntimeInfo{}, err
	}
	if _, err := os.Stat(executableFullPath); err != nil {
		return LauncherRuntimeInfo{}, fmt.Errorf("找不到 CPA 可执行文件: %w", err)
	}

	configFullPath, err := filepath.Abs(configPath)
	if err != nil {
		return LauncherRuntimeInfo{}, err
	}
	if _, err := os.Stat(configFullPath); err != nil {
		return LauncherRuntimeInfo{}, fmt.Errorf("找不到 CPA 配置文件: %w", err)
	}

	configBytes, err := os.ReadFile(configFullPath)
	if err != nil {
		return LauncherRuntimeInfo{}, fmt.Errorf("读取 CPA 配置文件失败: %w", err)
	}
	values := parseYAMLScalarValues(string(configBytes))

	bindHost := strings.TrimSpace(values["host"])
	accessHost := normalizeAccessHost(bindHost)
	port := parseInt(values["port"], defaultCPAListenPort)
	useTLS := parseBool(values["tls.enable"], false)
	authDirectory := expandHomeDirectory(values["auth-dir"])
	loggingToFile := parseBool(values["logging-to-file"], false)
	usageStatisticsEnabled := parseBool(values["usage-statistics-enabled"], false)
	controlPanelDisabled := parseBool(values["remote-management.disable-control-panel"], false)
	managementSecretKey := strings.TrimSpace(values["remote-management.secret-key"])

	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	baseURL := fmt.Sprintf("%s://%s:%d", scheme, accessHost, port)
	executableDirectory := filepath.Dir(executableFullPath)

	return LauncherRuntimeInfo{
		ExecutablePath:             executableFullPath,
		ExecutableDirectory:        executableDirectory,
		ConfigPath:                 configFullPath,
		ConfigDirectory:            filepath.Dir(configFullPath),
		BindHost:                   stringOr(bindHost, "0.0.0.0"),
		AccessHost:                 accessHost,
		Port:                       port,
		UseTLS:                     useTLS,
		LoggingToFile:              loggingToFile,
		UsageStatisticsEnabled:     usageStatisticsEnabled,
		ControlPanelDisabled:       controlPanelDisabled,
		ManagementSecretConfigured: managementSecretKey != "",
		ManagementSecretKey:        managementSecretKey,
		AuthDirectory:              authDirectory,
		LogDirectory:               resolveLauncherLogDirectory(executableDirectory, authDirectory),
		BaseURL:                    baseURL,
		ManagementURL:              baseURL + "/management.html#/login",
		ServiceProbeURL:            baseURL,
	}, nil
}

func writeDefaultLauncherConfig(configPath string, host string, port int, proxyURL string, secretKey string) error {
	var builder strings.Builder

	builder.WriteString("# Server host/interface to bind to.\n")
	builder.WriteString("# Use 127.0.0.1 or localhost to allow local access only.\n")
	builder.WriteString(fmt.Sprintf("host: %q\n", host))
	builder.WriteString(fmt.Sprintf("port: %d\n", port))
	builder.WriteString("tls:\n")
	builder.WriteString("  enable: false\n")
	builder.WriteString("  cert: \"\"\n")
	builder.WriteString("  key: \"\"\n")
	builder.WriteString("remote-management:\n")
	builder.WriteString("  allow-remote: false\n")
	builder.WriteString(fmt.Sprintf("  secret-key: %q\n", strings.TrimSpace(secretKey)))
	builder.WriteString("  disable-control-panel: false\n")
	builder.WriteString(fmt.Sprintf("  panel-github-repository: %q\n", defaultCPAManagerPanelRepo))
	builder.WriteString(fmt.Sprintf("auth-dir: %q\n", filepath.Join(userHomeDirectory(), ".cli-proxy-api")))
	builder.WriteString("api-keys: []\n")
	builder.WriteString("debug: false\n")
	builder.WriteString("pprof:\n")
	builder.WriteString("  enable: false\n")
	builder.WriteString("  addr: \"127.0.0.1:8316\"\n")
	builder.WriteString("commercial-mode: false\n")
	builder.WriteString("logging-to-file: true\n")
	builder.WriteString("logs-max-total-size-mb: 512\n")
	builder.WriteString("error-logs-max-files: 10\n")
	builder.WriteString("usage-statistics-enabled: true\n")
	builder.WriteString(fmt.Sprintf("proxy-url: %q\n", strings.TrimSpace(proxyURL)))
	builder.WriteString("force-model-prefix: false\n")
	builder.WriteString("passthrough-headers: false\n")
	builder.WriteString("request-retry: 3\n")
	builder.WriteString("max-retry-credentials: 0\n")
	builder.WriteString("max-retry-interval: 30\n")
	builder.WriteString("quota-exceeded:\n")
	builder.WriteString("  switch-project: true\n")
	builder.WriteString("  switch-preview-model: true\n")
	builder.WriteString("routing:\n")
	builder.WriteString("  strategy: \"round-robin\"\n")
	builder.WriteString("ws-auth: false\n")
	builder.WriteString("nonstream-keepalive-interval: 0\n")
	builder.WriteString("codex-api-key: []\n")
	builder.WriteString("openai-compatibility: []\n")

	if err := ensureDir(filepath.Dir(configPath)); err != nil {
		return err
	}
	return os.WriteFile(configPath, []byte(builder.String()), 0o644)
}

func resolveLauncherLogDirectory(executableDirectory string, authDirectory string) string {
	for _, envKey := range []string{"WRITABLE_PATH", "writable_path"} {
		if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
			return filepath.Join(value, "logs")
		}
	}

	localLogs := filepath.Join(executableDirectory, "logs")
	if directoryExists(localLogs) {
		return localLogs
	}
	if authDirectory != "" {
		return filepath.Join(authDirectory, "logs")
	}
	return localLogs
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func normalizeAccessHost(bindHost string) string {
	switch strings.Trim(strings.TrimSpace(bindHost), `"'`) {
	case "", "0.0.0.0", "::", "[::]":
		return "127.0.0.1"
	default:
		return strings.Trim(strings.TrimSpace(bindHost), `"'`)
	}
}

func expandHomeDirectory(path string) string {
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "~") {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return path
		}
		return absolute
	}
	remainder := strings.TrimLeft(strings.TrimPrefix(path, "~"), `/\`)
	if remainder == "" {
		return userHomeDirectory()
	}
	return filepath.Join(userHomeDirectory(), remainder)
}

func userHomeDirectory() string {
	if directory, err := os.UserHomeDir(); err == nil && directory != "" {
		return directory
	}
	return "."
}

func parseYAMLScalarValues(yamlText string) map[string]string {
	result := make(map[string]string)
	type node struct {
		indent int
		key    string
	}
	var stack []node

	for _, rawLine := range strings.Split(yamlText, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		line = stripYAMLComment(line)
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := leadingSpaces(line)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-") {
			continue
		}
		colonIndex := findYAMLColon(trimmed)
		if colonIndex <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:colonIndex])
		value := strings.TrimSpace(trimmed[colonIndex+1:])

		for len(stack) > 0 && indent <= stack[len(stack)-1].indent {
			stack = stack[:len(stack)-1]
		}

		if value == "" {
			stack = append(stack, node{indent: indent, key: key})
			continue
		}

		segments := make([]string, 0, len(stack)+1)
		for _, item := range stack {
			segments = append(segments, item.key)
		}
		segments = append(segments, key)
		result[strings.Join(segments, ".")] = unquoteYAMLValue(value)
	}

	return result
}

func stripYAMLComment(line string) string {
	var builder strings.Builder
	inSingleQuotes := false
	inDoubleQuotes := false

	for _, character := range line {
		switch character {
		case '\'':
			if !inDoubleQuotes {
				inSingleQuotes = !inSingleQuotes
			}
			builder.WriteRune(character)
		case '"':
			if !inSingleQuotes {
				inDoubleQuotes = !inDoubleQuotes
			}
			builder.WriteRune(character)
		case '#':
			if !inSingleQuotes && !inDoubleQuotes {
				return strings.TrimRight(builder.String(), " ")
			}
			builder.WriteRune(character)
		default:
			builder.WriteRune(character)
		}
	}

	return strings.TrimRight(builder.String(), " ")
}

func leadingSpaces(line string) int {
	count := 0
	for _, character := range line {
		if character != ' ' {
			break
		}
		count++
	}
	return count
}

func findYAMLColon(line string) int {
	inSingleQuotes := false
	inDoubleQuotes := false
	for index, character := range line {
		switch character {
		case '\'':
			if !inDoubleQuotes {
				inSingleQuotes = !inSingleQuotes
			}
		case '"':
			if !inSingleQuotes {
				inDoubleQuotes = !inDoubleQuotes
			}
		case ':':
			if !inSingleQuotes && !inDoubleQuotes {
				return index
			}
		}
	}
	return -1
}

func unquoteYAMLValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func parseInt(value string, fallback int) int {
	number, err := strconv.Atoi(strings.Trim(strings.TrimSpace(value), `"'`))
	if err != nil {
		return fallback
	}
	return number
}

func parseBool(value string, fallback bool) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(value), `"'`)) {
	case "true":
		return true
	case "false":
		return false
	default:
		return fallback
	}
}

func launcherText(locale string, english string, chinese string) string {
	if localeOrDefault(locale) == localeChinese {
		return chinese
	}
	return english
}

func currentGOOS() string {
	return goruntime.GOOS
}

func currentGOARCH() string {
	return goruntime.GOARCH
}
