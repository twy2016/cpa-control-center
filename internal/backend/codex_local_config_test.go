package backend

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateCodexLocalConfigContent(t *testing.T) {
	t.Parallel()

	valid := validateCodexLocalConfigContent("OpenAI", "model = 'gpt-5'\n", "{\"api_key\":\"demo\"}\n")
	if !valid.OK || !valid.ConfigTomlValid || !valid.AuthJSONValid {
		t.Fatalf("expected valid config result, got %+v", valid)
	}

	invalidTOML := validateCodexLocalConfigContent("Broken", "model = [\n", "{\"api_key\":\"demo\"}\n")
	if invalidTOML.OK || invalidTOML.ConfigTomlValid || !invalidTOML.AuthJSONValid {
		t.Fatalf("expected invalid TOML result, got %+v", invalidTOML)
	}

	invalidJSON := validateCodexLocalConfigContent("Broken", "model = 'gpt-5'\n", "{\"api_key\":")
	if invalidJSON.OK || !invalidJSON.ConfigTomlValid || invalidJSON.AuthJSONValid {
		t.Fatalf("expected invalid JSON result, got %+v", invalidJSON)
	}
}

func TestCodexLocalConfigTestSavedProfileConnection(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			http.Error(w, `{"error":{"message":"unauthorized"}}`, http.StatusUnauthorized)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, `{"error":{"message":"bad json"}}`, http.StatusBadRequest)
			return
		}
		if payload["model"] != "gpt-5.4" {
			http.Error(w, `{"error":{"message":"wrong model"}}`, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "resp_test",
		})
	}))
	defer server.Close()

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	manager := newCodexLocalConfigManager(store)
	manager.defaultDirectory = filepath.Join(t.TempDir(), ".codex")

	if _, err := manager.SaveProfileContent(CodexLocalConfigSaveInput{
		Name:       "Reachable",
		ConfigToml: "model_provider = \"cliproxyapi\"\nmodel = \"gpt-5.4\"\n\n[model_providers.cliproxyapi]\nname = \"cliproxyapi\"\nbase_url = \"" + server.URL + "/v1\"\nwire_api = \"responses\"\n",
		AuthJSON:   "{\"OPENAI_API_KEY\":\"test-key\"}\n",
	}); err != nil {
		t.Fatalf("SaveProfileContent Reachable: %v", err)
	}

	result, err := manager.TestSavedProfileConnection("Reachable")
	if err != nil {
		t.Fatalf("TestSavedProfileConnection: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected successful connection result, got %+v", result)
	}
	if result.StatusCode == nil || *result.StatusCode != http.StatusOK {
		t.Fatalf("unexpected connection status code: %+v", result)
	}
	if result.ProviderName != "cliproxyapi" || result.Model != "gpt-5.4" {
		t.Fatalf("unexpected connection metadata: %+v", result)
	}
}

