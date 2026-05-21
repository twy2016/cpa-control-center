package backend

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type launcherRoundTripFunc func(*http.Request) (*http.Response, error)

func (f launcherRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testCPAManagerNativeAssetName(version string) string {
	extension := ".tar.gz"
	if currentGOOS() == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf("cpa-manager_%s_%s_%s%s", version, currentGOOS(), currentGOARCH(), extension)
}

func testCPAManagerNativeArchive(t *testing.T) []byte {
	t.Helper()

	content := []byte("fake cpa-manager")
	executableName := cpaManagerExecutableName()
	if currentGOOS() == "windows" {
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		file, err := writer.Create("cpa-manager_v9.9.9_windows_amd64/" + executableName)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := file.Write(content); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close zip: %v", err)
		}
		return buffer.Bytes()
	}

	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	header := &tar.Header{
		Name: "cpa-manager_v9.9.9_" + currentGOOS() + "_" + currentGOARCH() + "/" + executableName,
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buffer.Bytes()
}

func TestCheckCPAManagerForUpdateUsesCPAManagerRelease(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Setenv("REQUEST_METHOD", "")

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	service := newLauncherService(store, nil, nil)
	if err := os.MkdirAll(filepath.Dir(service.cpaManagerExecutablePath()), 0o755); err != nil {
		t.Fatalf("mkdir cpa-manager bin: %v", err)
	}
	if err := os.WriteFile(service.cpaManagerExecutablePath(), []byte("exe"), 0o755); err != nil {
		t.Fatalf("write cpa-manager binary: %v", err)
	}
	service.downloadClient = &http.Client{
		Transport: launcherRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Host != "api.github.com" || request.URL.Path != "/repos/seakee/CPA-Manager/releases/latest" {
				t.Fatalf("unexpected release request URL: %s", request.URL.String())
			}
			body := fmt.Sprintf(`{"tag_name":"v9.9.9","html_url":"https://github.com/seakee/CPA-Manager/releases/tag/v9.9.9","assets":[{"name":%q,"browser_download_url":"https://downloads.example.test/cpa-manager","size":123}]}`, testCPAManagerNativeAssetName("v9.9.9"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    request,
			}, nil
		}),
	}

	update, err := service.checkCPAManagerForUpdate(defaultSettings(t.TempDir()), true, launcherUpdateSourceManual)
	if err != nil {
		t.Fatalf("checkCPAManagerForUpdate: %v", err)
	}
	if !update.Available {
		t.Fatal("expected CPA-Manager update to be available")
	}
	if update.CurrentVersion != embeddedCPAManagerVersion {
		t.Fatalf("expected current version %q, got %q", embeddedCPAManagerVersion, update.CurrentVersion)
	}
	if update.TagName != "v9.9.9" {
		t.Fatalf("expected latest tag v9.9.9, got %q", update.TagName)
	}
	if update.ReleaseURL == "" {
		t.Fatal("expected CPA-Manager release URL to be recorded")
	}
}

func TestCheckCPAManagerForUpdateUsesEmbeddedVersionBaseline(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Setenv("REQUEST_METHOD", "")

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	service := newLauncherService(store, nil, nil)
	if err := os.MkdirAll(filepath.Dir(service.cpaManagerExecutablePath()), 0o755); err != nil {
		t.Fatalf("mkdir cpa-manager bin: %v", err)
	}
	if err := os.WriteFile(service.cpaManagerExecutablePath(), []byte("exe"), 0o755); err != nil {
		t.Fatalf("write cpa-manager binary: %v", err)
	}
	service.downloadClient = &http.Client{
		Transport: launcherRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			body := fmt.Sprintf(`{"tag_name":%q,"html_url":"https://github.com/seakee/CPA-Manager/releases/tag/%s","assets":[{"name":%q,"browser_download_url":"https://downloads.example.test/cpa-manager","size":123}]}`, embeddedCPAManagerVersion, embeddedCPAManagerVersion, testCPAManagerNativeAssetName(embeddedCPAManagerVersion))
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    request,
			}, nil
		}),
	}

	update, err := service.checkCPAManagerForUpdate(defaultSettings(t.TempDir()), true, launcherUpdateSourceManual)
	if err != nil {
		t.Fatalf("checkCPAManagerForUpdate: %v", err)
	}
	if update.Available {
		t.Fatalf("expected CPA-Manager to be treated as current, got update=%+v", update)
	}
	if update.CurrentVersion != embeddedCPAManagerVersion {
		t.Fatalf("expected current version %q, got %q", embeddedCPAManagerVersion, update.CurrentVersion)
	}
}

