package backend

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestLauncherProxyURLFromConfigReadsProxyURL(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("proxy-url: \"http://127.0.0.1:7890\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	proxyURL, err := launcherProxyURLFromSettings(LauncherSettings{ConfigPath: configPath})
	if err != nil {
		t.Fatalf("launcherProxyURLFromSettings: %v", err)
	}
	if proxyURL != "http://127.0.0.1:7890" {
		t.Fatalf("expected proxy URL to be read from config, got %q", proxyURL)
	}
}

func TestLauncherProxyURLFromSettingsFallsBackToExecutableDirectoryConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executablePath := filepath.Join(root, "cli-proxy-api.exe")
	configPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(executablePath, []byte("binary"), 0o644); err != nil {
		t.Fatalf("WriteFile executable: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("proxy-url: \"http://127.0.0.1:7890\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}

	proxyURL, err := launcherProxyURLFromSettings(LauncherSettings{
		ConfigPath:     filepath.Join(".", "missing-config.yaml"),
		ExecutablePath: executablePath,
	})
	if err != nil {
		t.Fatalf("launcherProxyURLFromSettings fallback: %v", err)
	}
	if proxyURL != "http://127.0.0.1:7890" {
		t.Fatalf("expected fallback proxy URL from executable directory config, got %q", proxyURL)
	}
}

func TestLauncherDownloadClientForProxyRejectsInvalidURL(t *testing.T) {
	t.Parallel()

	service := newLauncherService(nil, nil, nil)
	if _, _, err := service.downloadClientForProxy("127.0.0.1:7890", "https://api.github.com/repos/router-for-me/CLIProxyAPI/releases/latest"); err == nil {
		t.Fatal("expected invalid proxy URL to be rejected")
	}
}

func TestLauncherDownloadClientForProxyUsesEnvironmentProxyWhenConfigEmpty(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:7890")
	t.Setenv("https_proxy", "http://127.0.0.1:7890")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Setenv("REQUEST_METHOD", "")

	service := newLauncherService(nil, nil, nil)
	client, label, err := service.downloadClientForProxy("", "https://api.github.com/repos/router-for-me/CLIProxyAPI/releases/latest")
	if err != nil {
		t.Fatalf("downloadClientForProxy: %v", err)
	}
	if client == nil {
		t.Fatal("expected client to be returned")
	}
	if label == "" {
		t.Fatal("expected environment proxy label to be returned")
	}

	request, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/router-for-me/CLIProxyAPI/releases/latest", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == nil || transport.Proxy == nil {
		t.Fatalf("expected client transport to carry proxy function, got %#v", client.Transport)
	}
	resolved, err := transport.Proxy(request)
	if err != nil {
		t.Fatalf("transport.Proxy: %v", err)
	}
	if resolved == nil || resolved.String() != "http://127.0.0.1:7890" {
		t.Fatalf("expected environment proxy to be applied, got %v", resolved)
	}
}