func TestCodexLocalConfigImportSwitchAndDelete(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	manager := newCodexLocalConfigManager(store)
	manager.defaultDirectory = filepath.Join(t.TempDir(), ".codex")
	if err := ensureDir(manager.defaultDirectory); err != nil {
		t.Fatalf("ensureDir: %v", err)
	}

	openAIConfig := []byte("model = 'gpt-5'\n")
	openAIAuth := []byte("{\"api_key\":\"openai-key\"}\n")
	if err := os.WriteFile(filepath.Join(manager.defaultDirectory, codexConfigTomlFileName), openAIConfig, 0o600); err != nil {
		t.Fatalf("WriteFile config.toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(manager.defaultDirectory, codexAuthJSONFileName), openAIAuth, 0o600); err != nil {
		t.Fatalf("WriteFile auth.json: %v", err)
	}

	snapshot, err := manager.ImportCurrent(CodexLocalConfigImportInput{Name: "OpenAI"})
	if err != nil {
		t.Fatalf("ImportCurrent OpenAI: %v", err)
	}
	if len(snapshot.Profiles) != 1 || snapshot.Profiles[0].Name != "OpenAI" {
		t.Fatalf("unexpected profiles after first import: %+v", snapshot.Profiles)
	}

	openRouterConfig := []byte("model = 'openrouter/gpt-5'\n")
	openRouterAuth := []byte("{\"api_key\":\"openrouter-key\"}\n")
	if err := os.WriteFile(filepath.Join(manager.defaultDirectory, codexConfigTomlFileName), openRouterConfig, 0o600); err != nil {
		t.Fatalf("WriteFile updated config.toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(manager.defaultDirectory, codexAuthJSONFileName), openRouterAuth, 0o600); err != nil {
		t.Fatalf("WriteFile updated auth.json: %v", err)
	}

	snapshot, err = manager.ImportCurrent(CodexLocalConfigImportInput{Name: "OpenRouter"})
	if err != nil {
		t.Fatalf("ImportCurrent OpenRouter: %v", err)
	}
	if len(snapshot.Profiles) != 2 {
		t.Fatalf("expected two profiles, got %+v", snapshot.Profiles)
	}

	snapshot, err = manager.Switch(CodexLocalConfigSwitchInput{Name: "OpenAI"})
	if err != nil {
		t.Fatalf("Switch OpenAI: %v", err)
	}
	if snapshot.ActiveProfileName != "OpenAI" {
		t.Fatalf("expected active profile OpenAI, got %+v", snapshot)
	}
	if len(snapshot.Backups) != 1 {
		t.Fatalf("expected one backup after switch, got %+v", snapshot.Backups)
	}

	configBytes, err := os.ReadFile(filepath.Join(manager.defaultDirectory, codexConfigTomlFileName))
	if err != nil {
		t.Fatalf("ReadFile config.toml: %v", err)
	}
	if string(configBytes) != string(openAIConfig) {
		t.Fatalf("expected switched config.toml to match OpenAI profile, got %q", string(configBytes))
	}
	authBytes, err := os.ReadFile(filepath.Join(manager.defaultDirectory, codexAuthJSONFileName))
	if err != nil {
		t.Fatalf("ReadFile auth.json: %v", err)
	}
	if string(authBytes) != string(openAIAuth) {
		t.Fatalf("expected switched auth.json to match OpenAI profile, got %q", string(authBytes))
	}

	backupDir := filepath.Join(manager.backupsDir(), snapshot.Backups[0].Name)
	backupConfig, err := os.ReadFile(filepath.Join(backupDir, codexConfigTomlFileName))
	if err != nil {
		t.Fatalf("ReadFile backup config.toml: %v", err)
	}
	if string(backupConfig) != string(openRouterConfig) {
		t.Fatalf("expected backup config.toml to match pre-switch content, got %q", string(backupConfig))
	}
	backupAuth, err := os.ReadFile(filepath.Join(backupDir, codexAuthJSONFileName))
	if err != nil {
		t.Fatalf("ReadFile backup auth.json: %v", err)
	}
	if string(backupAuth) != string(openRouterAuth) {
		t.Fatalf("expected backup auth.json to match pre-switch content, got %q", string(backupAuth))
	}

	if _, err := manager.Delete("OpenAI"); err == nil {
		t.Fatal("expected deleting active profile to fail")
	}

	snapshot, err = manager.Delete("OpenRouter")
	if err != nil {
		t.Fatalf("Delete OpenRouter: %v", err)
	}
	if len(snapshot.Profiles) != 1 || snapshot.Profiles[0].Name != "OpenAI" {
		t.Fatalf("unexpected profiles after delete: %+v", snapshot.Profiles)
	}
}

func TestCodexLocalConfigImportRejectsMissingFilesAndDuplicates(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	manager := newCodexLocalConfigManager(store)
	manager.defaultDirectory = filepath.Join(t.TempDir(), ".codex")
	if err := ensureDir(manager.defaultDirectory); err != nil {
		t.Fatalf("ensureDir: %v", err)
	}

	if _, err := manager.ImportCurrent(CodexLocalConfigImportInput{Name: "Missing"}); err == nil {
		t.Fatal("expected import without current files to fail")
	}

	if err := os.WriteFile(filepath.Join(manager.defaultDirectory, codexConfigTomlFileName), []byte("model = 'gpt-5'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config.toml: %v", err)
	}
	if _, err := manager.ImportCurrent(CodexLocalConfigImportInput{Name: "MissingAuth"}); err == nil {
		t.Fatal("expected import without auth.json to fail")
	}

	if err := os.WriteFile(filepath.Join(manager.defaultDirectory, codexAuthJSONFileName), []byte("{\"api_key\":\"demo\"}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile auth.json: %v", err)
	}
	if _, err := manager.ImportCurrent(CodexLocalConfigImportInput{Name: "Demo"}); err != nil {
		t.Fatalf("ImportCurrent Demo: %v", err)
	}
	if _, err := manager.ImportCurrent(CodexLocalConfigImportInput{Name: "demo"}); err == nil {
		t.Fatal("expected duplicate supplier names to be rejected case-insensitively")
	}
}

func TestCodexLocalConfigSaveProfileContentSyncsActiveProfile(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	manager := newCodexLocalConfigManager(store)
	manager.defaultDirectory = filepath.Join(t.TempDir(), ".codex")
	if err := ensureDir(manager.defaultDirectory); err != nil {
		t.Fatalf("ensureDir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(manager.defaultDirectory, codexConfigTomlFileName), []byte("model = 'openai'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config.toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(manager.defaultDirectory, codexAuthJSONFileName), []byte("{\"api_key\":\"openai\"}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile auth.json: %v", err)
	}
	if _, err := manager.ImportCurrent(CodexLocalConfigImportInput{Name: "OpenAI"}); err != nil {
		t.Fatalf("ImportCurrent OpenAI: %v", err)
	}

	if err := os.WriteFile(filepath.Join(manager.defaultDirectory, codexConfigTomlFileName), []byte("model = 'openrouter'\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config.toml router: %v", err)
	}
	if err := os.WriteFile(filepath.Join(manager.defaultDirectory, codexAuthJSONFileName), []byte("{\"api_key\":\"router\"}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile auth.json router: %v", err)
	}
	if _, err := manager.ImportCurrent(CodexLocalConfigImportInput{Name: "OpenRouter"}); err != nil {
		t.Fatalf("ImportCurrent OpenRouter: %v", err)
	}
	if _, err := manager.Switch(CodexLocalConfigSwitchInput{Name: "OpenAI"}); err != nil {
		t.Fatalf("Switch OpenAI: %v", err)
	}

	saved, err := manager.SaveProfileContent(CodexLocalConfigSaveInput{
		Name:       "OpenAI",
		ConfigToml: "model = 'openai-edited'\n",
		AuthJSON:   "{\"api_key\":\"openai-edited\"}\n",
	})
	if err != nil {
		t.Fatalf("SaveProfileContent OpenAI: %v", err)
	}
	if saved.Name != "OpenAI" || saved.UpdatedAt == "" {
		t.Fatalf("unexpected saved content metadata: %+v", saved)
	}

	currentConfig, err := os.ReadFile(filepath.Join(manager.defaultDirectory, codexConfigTomlFileName))
	if err != nil {
		t.Fatalf("ReadFile current config.toml: %v", err)
	}
	if string(currentConfig) != "model = 'openai-edited'\n" {
		t.Fatalf("expected active save to sync current config.toml, got %q", string(currentConfig))
	}
	currentAuth, err := os.ReadFile(filepath.Join(manager.defaultDirectory, codexAuthJSONFileName))
	if err != nil {
		t.Fatalf("ReadFile current auth.json: %v", err)
	}
	if string(currentAuth) != "{\"api_key\":\"openai-edited\"}\n" {
		t.Fatalf("expected active save to sync current auth.json, got %q", string(currentAuth))
	}

	routerContent, err := manager.ProfileContent("OpenRouter")
	if err != nil {
		t.Fatalf("ProfileContent OpenRouter: %v", err)
	}
	routerBeforeConfig := routerContent.ConfigToml
	routerBeforeAuth := routerContent.AuthJSON

	if _, err := manager.SaveProfileContent(CodexLocalConfigSaveInput{
		Name:       "OpenRouter",
		ConfigToml: "model = 'openrouter-edited'\n",
		AuthJSON:   "{\"api_key\":\"openrouter-edited\"}\n",
	}); err != nil {
		t.Fatalf("SaveProfileContent OpenRouter: %v", err)
	}

	currentConfig, err = os.ReadFile(filepath.Join(manager.defaultDirectory, codexConfigTomlFileName))
	if err != nil {
		t.Fatalf("ReadFile current config.toml after inactive save: %v", err)
	}
	if string(currentConfig) != "model = 'openai-edited'\n" {
		t.Fatalf("inactive save should not change current config.toml, got %q", string(currentConfig))
	}
	currentAuth, err = os.ReadFile(filepath.Join(manager.defaultDirectory, codexAuthJSONFileName))
	if err != nil {
		t.Fatalf("ReadFile current auth.json after inactive save: %v", err)
	}
	if string(currentAuth) != "{\"api_key\":\"openai-edited\"}\n" {
		t.Fatalf("inactive save should not change current auth.json, got %q", string(currentAuth))
	}

	routerContent, err = manager.ProfileContent("OpenRouter")
	if err != nil {
		t.Fatalf("ProfileContent OpenRouter after save: %v", err)
	}
	if routerContent.ConfigToml == routerBeforeConfig || routerContent.AuthJSON == routerBeforeAuth {
		t.Fatalf("expected inactive profile content to update, got %+v", routerContent)
	}

	created, err := manager.SaveProfileContent(CodexLocalConfigSaveInput{
		Name:       "Azure",
		ConfigToml: "model = 'azure/gpt-5'\n",
		AuthJSON:   "{\"api_key\":\"azure\"}\n",
	})
	if err != nil {
		t.Fatalf("SaveProfileContent Azure: %v", err)
	}
	if created.Name != "Azure" || created.UpdatedAt == "" {
		t.Fatalf("unexpected created profile content: %+v", created)
	}

	snapshotAfterCreate, err := manager.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot after create: %v", err)
	}
	if len(snapshotAfterCreate.Profiles) != 3 {
		t.Fatalf("expected three profiles after editor-based create, got %+v", snapshotAfterCreate.Profiles)
	}

	currentConfig, err = os.ReadFile(filepath.Join(manager.defaultDirectory, codexConfigTomlFileName))
	if err != nil {
		t.Fatalf("ReadFile current config.toml after create: %v", err)
	}
	if string(currentConfig) != "model = 'openai-edited'\n" {
		t.Fatalf("creating an inactive profile should not change current config.toml, got %q", string(currentConfig))
	}
}
