package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"

	"cpa-control-center/internal/backend"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx        context.Context
	backend    *backend.Backend
	initErr    error
	tray       trayController
	allowClose atomic.Bool
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	dataDir, err := backend.DefaultDataDir()
	if err != nil {
		a.initErr = err
		return
	}
	service, err := backend.New(dataDir, a)
	if err != nil {
		a.initErr = err
		return
	}
	a.backend = service

	settings, err := service.GetSettings()
	if err != nil {
		a.initErr = err
		return
	}
	tray, err := newTrayController(
		trayLabelsForLocale(settings.Locale),
		trayActions{
			Show:           a.showWindowFromTray,
			Start:          a.startCPAFromTray,
			Stop:           a.stopCPAFromTray,
			OpenManagement: a.openManagementFromTray,
			QuitLauncher:   a.quitLauncherFromTray,
			CurrentState:   a.currentTrayMenuState,
		},
	)
	if err == nil {
		a.tray = tray
	} else {
		a.logTrayWarningf("初始化托盘失败: %v", err)
	}
}

func (a *App) domReady(ctx context.Context) {
	a.ctx = ctx
	go func() {
		if err := applyNativeWindowIcon(appTitle); err != nil {
			a.logTrayWarningf("设置窗口图标失败: %v", err)
		}
	}()
}

func (a *App) shutdown(ctx context.Context) {
	if a.tray != nil {
		_ = a.tray.Close()
	}
	if a.backend != nil {
		_ = a.backend.Close()
	}
	releaseNativeAppIcon()
}

func (a *App) beforeClose(ctx context.Context) bool {
	if a.allowClose.Load() {
		return false
	}
	if !a.shouldMinimizeToTray() {
		return false
	}
	runtime.WindowHide(ctx)
	return true
}

func (a *App) ensureBackend() (*backend.Backend, error) {
	if a.initErr != nil {
		return nil, a.initErr
	}
	if a.backend == nil {
		return nil, errors.New("backend not initialized")
	}
	return a.backend, nil
}

func (a *App) Emit(event string, payload any) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, event, payload)
	}
}

func (a *App) GetSettings() (backend.AppSettings, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.AppSettings{}, err
	}
	return service.GetSettings()
}

func (a *App) GetCodexLocalConfigSnapshot() (backend.CodexLocalConfigSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.CodexLocalConfigSnapshot{}, err
	}
	return service.GetCodexLocalConfigSnapshot()
}

func (a *App) ImportCurrentCodexLocalConfig(input backend.CodexLocalConfigImportInput) (backend.CodexLocalConfigSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.CodexLocalConfigSnapshot{}, err
	}
	return service.ImportCurrentCodexLocalConfig(input)
}

func (a *App) GetCodexLocalConfigProfileContent(name string) (backend.CodexLocalConfigProfileContent, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.CodexLocalConfigProfileContent{}, err
	}
	return service.GetCodexLocalConfigProfileContent(name)
}

func (a *App) ReloadCodexLocalConfigProfileContent(name string) (backend.CodexLocalConfigProfileContent, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.CodexLocalConfigProfileContent{}, err
	}
	return service.ReloadCodexLocalConfigProfileContent(name)
}

func (a *App) SaveCodexLocalConfigProfileContent(input backend.CodexLocalConfigSaveInput) (backend.CodexLocalConfigProfileContent, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.CodexLocalConfigProfileContent{}, err
	}
	return service.SaveCodexLocalConfigProfileContent(input)
}

func (a *App) ImportCodexLocalConfigProfile() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择要导入的 Codex 配置文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "JSON 配置包", Pattern: "*.json"},
			{DisplayName: "所有文件", Pattern: "*.*"},
		},
	})
	if err != nil {
		return "", err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}

	service, err := a.ensureBackend()
	if err != nil {
		return "", err
	}
	return service.ImportCodexLocalConfigProfileFromFile(path)
}

