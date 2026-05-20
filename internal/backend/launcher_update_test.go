package backend

import (
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
	service.downloadClient = &http.Client{
		Transport: launcherRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Host != "api.github.com" || request.URL.Path != "/repos/seakee/CPA-Manager/releases/latest" {
				t.Fatalf("unexpected release request URL: %s", request.URL.String())
			}
			body := `{"tag_name":"v9.9.9","html_url":"https://github.com/seakee/CPA-Manager/releases/tag/v9.9.9"}`
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
	service.downloadClient = &http.Client{
		Transport: launcherRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			body := `{"tag_name":"` + embeddedCPAManagerVersion + `","html_url":"https://github.com/seakee/CPA-Manager/releases/tag/` + embeddedCPAManagerVersion + `"}`
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