func TestUpdateCPAManagerDownloadsManagementPanelAsset(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Setenv("REQUEST_METHOD", "")

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	installDir := t.TempDir()
	executablePath := filepath.Join(installDir, "cli-proxy-api.exe")
	if err := os.WriteFile(executablePath, []byte("exe"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	configPath := filepath.Join(installDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(strings.Join([]string{
		"remote-management:",
		"  disable-control-panel: false",
		"  panel-github-repository: \"https://github.com/router-for-me/Cli-Proxy-API-Management-Center\"",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	settings := defaultSettings(t.TempDir())
	settings.Launcher.ExecutablePath = executablePath
	settings.Launcher.ConfigPath = configPath
	settings.Launcher.CPAManagerLastInstalledVersion = "v1.0.0"
	if _, err := store.SaveSettings(settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	service := newLauncherService(store, nil, nil)
	nativeAssetName := testCPAManagerNativeAssetName("v9.9.9")
	nativeArchive := testCPAManagerNativeArchive(t)
	service.downloadClient = &http.Client{
		Transport: launcherRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch {
			case request.URL.Host == "api.github.com" && request.URL.Path == "/repos/seakee/CPA-Manager/releases/latest":
				body := fmt.Sprintf(`{"tag_name":"v9.9.9","html_url":"https://github.com/seakee/CPA-Manager/releases/tag/v9.9.9","assets":[{"name":%q,"browser_download_url":"https://downloads.example.test/cpa-manager-native","size":%d},{"name":"management.html","browser_download_url":"https://downloads.example.test/management.html","size":45}]}`, nativeAssetName, len(nativeArchive))
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    request,
				}, nil
			case request.URL.Host == "downloads.example.test" && request.URL.Path == "/cpa-manager-native":
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(bytes.NewReader(nativeArchive)),
					Request:    request,
				}, nil
			case request.URL.Host == "downloads.example.test" && request.URL.Path == "/management.html":
				body := "<!doctype html><html>CPA-Manager externalUsageService</html>"
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    request,
				}, nil
			default:
				t.Fatalf("unexpected request URL: %s", request.URL.String())
				return nil, nil
			}
		}),
	}

	snapshot, err := service.UpdateCPAManager()
	if err != nil {
		t.Fatalf("UpdateCPAManager: %v", err)
	}
	if snapshot.CPAManagerUpdate.Available {
		t.Fatalf("expected CPA-Manager update to be consumed, got %+v", snapshot.CPAManagerUpdate)
	}
	if snapshot.CPAManagerUpdate.CurrentVersion != "v9.9.9" {
		t.Fatalf("expected current CPA-Manager version v9.9.9, got %q", snapshot.CPAManagerUpdate.CurrentVersion)
	}

	staticPanelPath := filepath.Join(installDir, "static", "management.html")
	if !panelSupportsUsageService(staticPanelPath) {
		t.Fatal("expected downloaded panel to be installed to static cache")
	}
	if _, err := os.Stat(service.cpaManagerExecutablePath()); err != nil {
		t.Fatalf("expected CPA-Manager binary to be installed: %v", err)
	}
	updatedSettings, err := store.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if updatedSettings.Launcher.CPAManagerLastInstalledVersion != "v9.9.9" {
		t.Fatalf("expected stored CPA-Manager version v9.9.9, got %q", updatedSettings.Launcher.CPAManagerLastInstalledVersion)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile config: %v", err)
	}
	if !strings.Contains(string(configData), `panel-github-repository: "https://github.com/seakee/CPA-Manager"`) {
		t.Fatalf("expected CPA-Manager panel repository in config, got:\n%s", string(configData))
	}
}

func TestResolveLauncherManagementKeyPrefersPlainSavedTokenOverHashedConfig(t *testing.T) {
	t.Parallel()

	key, source, skippedHashedKey := resolveLauncherManagementKey(
		AppSettings{ManagementToken: "plain-management-key"},
		LauncherRuntimeInfo{ManagementSecretKey: "$2a$10$ygh/EsdciY5FHKXbS1b3COL.DlnJExjRbfjqFbozjBXCmRwrQOGC."},
	)

	if key != "plain-management-key" {
		t.Fatalf("expected plain saved token, got %q", key)
	}
	if source != "settings" {
		t.Fatalf("expected settings source, got %q", source)
	}
	if skippedHashedKey {
		t.Fatal("did not expect saved plaintext token to be treated as skipped hash")
	}
}

func TestResolveLauncherManagementKeyRejectsHashedOnlySecret(t *testing.T) {
	t.Parallel()

	key, source, skippedHashedKey := resolveLauncherManagementKey(
		AppSettings{},
		LauncherRuntimeInfo{ManagementSecretKey: "$2a$10$ygh/EsdciY5FHKXbS1b3COL.DlnJExjRbfjqFbozjBXCmRwrQOGC."},
	)

	if key != "" || source != "" {
		t.Fatalf("expected hashed config secret to be ignored, got key=%q source=%q", key, source)
	}
	if !skippedHashedKey {
		t.Fatal("expected hashed config secret to be reported")
	}
}

func TestReplaceCPAManagerPanelRepositoryUpdatesExistingValue(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		"remote-management:",
		"  allow-remote: false",
		"  disable-control-panel: false",
		"  panel-github-repository: \"https://github.com/router-for-me/Cli-Proxy-API-Management-Center\"",
		"auth-dir: \"C:\\\\Users\\\\gp\\\\.cli-proxy-api\"",
		"",
	}, "\n")

	output, changed := replaceCPAManagerPanelRepository(input)
	if !changed {
		t.Fatal("expected panel repository to be updated")
	}
	if !strings.Contains(output, `panel-github-repository: "https://github.com/seakee/CPA-Manager"`) {
		t.Fatalf("expected CPA-Manager panel repository, got:\n%s", output)
	}
	if !strings.Contains(output, `auth-dir: "C:\\Users\\gp\\.cli-proxy-api"`) {
		t.Fatalf("expected following top-level config to be preserved, got:\n%s", output)
	}
}