func (a *App) ExportCodexLocalConfigProfile(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("供应商名称不能为空")
	}

	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出 Codex 配置文件",
		DefaultFilename: defaultCodexLocalConfigExportFileName(name),
		Filters: []runtime.FileFilter{
			{DisplayName: "JSON 配置包", Pattern: "*.json"},
			{DisplayName: "所有文件", Pattern: "*.*"},
		},
	})
	if err != nil {
		return "", err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}

	service, err := a.ensureBackend()
	if err != nil {
		return "", err
	}
	return service.ExportCodexLocalConfigProfileToFile(name, path)
}

func (a *App) ImportCodexLocalConfigProfiles() (backend.CodexLocalConfigTransferResult, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择要导入的 Codex 配置包",
		Filters: []runtime.FileFilter{
			{DisplayName: "JSON 配置包", Pattern: "*.json"},
			{DisplayName: "所有文件", Pattern: "*.*"},
		},
	})
	if err != nil {
		return backend.CodexLocalConfigTransferResult{}, err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return backend.CodexLocalConfigTransferResult{}, nil
	}

	service, err := a.ensureBackend()
	if err != nil {
		return backend.CodexLocalConfigTransferResult{}, err
	}
	return service.ImportCodexLocalConfigProfilesFromFile(path)
}

func (a *App) ExportCodexLocalConfigProfiles() (backend.CodexLocalConfigTransferResult, error) {
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出全部 Codex 配置",
		DefaultFilename: "codex-profiles.bundle.json",
		Filters: []runtime.FileFilter{
			{DisplayName: "JSON 配置包", Pattern: "*.json"},
			{DisplayName: "所有文件", Pattern: "*.*"},
		},
	})
	if err != nil {
		return backend.CodexLocalConfigTransferResult{}, err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return backend.CodexLocalConfigTransferResult{}, nil
	}

	service, err := a.ensureBackend()
	if err != nil {
		return backend.CodexLocalConfigTransferResult{}, err
	}
	return service.ExportCodexLocalConfigProfilesToFile(path)
}

func (a *App) TestCodexLocalConfigProfileContent(input backend.CodexLocalConfigSaveInput) (backend.CodexLocalConfigValidationResult, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.CodexLocalConfigValidationResult{}, err
	}
	return service.TestCodexLocalConfigProfileContent(input)
}

func (a *App) TestCodexLocalConfigProfileConnection(name string) (backend.CodexLocalConfigConnectionTestResult, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.CodexLocalConfigConnectionTestResult{}, err
	}
	return service.TestCodexLocalConfigProfileConnection(name)
}

func (a *App) SwitchCodexLocalConfigProfile(input backend.CodexLocalConfigSwitchInput) (backend.CodexLocalConfigSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.CodexLocalConfigSnapshot{}, err
	}
	return service.SwitchCodexLocalConfigProfile(input)
}

func (a *App) DeleteCodexLocalConfigProfile(name string) (backend.CodexLocalConfigSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.CodexLocalConfigSnapshot{}, err
	}
	return service.DeleteCodexLocalConfigProfile(name)
}

func (a *App) SaveSettings(input backend.AppSettings) (backend.AppSettings, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.AppSettings{}, err
	}
	saved, err := service.SaveSettings(input)
	if err != nil {
		return backend.AppSettings{}, err
	}
	a.syncTrayLocale(saved.Locale)
	return saved, nil
}

func (a *App) TestConnection(input backend.AppSettings) (backend.ConnectionResult, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.ConnectionResult{}, err
	}
	return service.TestConnection(input)
}

func (a *App) TestAndSaveSettings(input backend.AppSettings) (backend.ConnectionResult, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.ConnectionResult{}, err
	}
	result, err := service.TestAndSaveSettings(input)
	if err != nil {
		return backend.ConnectionResult{}, err
	}
	if settings, settingsErr := service.GetSettings(); settingsErr == nil {
		a.syncTrayLocale(settings.Locale)
	}
	return result, nil
}

func (a *App) SyncInventory() (backend.InventorySyncResult, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.InventorySyncResult{}, err
	}
	return service.SyncInventory()
}

func (a *App) GetSchedulerStatus() (backend.SchedulerStatus, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.SchedulerStatus{}, err
	}
	return service.GetSchedulerStatus(), nil
}

func (a *App) GetDashboardSummary() (backend.DashboardSummary, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.DashboardSummary{}, err
	}
	return service.GetDashboardSummary()
}

func (a *App) GetDashboardSnapshot() (backend.DashboardSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.DashboardSnapshot{}, err
	}
	return service.GetDashboardSnapshot()
}

func (a *App) GetCodexQuotaSnapshot() (backend.CodexQuotaSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.CodexQuotaSnapshot{}, err
	}
	return service.GetCodexQuotaSnapshot()
}

func (a *App) ListAccounts(filter backend.AccountFilter) ([]backend.AccountRecord, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return nil, err
	}
	return service.ListAccounts(filter)
}

func (a *App) ListAccountsPage(filter backend.AccountFilter, page int, pageSize int) (backend.AccountPage, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.AccountPage{}, err
	}
	return service.ListAccountsPage(filter, page, pageSize)
}

func (a *App) RunScan() (backend.ScanSummary, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.ScanSummary{}, err
	}
	return service.RunScan()
}

func (a *App) CancelScan() (bool, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return false, err
	}
	return service.CancelScan()
}

func (a *App) RunMaintain(options backend.MaintainOptions) (backend.MaintainResult, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.MaintainResult{}, err
	}
	return service.RunMaintain(options)
}

func (a *App) ProbeAccount(name string) (backend.AccountRecord, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.AccountRecord{}, err
	}
	return service.ProbeAccount(name)
}

func (a *App) ProbeAccounts(names []string) (backend.BulkAccountActionResult, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.BulkAccountActionResult{}, err
	}
	return service.ProbeAccounts(names)
}

func (a *App) SetAccountDisabled(name string, disabled bool) (backend.ActionResult, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.ActionResult{}, err
	}
	return service.SetAccountDisabled(name, disabled)
}

func (a *App) SetAccountsDisabled(names []string, disabled bool) (backend.BulkAccountActionResult, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.BulkAccountActionResult{}, err
	}
	return service.SetAccountsDisabled(names, disabled)
}

func (a *App) DeleteAccount(name string) (backend.ActionResult, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.ActionResult{}, err
	}
	return service.DeleteAccount(name)
}

func (a *App) DeleteAccounts(names []string) (backend.BulkAccountActionResult, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.BulkAccountActionResult{}, err
	}
	return service.DeleteAccounts(names)
}

func (a *App) ExportAccounts(kind string, format string, path string) (backend.ExportResult, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.ExportResult{}, err
	}
	return service.ExportAccounts(kind, format, path)
}

func (a *App) ListScanHistory(limit int) ([]backend.ScanSummary, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return nil, err
	}
	return service.ListScanHistory(limit)
}

func (a *App) GetScanDetails(runID int64) (backend.ScanDetail, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.ScanDetail{}, err
	}
	return service.GetScanDetails(runID)
}

func (a *App) GetScanDetailsPage(runID int64, page int, pageSize int) (backend.ScanDetailPage, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.ScanDetailPage{}, err
	}
	return service.GetScanDetailsPage(runID, page, pageSize)
}

func (a *App) GetLauncherStatus() (backend.LauncherStatusSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.LauncherStatusSnapshot{}, err
	}
	return service.GetLauncherStatus()
}

func (a *App) SaveLauncherSettings(input backend.LauncherSettings) (backend.LauncherStatusSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.LauncherStatusSnapshot{}, err
	}
	return service.SaveLauncherSettings(input)
}

func (a *App) RefreshLauncherStatus() (backend.LauncherStatusSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.LauncherStatusSnapshot{}, err
	}
	return service.RefreshLauncherStatus()
}

func (a *App) StartLauncherService() (backend.LauncherStatusSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.LauncherStatusSnapshot{}, err
	}
	return service.StartLauncherService()
}