func TestReplaceCPAManagerPanelRepositoryInsertsMissingValue(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		"remote-management:",
		"  allow-remote: false",
		"  secret-key: \"plain-key\"",
		"  disable-control-panel: false",
		"auth-dir: \"C:\\\\Users\\\\gp\\\\.cli-proxy-api\"",
		"",
	}, "\n")

	output, changed := replaceCPAManagerPanelRepository(input)
	if !changed {
		t.Fatal("expected panel repository to be inserted")
	}
	if !strings.Contains(output, "  "+cpamanagerPanelRepositoryYAML()+"\n"+"auth-dir:") {
		t.Fatalf("expected panel repository to be inserted before auth-dir, got:\n%s", output)
	}
}

func TestEnsureCPAManagerPanelAssetCopiesLocalPanelToStaticCache(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "management.html"), []byte("<html>externalUsageService</html>"), 0o644); err != nil {
		t.Fatalf("write local panel: %v", err)
	}
	staticDir := filepath.Join(dir, "static")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		t.Fatalf("mkdir static: %v", err)
	}
	staticPanel := filepath.Join(staticDir, "management.html")
	if err := os.WriteFile(staticPanel, []byte("<html>legacy panel</html>"), 0o644); err != nil {
		t.Fatalf("write static panel: %v", err)
	}

	action, err := ensureCPAManagerPanelAsset(dir)
	if err != nil {
		t.Fatalf("ensureCPAManagerPanelAsset: %v", err)
	}
	if action != "copied" {
		t.Fatalf("expected copied action, got %q", action)
	}
	if !panelSupportsUsageService(staticPanel) {
		t.Fatal("expected static panel to support Usage Service after copy")
	}
}