func (a *App) StopLauncherService() (backend.LauncherStatusSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.LauncherStatusSnapshot{}, err
	}
	return service.StopLauncherService()
}

func (a *App) ClearLauncherLogs() (backend.LauncherStatusSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.LauncherStatusSnapshot{}, err
	}
	return service.ClearLauncherLogs()
}

func (a *App) CheckLauncherForUpdate() (backend.LauncherStatusSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.LauncherStatusSnapshot{}, err
	}
	return service.CheckLauncherForUpdate()
}

func (a *App) InstallLauncherLatest(targetDirectory string) (backend.LauncherStatusSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.LauncherStatusSnapshot{}, err
	}
	return service.InstallLauncherLatest(targetDirectory)
}

func (a *App) UpdateLauncherCPA() (backend.LauncherStatusSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.LauncherStatusSnapshot{}, err
	}
	return service.UpdateLauncherCPA()
}

func (a *App) UpdateLauncherCPAManager() (backend.LauncherStatusSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.LauncherStatusSnapshot{}, err
	}
	return service.UpdateLauncherCPAManager()
}

func (a *App) GenerateLauncherConfig(input backend.LauncherConfigTemplateInput) (backend.LauncherStatusSnapshot, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.LauncherStatusSnapshot{}, err
	}
	return service.GenerateLauncherConfig(input)
}

func (a *App) ApplyLauncherConnection() (backend.AppSettings, error) {
	service, err := a.ensureBackend()
	if err != nil {
		return backend.AppSettings{}, err
	}
	return service.ApplyLauncherConnection()
}

func (a *App) SelectLauncherExecutablePath() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择 cli-proxy-api 可执行文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "可执行文件", Pattern: "*.exe"},
			{DisplayName: "所有文件", Pattern: "*.*"},
		},
	})
}

func (a *App) SelectLauncherConfigPath() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择 config.yaml",
		Filters: []runtime.FileFilter{
			{DisplayName: "YAML 配置", Pattern: "*.yaml;*.yml"},
			{DisplayName: "所有文件", Pattern: "*.*"},
		},
	})
}

func (a *App) SelectLauncherInstallDirectory() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择 CPA 安装目录",
	})
}

func (a *App) SelectLauncherConfigSavePath() (string, error) {
	return runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "保存默认 CPA 配置",
		DefaultFilename: "config.yaml",
		Filters: []runtime.FileFilter{
			{DisplayName: "YAML 配置", Pattern: "*.yaml"},
			{DisplayName: "所有文件", Pattern: "*.*"},
		},
	})
}

func (a *App) OpenLauncherManagementPage() error {
	service, err := a.ensureBackend()
	if err != nil {
		return err
	}
	managementURL, err := service.LauncherManagementURL()
	if err != nil {
		return err
	}
	if strings.TrimSpace(managementURL) == "" {
		return errors.New("当前没有可打开的管理页地址")
	}
	runtime.BrowserOpenURL(a.ctx, managementURL)
	return nil
}

func (a *App) OpenLauncherLogsDirectory() error {
	snapshot, err := a.GetLauncherStatus()
	if err != nil {
		return err
	}
	if snapshot.Runtime == nil || snapshot.Runtime.LogDirectory == "" {
		return errors.New("当前没有可打开的日志目录")
	}
	return openPathWithSystem(snapshot.Runtime.LogDirectory)
}

func (a *App) OpenLauncherExecutableDirectory() error {
	snapshot, err := a.GetLauncherStatus()
	if err != nil {
		return err
	}
	if snapshot.Runtime == nil || snapshot.Runtime.ExecutableDirectory == "" {
		return errors.New("当前没有可打开的可执行文件目录")
	}
	return openPathWithSystem(snapshot.Runtime.ExecutableDirectory)
}

func (a *App) OpenLauncherConfigDirectory() error {
	snapshot, err := a.GetLauncherStatus()
	if err != nil {
		return err
	}
	if snapshot.Runtime == nil || snapshot.Runtime.ConfigDirectory == "" {
		return errors.New("当前没有可打开的配置目录")
	}
	return openPathWithSystem(snapshot.Runtime.ConfigDirectory)
}

func (a *App) OpenCodexLocalConfigDirectory() error {
	snapshot, err := a.GetCodexLocalConfigSnapshot()
	if err != nil {
		return err
	}
	if snapshot.DefaultDirectory == "" {
		return errors.New("当前没有可打开的 Codex 配置目录")
	}
	if err := os.MkdirAll(snapshot.DefaultDirectory, 0o755); err != nil {
		return err
	}
	return openPathWithSystem(snapshot.DefaultDirectory)
}

func openPathWithSystem(path string) error {
	target := filepath.Clean(path)
	cmd := exec.Command("cmd", "/c", "start", "", target)
	return cmd.Start()
}

func defaultCodexLocalConfigExportFileName(name string) string {
	replacer := strings.NewReplacer("\\", "-", "/", "-", ":", "-", "*", "-", "?", "", "\"", "", "<", "", ">", "", "|", "-")
	cleaned := strings.TrimSpace(replacer.Replace(name))
	if cleaned == "" {
		cleaned = "codex-profile"
	}
	return cleaned + ".codex-profile.json"
}

func (a *App) shouldMinimizeToTray() bool {
	if a.tray == nil || !a.tray.Ready() {
		return false
	}
	service, err := a.ensureBackend()
	if err != nil {
		return false
	}
	settings, err := service.GetSettings()
	if err != nil {
		return false
	}
	return settings.Launcher.MinimizeToTrayOnClose
}

func (a *App) syncTrayLocale(locale string) {
	if a.tray == nil {
		return
	}
	a.tray.UpdateLabels(trayLabelsForLocale(locale))
}

func trayMenuStateFromSnapshot(snapshot backend.LauncherStatusSnapshot) trayMenuState {
	status := strings.ToLower(strings.TrimSpace(snapshot.Status))
	canStart := status == "stopped" || status == "start_failed"
	canStop := status == "starting" || status == "running" || status == "stopping"
	canOpenManagement := snapshot.Runtime != nil && strings.TrimSpace(snapshot.Runtime.ManagementURL) != ""
	return trayMenuState{
		CanStart:          canStart,
		CanStop:           canStop,
		CanOpenManagement: canOpenManagement,
	}
}

func (a *App) currentTrayMenuState() trayMenuState {
	snapshot, err := a.GetLauncherStatus()
	if err != nil {
		a.logTrayWarningf("获取托盘菜单状态失败: %v", err)
		return trayMenuState{}
	}
	return trayMenuStateFromSnapshot(snapshot)
}

func (a *App) logTrayWarningf(format string, args ...interface{}) {
	if a.ctx != nil {
		runtime.LogWarningf(a.ctx, format, args...)
	}
}

func (a *App) showWindowFromTray() {
	if a.ctx == nil {
		return
	}
	runtime.Show(a.ctx)
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
}

func (a *App) startCPAFromTray() {
	if _, err := a.StartLauncherService(); err != nil {
		a.logTrayWarningf("从托盘启动 CPA 失败: %v", err)
	}
}

func (a *App) stopCPAFromTray() {
	if _, err := a.StopLauncherService(); err != nil {
		a.logTrayWarningf("从托盘停止 CPA 失败: %v", err)
	}
}

func (a *App) openManagementFromTray() {
	if err := a.OpenLauncherManagementPage(); err != nil {
		a.logTrayWarningf("从托盘打开管理页失败: %v", err)
	}
}

func (a *App) quitLauncherFromTray() {
	if _, err := a.StopLauncherService(); err != nil {
		a.logTrayWarningf("退出启动器前停止 CPA 失败: %v", err)
	}
	a.quitAppFromTray()
}

func (a *App) quitAppFromTray() {
	if a.ctx == nil {
		return
	}
	a.allowClose.Store(true)
	runtime.Quit(a.ctx)
}
