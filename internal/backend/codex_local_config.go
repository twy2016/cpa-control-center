package backend

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	codexLocalConfigRootDirName      = "codex-local"
	codexLocalConfigProfilesDirName  = "profiles"
	codexLocalConfigBackupsDirName   = "backups"
	codexLocalConfigIndexFileName    = "index.json"
	codexConfigTomlFileName          = "config.toml"
	codexAuthJSONFileName            = "auth.json"
	codexLocalConfigTransferKind     = "codex-local-profile"
	codexLocalConfigTransferListKind = "codex-local-profiles"
	codexLocalConfigTransferVersion  = 1
)

var codexLocalConfigSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)
var codexLocalConfigTableHeaderPattern = regexp.MustCompile(`^\s*\[\s*([^\[\]]+?)\s*\]\s*(?:#.*)?$`)
var codexLocalConfigRootKeyPattern = regexp.MustCompile(`^([A-Za-z0-9_-]+)\s*=`)

type codexLocalConfigIndex struct {
	ActiveProfileName string                         `json:"activeProfileName"`
	Profiles          []codexLocalConfigIndexProfile `json:"profiles"`
}

type codexLocalConfigIndexProfile struct {
	Name            string `json:"name"`
	DirName         string `json:"dirName"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
	LastActivatedAt string `json:"lastActivatedAt"`
}

type codexLocalConfigManager struct {
	store            *Store
	defaultDirectory string
}

type codexLocalParsedConfig struct {
	Model          string                              `toml:"model"`
	ModelProvider  string                              `toml:"model_provider"`
	ModelProviders map[string]codexLocalParsedProvider `toml:"model_providers"`
}

type codexLocalParsedProvider struct {
	Name    string `toml:"name"`
	BaseURL string `toml:"base_url"`
	WireAPI string `toml:"wire_api"`
}

type codexLocalConfigTransferFile struct {
	Kind       string                            `json:"kind"`
	Version    int                               `json:"version"`
	Name       string                            `json:"name"`
	ConfigToml string                            `json:"configToml"`
	AuthJSON   string                            `json:"authJson"`
	Profiles   []codexLocalConfigTransferProfile `json:"profiles"`
	ExportedAt string                            `json:"exportedAt"`
}

type codexLocalConfigTransferProfile struct {
	Name       string `json:"name"`
	ConfigToml string `json:"configToml"`
	AuthJSON   string `json:"authJson"`
}

type codexLocalConfigDocument struct {
	newline         string
	trailingNewline bool
	preamble        []string
	blocks          []codexLocalConfigTableBlock
}

type codexLocalConfigTableBlock struct {
	path  []string
	lines []string
}

type codexLocalConfigMCPOverlay struct {
	topLevelKeys  map[string]string
	topLevelOrder []string
	blocks        []codexLocalConfigTableBlock
	targetKeys    map[string]struct{}
}

func newCodexLocalConfigManager(store *Store) *codexLocalConfigManager {
	return &codexLocalConfigManager{
		store:            store,
		defaultDirectory: filepath.Join(userHomeDirectory(), ".codex"),
	}
}

func (m *codexLocalConfigManager) Snapshot() (CodexLocalConfigSnapshot, error) {
	index, err := m.loadIndex()
	if err != nil {
		return CodexLocalConfigSnapshot{}, err
	}

	profiles := make([]CodexLocalConfigProfile, 0, len(index.Profiles))
	for _, profile := range index.Profiles {
		profileDir, err := m.profileDir(profile.DirName)
		if err != nil {
			return CodexLocalConfigSnapshot{}, err
		}
		profiles = append(profiles, CodexLocalConfigProfile{
			Name:            profile.Name,
			CreatedAt:       profile.CreatedAt,
			UpdatedAt:       profile.UpdatedAt,
			LastActivatedAt: profile.LastActivatedAt,
			HasConfigToml:   fileExists(filepath.Join(profileDir, codexConfigTomlFileName)),
			HasAuthJSON:     fileExists(filepath.Join(profileDir, codexAuthJSONFileName)),
		})
	}
	sort.Slice(profiles, func(i, j int) bool {
		return strings.ToLower(profiles[i].Name) < strings.ToLower(profiles[j].Name)
	})

	backups, err := m.listBackups()
	if err != nil {
		return CodexLocalConfigSnapshot{}, err
	}

	return CodexLocalConfigSnapshot{
		Profiles:            profiles,
		ActiveProfileName:   index.ActiveProfileName,
		DefaultDirectory:    m.defaultDirectory,
		ConfigPath:          m.defaultConfigPath(),
		AuthPath:            m.defaultAuthPath(),
		CurrentConfigExists: fileExists(m.defaultConfigPath()),
		CurrentAuthExists:   fileExists(m.defaultAuthPath()),
		Backups:             backups,
	}, nil
}

func (m *codexLocalConfigManager) ImportCurrent(input CodexLocalConfigImportInput) (CodexLocalConfigSnapshot, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return CodexLocalConfigSnapshot{}, errors.New("供应商名称不能为空")
	}

	index, err := m.loadIndex()
	if err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	if _, existing := findCodexLocalConfigProfile(index.Profiles, name); existing != nil {
		return CodexLocalConfigSnapshot{}, fmt.Errorf("供应商配置 %q 已存在", name)
	}

	configBytes, err := os.ReadFile(m.defaultConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CodexLocalConfigSnapshot{}, fmt.Errorf("当前 Codex 配置缺少 %s", codexConfigTomlFileName)
		}
		return CodexLocalConfigSnapshot{}, err
	}
	authBytes, err := os.ReadFile(m.defaultAuthPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CodexLocalConfigSnapshot{}, fmt.Errorf("当前 Codex 配置缺少 %s", codexAuthJSONFileName)
		}
		return CodexLocalConfigSnapshot{}, err
	}

	if err := m.ensureStorageLayout(); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}

	dirName := codexLocalConfigDirectoryName(name)
	profileDir, err := m.profileDir(dirName)
	if err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	if pathExists(profileDir) {
		return CodexLocalConfigSnapshot{}, fmt.Errorf("供应商配置目录已存在：%s", profileDir)
	}
	if err := ensureDir(profileDir); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	if err := os.WriteFile(filepath.Join(profileDir, codexConfigTomlFileName), configBytes, 0o600); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	if err := os.WriteFile(filepath.Join(profileDir, codexAuthJSONFileName), authBytes, 0o600); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}

	now := nowISO()
	index.Profiles = append(index.Profiles, codexLocalConfigIndexProfile{
		Name:      name,
		DirName:   dirName,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if _, err := m.syncMCPConfigAcrossProfiles(&index, string(configBytes)); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	if err := m.saveIndex(index); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	return m.Snapshot()
}

func (m *codexLocalConfigManager) ProfileContent(name string) (CodexLocalConfigProfileContent, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return CodexLocalConfigProfileContent{}, errors.New("供应商名称不能为空")
	}

	index, err := m.loadIndex()
	if err != nil {
		return CodexLocalConfigProfileContent{}, err
	}
	_, profile := findCodexLocalConfigProfile(index.Profiles, name)
	if profile == nil {
		return CodexLocalConfigProfileContent{}, fmt.Errorf("找不到供应商配置 %q", name)
	}

	profileDir, err := m.profileDir(profile.DirName)
	if err != nil {
		return CodexLocalConfigProfileContent{}, err
	}
	configBytes, err := os.ReadFile(filepath.Join(profileDir, codexConfigTomlFileName))
	if err != nil {
		return CodexLocalConfigProfileContent{}, err
	}
	authBytes, err := os.ReadFile(filepath.Join(profileDir, codexAuthJSONFileName))
	if err != nil {
		return CodexLocalConfigProfileContent{}, err
	}

	return CodexLocalConfigProfileContent{
		Name:         profile.Name,
		OriginalName: profile.Name,
		ConfigToml:   string(configBytes),
		AuthJSON:     string(authBytes),
		UpdatedAt:    profile.UpdatedAt,
	}, nil
}

func (m *codexLocalConfigManager) ReloadProfileContent(name string) (CodexLocalConfigProfileContent, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return CodexLocalConfigProfileContent{}, errors.New("供应商名称不能为空")
	}

	index, err := m.loadIndex()
	if err != nil {
		return CodexLocalConfigProfileContent{}, err
	}
	targetIndex, profile := findCodexLocalConfigProfile(index.Profiles, name)
	if profile == nil {
		return CodexLocalConfigProfileContent{}, fmt.Errorf("找不到供应商配置 %q", name)
	}
	if !strings.EqualFold(strings.TrimSpace(index.ActiveProfileName), strings.TrimSpace(profile.Name)) {
		return m.ProfileContent(profile.Name)
	}

	configBytes, err := os.ReadFile(m.defaultConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CodexLocalConfigProfileContent{}, fmt.Errorf("当前 Codex 配置缺少 %s", codexConfigTomlFileName)
		}
		return CodexLocalConfigProfileContent{}, err
	}
	authBytes, err := os.ReadFile(m.defaultAuthPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CodexLocalConfigProfileContent{}, fmt.Errorf("当前 Codex 配置缺少 %s", codexAuthJSONFileName)
		}
		return CodexLocalConfigProfileContent{}, err
	}

	if err := m.ensureStorageLayout(); err != nil {
		return CodexLocalConfigProfileContent{}, err
	}
	profileDir, err := m.profileDir(profile.DirName)
	if err != nil {
		return CodexLocalConfigProfileContent{}, err
	}
	if err := ensureDir(profileDir); err != nil {
		return CodexLocalConfigProfileContent{}, err
	}

	storedConfigPath := filepath.Join(profileDir, codexConfigTomlFileName)
	storedAuthPath := filepath.Join(profileDir, codexAuthJSONFileName)
	storedConfigBytes, storedConfigErr := os.ReadFile(storedConfigPath)
	if storedConfigErr != nil && !errors.Is(storedConfigErr, os.ErrNotExist) {
		return CodexLocalConfigProfileContent{}, storedConfigErr
	}
	storedAuthBytes, storedAuthErr := os.ReadFile(storedAuthPath)
	if storedAuthErr != nil && !errors.Is(storedAuthErr, os.ErrNotExist) {
		return CodexLocalConfigProfileContent{}, storedAuthErr
	}

	mcpSynced, err := m.syncMCPConfigAcrossProfiles(&index, string(configBytes))
	if err != nil {
		return CodexLocalConfigProfileContent{}, err
	}

	profileChanged := storedConfigErr != nil || storedAuthErr != nil ||
		!bytes.Equal(storedConfigBytes, configBytes) || !bytes.Equal(storedAuthBytes, authBytes)
	if profileChanged {
		if err := os.WriteFile(storedConfigPath, configBytes, 0o600); err != nil {
			return CodexLocalConfigProfileContent{}, err
		}
		if err := os.WriteFile(storedAuthPath, authBytes, 0o600); err != nil {
			return CodexLocalConfigProfileContent{}, err
		}
	}

	if profileChanged || mcpSynced {
		if profileChanged {
			index.Profiles[targetIndex].UpdatedAt = nowISO()
		}
		if err := m.saveIndex(index); err != nil {
			return CodexLocalConfigProfileContent{}, err
		}
		profile = &index.Profiles[targetIndex]
	}

	return CodexLocalConfigProfileContent{
		Name:         profile.Name,
		OriginalName: profile.Name,
		ConfigToml:   string(configBytes),
		AuthJSON:     string(authBytes),
		UpdatedAt:    profile.UpdatedAt,
	}, nil
}

func (m *codexLocalConfigManager) TestProfileContent(input CodexLocalConfigSaveInput) CodexLocalConfigValidationResult {
	return validateCodexLocalConfigContent(input.Name, input.ConfigToml, input.AuthJSON)
}

func (m *codexLocalConfigManager) TestSavedProfileConnection(name string) (CodexLocalConfigConnectionTestResult, error) {
	content, err := m.ProfileContent(name)
	if err != nil {
		return CodexLocalConfigConnectionTestResult{}, err
	}
	return testCodexProfileConnection(content)
}

func (m *codexLocalConfigManager) Switch(input CodexLocalConfigSwitchInput) (CodexLocalConfigSnapshot, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return CodexLocalConfigSnapshot{}, errors.New("供应商名称不能为空")
	}

	index, err := m.loadIndex()
	if err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	targetIndex, target := findCodexLocalConfigProfile(index.Profiles, name)
	if target == nil {
		return CodexLocalConfigSnapshot{}, fmt.Errorf("找不到供应商配置 %q", name)
	}
	if currentConfigBytes, err := os.ReadFile(m.defaultConfigPath()); err == nil {
		if _, err := m.syncMCPConfigAcrossProfiles(&index, string(currentConfigBytes)); err != nil {
			return CodexLocalConfigSnapshot{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return CodexLocalConfigSnapshot{}, err
	}

	profileDir, err := m.profileDir(target.DirName)
	if err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	configBytes, err := os.ReadFile(filepath.Join(profileDir, codexConfigTomlFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CodexLocalConfigSnapshot{}, fmt.Errorf("供应商配置 %q 缺少 %s", target.Name, codexConfigTomlFileName)
		}
		return CodexLocalConfigSnapshot{}, err
	}
	authBytes, err := os.ReadFile(filepath.Join(profileDir, codexAuthJSONFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CodexLocalConfigSnapshot{}, fmt.Errorf("供应商配置 %q 缺少 %s", target.Name, codexAuthJSONFileName)
		}
		return CodexLocalConfigSnapshot{}, err
	}

	if err := m.ensureStorageLayout(); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	if err := ensureDir(m.defaultDirectory); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	if err := m.createBackupIfNeeded(); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	if err := os.WriteFile(m.defaultConfigPath(), configBytes, 0o600); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	if err := os.WriteFile(m.defaultAuthPath(), authBytes, 0o600); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}

	now := nowISO()
	index.ActiveProfileName = target.Name
	index.Profiles[targetIndex].LastActivatedAt = now
	if err := m.saveIndex(index); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	return m.Snapshot()
}

func (m *codexLocalConfigManager) SaveProfileContent(input CodexLocalConfigSaveInput) (CodexLocalConfigProfileContent, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return CodexLocalConfigProfileContent{}, errors.New("供应商名称不能为空")
	}
	originalName := strings.TrimSpace(input.OriginalName)
	if originalName == "" {
		originalName = name
	}

	index, err := m.loadIndex()
	if err != nil {
		return CodexLocalConfigProfileContent{}, err
	}
	targetIndex, target := findCodexLocalConfigProfile(index.Profiles, originalName)
	if err := m.ensureStorageLayout(); err != nil {
		return CodexLocalConfigProfileContent{}, err
	}
	wasActive := strings.EqualFold(strings.TrimSpace(index.ActiveProfileName), originalName)

	if existingIndex, existing := findCodexLocalConfigProfile(index.Profiles, name); existing != nil && existingIndex != targetIndex {
		return CodexLocalConfigProfileContent{}, fmt.Errorf("供应商配置 %q 已存在", name)
	}

	if target == nil {
		if originalName != name {
			return CodexLocalConfigProfileContent{}, fmt.Errorf("找不到供应商配置 %q", originalName)
		}
		now := nowISO()
		dirName := codexLocalConfigDirectoryName(name)
		profileDir, err := m.profileDir(dirName)
		if err != nil {
			return CodexLocalConfigProfileContent{}, err
		}
		if pathExists(profileDir) {
			return CodexLocalConfigProfileContent{}, fmt.Errorf("供应商配置目录已存在：%s", profileDir)
		}
		index.Profiles = append(index.Profiles, codexLocalConfigIndexProfile{
			Name:      name,
			DirName:   dirName,
			CreatedAt: now,
			UpdatedAt: now,
		})
		targetIndex = len(index.Profiles) - 1
		target = &index.Profiles[targetIndex]
	}
	index.Profiles[targetIndex].Name = name
	if wasActive {
		index.ActiveProfileName = name
	}

	profileDir, err := m.profileDir(target.DirName)
	if err != nil {
		return CodexLocalConfigProfileContent{}, err
	}
	if err := ensureDir(profileDir); err != nil {
		return CodexLocalConfigProfileContent{}, err
	}
	configBytes := []byte(input.ConfigToml)
	authBytes := []byte(input.AuthJSON)
	if err := os.WriteFile(filepath.Join(profileDir, codexConfigTomlFileName), configBytes, 0o600); err != nil {
		return CodexLocalConfigProfileContent{}, err
	}
	if err := os.WriteFile(filepath.Join(profileDir, codexAuthJSONFileName), authBytes, 0o600); err != nil {
		return CodexLocalConfigProfileContent{}, err
	}

	if wasActive {
		if err := ensureDir(m.defaultDirectory); err != nil {
			return CodexLocalConfigProfileContent{}, err
		}
		if err := m.createBackupIfNeeded(); err != nil {
			return CodexLocalConfigProfileContent{}, err
		}
		if err := os.WriteFile(m.defaultConfigPath(), configBytes, 0o600); err != nil {
			return CodexLocalConfigProfileContent{}, err
		}
		if err := os.WriteFile(m.defaultAuthPath(), authBytes, 0o600); err != nil {
			return CodexLocalConfigProfileContent{}, err
		}
	}
	if _, err := m.syncMCPConfigAcrossProfiles(&index, input.ConfigToml); err != nil {
		return CodexLocalConfigProfileContent{}, err
	}

	now := nowISO()
	index.Profiles[targetIndex].UpdatedAt = now
	if err := m.saveIndex(index); err != nil {
		return CodexLocalConfigProfileContent{}, err
	}
	return CodexLocalConfigProfileContent{
		Name:         name,
		OriginalName: name,
		ConfigToml:   input.ConfigToml,
		AuthJSON:     input.AuthJSON,
		UpdatedAt:    now,
	}, nil
}

func (m *codexLocalConfigManager) ImportProfileFromFile(path string) (string, error) {
	result, err := m.ImportProfilesFromFile(path)
	if err != nil {
		return "", err
	}
	if result.Count != 1 {
		return "", fmt.Errorf("导入文件包含 %d 个供应商配置，请使用整包导入", result.Count)
	}
	return result.Names[0], nil
}

func (m *codexLocalConfigManager) ExportProfileToFile(name string, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("导出路径不能为空")
	}

	content, err := m.ProfileContent(name)
	if err != nil {
		return "", err
	}

	payload := codexLocalConfigTransferFile{
		Kind:       codexLocalConfigTransferKind,
		Version:    codexLocalConfigTransferVersion,
		Name:       content.Name,
		ConfigToml: content.ConfigToml,
		AuthJSON:   content.AuthJSON,
		ExportedAt: nowISO(),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}

	parentDir := filepath.Dir(path)
	if strings.TrimSpace(parentDir) != "" {
		if err := ensureDir(parentDir); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (m *codexLocalConfigManager) ImportProfilesFromFile(path string) (CodexLocalConfigTransferResult, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return CodexLocalConfigTransferResult{}, errors.New("导入路径不能为空")
	}

	payload, err := m.loadTransferFile(path)
	if err != nil {
		return CodexLocalConfigTransferResult{}, err
	}
	profiles, err := codexLocalConfigTransferProfiles(payload)
	if err != nil {
		return CodexLocalConfigTransferResult{}, err
	}

	seen := make(map[string]struct{}, len(profiles))
	importedNames := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		name := strings.TrimSpace(profile.Name)
		if name == "" {
			return CodexLocalConfigTransferResult{}, errors.New("导入文件缺少供应商名称")
		}
		normalizedName := strings.ToLower(name)
		if _, exists := seen[normalizedName]; exists {
			return CodexLocalConfigTransferResult{}, fmt.Errorf("导入文件存在重复供应商名称 %q", name)
		}
		seen[normalizedName] = struct{}{}

		validation := validateCodexLocalConfigContent(name, profile.ConfigToml, profile.AuthJSON)
		if !validation.OK {
			return CodexLocalConfigTransferResult{}, errors.New(validation.Message)
		}

		saved, err := m.SaveProfileContent(CodexLocalConfigSaveInput{
			Name:       name,
			ConfigToml: profile.ConfigToml,
			AuthJSON:   profile.AuthJSON,
		})
		if err != nil {
			return CodexLocalConfigTransferResult{}, err
		}
		importedNames = append(importedNames, saved.Name)
	}

	return CodexLocalConfigTransferResult{
		Path:  path,
		Count: len(importedNames),
		Names: importedNames,
	}, nil
}

func (m *codexLocalConfigManager) ExportAllProfilesToFile(path string) (CodexLocalConfigTransferResult, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return CodexLocalConfigTransferResult{}, errors.New("导出路径不能为空")
	}

	snapshot, err := m.Snapshot()
	if err != nil {
		return CodexLocalConfigTransferResult{}, err
	}
	if len(snapshot.Profiles) == 0 {
		return CodexLocalConfigTransferResult{}, errors.New("当前没有可导出的供应商配置")
	}

	profiles := make([]codexLocalConfigTransferProfile, 0, len(snapshot.Profiles))
	names := make([]string, 0, len(snapshot.Profiles))
	for _, profile := range snapshot.Profiles {
		content, err := m.ProfileContent(profile.Name)
		if err != nil {
			return CodexLocalConfigTransferResult{}, err
		}
		profiles = append(profiles, codexLocalConfigTransferProfile{
			Name:       content.Name,
			ConfigToml: content.ConfigToml,
			AuthJSON:   content.AuthJSON,
		})
		names = append(names, content.Name)
	}

	payload := codexLocalConfigTransferFile{
		Kind:       codexLocalConfigTransferListKind,
		Version:    codexLocalConfigTransferVersion,
		Profiles:   profiles,
		ExportedAt: nowISO(),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return CodexLocalConfigTransferResult{}, err
	}

	parentDir := filepath.Dir(path)
	if strings.TrimSpace(parentDir) != "" {
		if err := ensureDir(parentDir); err != nil {
			return CodexLocalConfigTransferResult{}, err
		}
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return CodexLocalConfigTransferResult{}, err
	}

	return CodexLocalConfigTransferResult{
		Path:  path,
		Count: len(names),
		Names: names,
	}, nil
}

func (m *codexLocalConfigManager) Delete(name string) (CodexLocalConfigSnapshot, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return CodexLocalConfigSnapshot{}, errors.New("供应商名称不能为空")
	}

	index, err := m.loadIndex()
	if err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	targetIndex, target := findCodexLocalConfigProfile(index.Profiles, name)
	if target == nil {
		return CodexLocalConfigSnapshot{}, fmt.Errorf("找不到供应商配置 %q", name)
	}
	if strings.EqualFold(strings.TrimSpace(index.ActiveProfileName), strings.TrimSpace(target.Name)) {
		return CodexLocalConfigSnapshot{}, errors.New("不能删除当前激活的供应商配置")
	}

	profileDir, err := m.profileDir(target.DirName)
	if err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	if err := ensurePathWithinRoot(m.profilesDir(), profileDir); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	if err := os.RemoveAll(profileDir); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}

	index.Profiles = append(index.Profiles[:targetIndex], index.Profiles[targetIndex+1:]...)
	if err := m.saveIndex(index); err != nil {
		return CodexLocalConfigSnapshot{}, err
	}
	return m.Snapshot()
}

func (m *codexLocalConfigManager) DefaultDirectory() string {
	return m.defaultDirectory
}

func (m *codexLocalConfigManager) loadIndex() (codexLocalConfigIndex, error) {
	path := m.indexPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return codexLocalConfigIndex{}, nil
	}
	if err != nil {
		return codexLocalConfigIndex{}, err
	}

	var index codexLocalConfigIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return codexLocalConfigIndex{}, err
	}
	if index.Profiles == nil {
		index.Profiles = []codexLocalConfigIndexProfile{}
	}
	return index, nil
}

func (m *codexLocalConfigManager) saveIndex(index codexLocalConfigIndex) error {
	if err := m.ensureStorageLayout(); err != nil {
		return err
	}
	if index.Profiles == nil {
		index.Profiles = []codexLocalConfigIndexProfile{}
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.indexPath(), data, 0o600)
}

func (m *codexLocalConfigManager) ensureStorageLayout() error {
	for _, path := range []string{m.rootDir(), m.profilesDir(), m.backupsDir()} {
		if err := ensureDir(path); err != nil {
			return err
		}
	}
	return nil
}

func (m *codexLocalConfigManager) listBackups() ([]CodexLocalConfigBackup, error) {
	entries, err := os.ReadDir(m.backupsDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	backups := make([]CodexLocalConfigBackup, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		fullPath := filepath.Join(m.backupsDir(), entry.Name())
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		backups = append(backups, CodexLocalConfigBackup{
			Name:          entry.Name(),
			CreatedAt:     info.ModTime().UTC().Format(time.RFC3339),
			HasConfigToml: fileExists(filepath.Join(fullPath, codexConfigTomlFileName)),
			HasAuthJSON:   fileExists(filepath.Join(fullPath, codexAuthJSONFileName)),
		})
	}
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].CreatedAt > backups[j].CreatedAt
	})
	return backups, nil
}

func (m *codexLocalConfigManager) createBackupIfNeeded() error {
	if !fileExists(m.defaultConfigPath()) && !fileExists(m.defaultAuthPath()) {
		return nil
	}

	if err := ensureDir(m.backupsDir()); err != nil {
		return err
	}
	backupDir := filepath.Join(m.backupsDir(), time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := ensureDir(backupDir); err != nil {
		return err
	}
	if fileExists(m.defaultConfigPath()) {
		data, err := os.ReadFile(m.defaultConfigPath())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(backupDir, codexConfigTomlFileName), data, 0o600); err != nil {
			return err
		}
	}
	if fileExists(m.defaultAuthPath()) {
		data, err := os.ReadFile(m.defaultAuthPath())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(backupDir, codexAuthJSONFileName), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (m *codexLocalConfigManager) rootDir() string {
	return filepathJoin(m.store.dataDir, codexLocalConfigRootDirName)
}

func (m *codexLocalConfigManager) profilesDir() string {
	return filepathJoin(m.rootDir(), codexLocalConfigProfilesDirName)
}

func (m *codexLocalConfigManager) backupsDir() string {
	return filepathJoin(m.rootDir(), codexLocalConfigBackupsDirName)
}

func (m *codexLocalConfigManager) indexPath() string {
	return filepathJoin(m.rootDir(), codexLocalConfigIndexFileName)
}

func (m *codexLocalConfigManager) profileDir(dirName string) (string, error) {
	path := filepath.Join(m.profilesDir(), dirName)
	if err := ensurePathWithinRoot(m.profilesDir(), path); err != nil {
		return "", err
	}
	return path, nil
}

func (m *codexLocalConfigManager) defaultConfigPath() string {
	return filepath.Join(m.defaultDirectory, codexConfigTomlFileName)
}

func (m *codexLocalConfigManager) defaultAuthPath() string {
	return filepath.Join(m.defaultDirectory, codexAuthJSONFileName)
}

func codexLocalConfigDirectoryName(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	slug := codexLocalConfigSlugPattern.ReplaceAllString(normalized, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "profile"
	}
	hash := sha1.Sum([]byte(normalized))
	return fmt.Sprintf("%s-%s", slug, hex.EncodeToString(hash[:4]))
}

func (m *codexLocalConfigManager) loadTransferFile(path string) (codexLocalConfigTransferFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return codexLocalConfigTransferFile{}, err
	}

	var payload codexLocalConfigTransferFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return codexLocalConfigTransferFile{}, fmt.Errorf("导入文件不是合法 JSON：%w", err)
	}
	if payload.Kind != "" && payload.Kind != codexLocalConfigTransferKind && payload.Kind != codexLocalConfigTransferListKind {
		return codexLocalConfigTransferFile{}, fmt.Errorf("不支持的配置文件类型：%s", payload.Kind)
	}
	if payload.Version != 0 && payload.Version != codexLocalConfigTransferVersion {
		return codexLocalConfigTransferFile{}, fmt.Errorf("不支持的配置文件版本：%d", payload.Version)
	}
	return payload, nil
}

func codexLocalConfigTransferProfiles(payload codexLocalConfigTransferFile) ([]codexLocalConfigTransferProfile, error) {
	if len(payload.Profiles) > 0 {
		return payload.Profiles, nil
	}

	name := strings.TrimSpace(payload.Name)
	if name == "" {
		return nil, errors.New("导入文件中没有可用的供应商配置")
	}
	return []codexLocalConfigTransferProfile{
		{
			Name:       payload.Name,
			ConfigToml: payload.ConfigToml,
			AuthJSON:   payload.AuthJSON,
		},
	}, nil
}

func findCodexLocalConfigProfile(profiles []codexLocalConfigIndexProfile, name string) (int, *codexLocalConfigIndexProfile) {
	for index := range profiles {
		if strings.EqualFold(strings.TrimSpace(profiles[index].Name), strings.TrimSpace(name)) {
			return index, &profiles[index]
		}
	}
	return -1, nil
}

func ensurePathWithinRoot(root string, target string) error {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	targetPath, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(rootPath, targetPath)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("目标路径超出允许范围")
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (m *codexLocalConfigManager) syncMCPConfigAcrossProfiles(index *codexLocalConfigIndex, sourceConfig string) (bool, error) {
	overlay := extractCodexLocalConfigMCPOverlay(sourceConfig)
	if overlay.empty() {
		return false, nil
	}

	updated := false
	now := nowISO()
	for i := range index.Profiles {
		profileDir, err := m.profileDir(index.Profiles[i].DirName)
		if err != nil {
			return false, err
		}
		configPath := filepath.Join(profileDir, codexConfigTomlFileName)
		configBytes, err := os.ReadFile(configPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}

		merged, changed := mergeCodexLocalConfigMCP(string(configBytes), overlay)
		if !changed {
			continue
		}
		if err := os.WriteFile(configPath, []byte(merged), 0o600); err != nil {
			return false, err
		}
		index.Profiles[i].UpdatedAt = now
		updated = true
	}

	return updated, nil
}

func extractCodexLocalConfigMCPOverlay(config string) codexLocalConfigMCPOverlay {
	doc := parseCodexLocalConfigDocument(config)
	overlay := codexLocalConfigMCPOverlay{
		topLevelKeys: make(map[string]string),
		targetKeys:   make(map[string]struct{}),
	}

	for _, line := range doc.preamble {
		key, ok := parseCodexLocalConfigRootKey(line)
		if !ok || !isCodexLocalConfigMCPRootKey(key) {
			continue
		}
		if _, exists := overlay.topLevelKeys[key]; !exists {
			overlay.topLevelOrder = append(overlay.topLevelOrder, key)
		}
		overlay.topLevelKeys[key] = line
	}

	for _, block := range doc.blocks {
		targetKey, ok := codexLocalConfigMCPOverlayKey(block.path)
		if !ok {
			continue
		}
		overlay.blocks = append(overlay.blocks, block)
		overlay.targetKeys[targetKey] = struct{}{}
	}

	return overlay
}

func (o codexLocalConfigMCPOverlay) empty() bool {
	return len(o.topLevelOrder) == 0 && len(o.blocks) == 0
}

func mergeCodexLocalConfigMCP(targetConfig string, overlay codexLocalConfigMCPOverlay) (string, bool) {
	if overlay.empty() {
		return targetConfig, false
	}

	doc := parseCodexLocalConfigDocument(targetConfig)
	doc.preamble = mergeCodexLocalConfigMCPPreamble(doc.preamble, overlay)

	filteredBlocks := make([]codexLocalConfigTableBlock, 0, len(doc.blocks)+len(overlay.blocks))
	for _, block := range doc.blocks {
		targetKey, ok := codexLocalConfigMCPOverlayKey(block.path)
		if ok {
			if _, replace := overlay.targetKeys[targetKey]; replace {
				continue
			}
		}
		filteredBlocks = append(filteredBlocks, block)
	}
	filteredBlocks = append(filteredBlocks, overlay.blocks...)
	doc.blocks = filteredBlocks

	merged := doc.String()
	return merged, merged != targetConfig
}

func mergeCodexLocalConfigMCPPreamble(preamble []string, overlay codexLocalConfigMCPOverlay) []string {
	if len(overlay.topLevelOrder) == 0 {
		return append([]string{}, preamble...)
	}

	result := make([]string, 0, len(preamble)+len(overlay.topLevelOrder)+1)
	for _, line := range preamble {
		key, ok := parseCodexLocalConfigRootKey(line)
		if ok && isCodexLocalConfigMCPRootKey(key) {
			if _, replace := overlay.topLevelKeys[key]; replace {
				continue
			}
		}
		result = append(result, line)
	}

	if len(result) > 0 && strings.TrimSpace(result[len(result)-1]) != "" {
		result = append(result, "")
	}
	for _, key := range overlay.topLevelOrder {
		result = append(result, overlay.topLevelKeys[key])
	}
	return result
}

func parseCodexLocalConfigDocument(config string) codexLocalConfigDocument {
	lines, newline, trailingNewline := splitCodexLocalConfigLines(config)
	doc := codexLocalConfigDocument{
		newline:         newline,
		trailingNewline: trailingNewline,
	}
	if len(lines) == 0 {
		return doc
	}

	currentStart := -1
	var currentPath []string
	for i, line := range lines {
		path, ok := parseCodexLocalConfigTablePath(line)
		if !ok {
			continue
		}

		if currentStart == -1 {
			doc.preamble = append(doc.preamble, lines[:i]...)
		} else {
			doc.blocks = append(doc.blocks, codexLocalConfigTableBlock{
				path:  append([]string{}, currentPath...),
				lines: append([]string{}, lines[currentStart:i]...),
			})
		}
		currentStart = i
		currentPath = append([]string{}, path...)
	}

	if currentStart == -1 {
		doc.preamble = append(doc.preamble, lines...)
		return doc
	}

	doc.blocks = append(doc.blocks, codexLocalConfigTableBlock{
		path:  append([]string{}, currentPath...),
		lines: append([]string{}, lines[currentStart:]...),
	})
	return doc
}

func (d codexLocalConfigDocument) String() string {
	lines := make([]string, 0, len(d.preamble))
	lines = append(lines, d.preamble...)
	for _, block := range d.blocks {
		lines = append(lines, block.lines...)
	}
	return joinCodexLocalConfigLines(lines, d.newline, d.trailingNewline)
}

func splitCodexLocalConfigLines(config string) ([]string, string, bool) {
	newline := "\n"
	if strings.Contains(config, "\r\n") {
		newline = "\r\n"
	}
	normalized := strings.ReplaceAll(config, "\r\n", "\n")
	trailingNewline := strings.HasSuffix(normalized, "\n")
	if trailingNewline {
		normalized = strings.TrimSuffix(normalized, "\n")
	}
	if normalized == "" {
		return nil, newline, trailingNewline
	}
	return strings.Split(normalized, "\n"), newline, trailingNewline
}

func joinCodexLocalConfigLines(lines []string, newline string, trailingNewline bool) string {
	if newline == "" {
		newline = "\n"
	}
	if len(lines) == 0 {
		if trailingNewline {
			return newline
		}
		return ""
	}
	text := strings.Join(lines, newline)
	if trailingNewline {
		text += newline
	}
	return text
}

func parseCodexLocalConfigRootKey(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	matches := codexLocalConfigRootKeyPattern.FindStringSubmatch(trimmed)
	if len(matches) != 2 {
		return "", false
	}
	return matches[1], true
}

func isCodexLocalConfigMCPRootKey(key string) bool {
	return strings.HasPrefix(strings.TrimSpace(key), "mcp_")
}

func parseCodexLocalConfigTablePath(line string) ([]string, bool) {
	matches := codexLocalConfigTableHeaderPattern.FindStringSubmatch(line)
	if len(matches) != 2 {
		return nil, false
	}
	return parseCodexLocalConfigKeyPath(matches[1])
}

func parseCodexLocalConfigKeyPath(raw string) ([]string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}

	parts := make([]string, 0, 4)
	for len(raw) > 0 {
		raw = strings.TrimLeft(raw, " \t")
		if raw == "" {
			break
		}

		var part string
		switch raw[0] {
		case '"':
			value, rest, ok := consumeCodexLocalQuotedKey(raw, '"')
			if !ok {
				return nil, false
			}
			part = value
			raw = rest
		case '\'':
			value, rest, ok := consumeCodexLocalQuotedKey(raw, '\'')
			if !ok {
				return nil, false
			}
			part = value
			raw = rest
		default:
			index := strings.IndexByte(raw, '.')
			if index < 0 {
				part = strings.TrimSpace(raw)
				raw = ""
			} else {
				part = strings.TrimSpace(raw[:index])
				raw = raw[index:]
			}
			if part == "" {
				return nil, false
			}
		}

		parts = append(parts, part)
		raw = strings.TrimLeft(raw, " \t")
		if raw == "" {
			break
		}
		if raw[0] != '.' {
			return nil, false
		}
		raw = raw[1:]
	}

	if len(parts) == 0 {
		return nil, false
	}
	return parts, true
}

func consumeCodexLocalQuotedKey(raw string, quote byte) (string, string, bool) {
	if len(raw) == 0 || raw[0] != quote {
		return "", raw, false
	}
	var builder strings.Builder
	escaped := false
	for i := 1; i < len(raw); i++ {
		ch := raw[i]
		if quote == '"' && escaped {
			builder.WriteByte(ch)
			escaped = false
			continue
		}
		if quote == '"' && ch == '\\' {
			escaped = true
			continue
		}
		if ch == quote {
			return builder.String(), raw[i+1:], true
		}
		builder.WriteByte(ch)
	}
	return "", raw, false
}

func codexLocalConfigMCPOverlayKey(path []string) (string, bool) {
	if len(path) == 0 || path[0] != "mcp_servers" {
		return "", false
	}
	if len(path) == 1 {
		return "mcp_servers", true
	}
	return "mcp_servers." + path[1], true
}

func validateCodexLocalConfigContent(name string, configToml string, authJSON string) CodexLocalConfigValidationResult {
	result := CodexLocalConfigValidationResult{
		Name:     strings.TrimSpace(name),
		TestedAt: nowISO(),
	}

	var tomlPayload map[string]any
	if _, err := toml.Decode(configToml, &tomlPayload); err != nil {
		result.ConfigTomlValid = false
	} else {
		result.ConfigTomlValid = true
	}

	var jsonPayload any
	if err := json.Unmarshal([]byte(authJSON), &jsonPayload); err != nil {
		result.AuthJSONValid = false
	} else {
		result.AuthJSONValid = true
	}

	result.OK = result.ConfigTomlValid && result.AuthJSONValid
	switch {
	case result.OK:
		result.Message = "配置校验通过：config.toml 语法有效，auth.json 格式有效。"
	case !result.ConfigTomlValid && !result.AuthJSONValid:
		result.Message = "配置校验失败：config.toml 语法无效，auth.json 不是合法 JSON。"
	case !result.ConfigTomlValid:
		result.Message = "配置校验失败：config.toml 语法无效。"
	default:
		result.Message = "配置校验失败：auth.json 不是合法 JSON。"
	}

	return result
}

func testCodexProfileConnection(content CodexLocalConfigProfileContent) (CodexLocalConfigConnectionTestResult, error) {
	result := CodexLocalConfigConnectionTestResult{
		Name:     strings.TrimSpace(content.Name),
		TestedAt: nowISO(),
	}

	var parsedConfig codexLocalParsedConfig
	if _, err := toml.Decode(content.ConfigToml, &parsedConfig); err != nil {
		result.Message = fmt.Sprintf("config.toml 无法解析：%v", err)
		return result, nil
	}

	var authValues map[string]any
	if err := json.Unmarshal([]byte(content.AuthJSON), &authValues); err != nil {
		result.Message = fmt.Sprintf("auth.json 无法解析：%v", err)
		return result, nil
	}

	model := strings.TrimSpace(parsedConfig.Model)
	providerName := strings.TrimSpace(parsedConfig.ModelProvider)
	if model == "" {
		result.Message = "config.toml 缺少 model"
		return result, nil
	}
	if providerName == "" {
		result.Message = "config.toml 缺少 model_provider"
		return result, nil
	}
	provider := parsedConfig.ModelProviders[providerName]
	baseURL := strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	if baseURL == "" {
		result.Message = fmt.Sprintf("config.toml 缺少 model_providers.%s.base_url", providerName)
		return result, nil
	}
	apiKey := strings.TrimSpace(stringValue(authValues["OPENAI_API_KEY"]))
	if apiKey == "" {
		apiKey = strings.TrimSpace(stringValue(authValues["openai_api_key"]))
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(stringValue(authValues["api_key"]))
	}
	if apiKey == "" {
		result.Message = "auth.json 缺少 API Key"
		return result, nil
	}

	result.ProviderName = stringOr(provider.Name, providerName)
	result.BaseURL = baseURL
	result.Model = model

	endpoint := "/responses"
	requestBody := map[string]any{
		"model":             model,
		"input":             "Reply exactly: ok",
		"max_output_tokens": 1,
		"store":             false,
	}
	switch normalizeWireAPI(provider.WireAPI) {
	case "chat_completions":
		endpoint = "/chat/completions"
		requestBody = map[string]any{
			"model": model,
			"messages": []map[string]string{
				{"role": "user", "content": "Reply exactly: ok"},
			},
			"max_tokens": 1,
			"stream":     false,
		}
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return result, err
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+endpoint, bytes.NewReader(payload))
	if err != nil {
		result.Message = fmt.Sprintf("请求构造失败：%v", err)
		return result, nil
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		result.Message = fmt.Sprintf("连接失败：%v", err)
		return result, nil
	}
	defer resp.Body.Close()
	result.StatusCode = intPtr(resp.StatusCode)

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.OK = true
		result.Message = fmt.Sprintf("连接成功：%s %s", result.ProviderName, result.Model)
		return result, nil
	}

	bodyMessage := extractCodexConnectionErrorMessage(bodyBytes)
	if bodyMessage == "" {
		bodyMessage = strings.TrimSpace(string(bodyBytes))
	}
	if bodyMessage == "" {
		bodyMessage = http.StatusText(resp.StatusCode)
	}
	result.Message = fmt.Sprintf("连接失败（HTTP %d）：%s", resp.StatusCode, normalizeText(bodyMessage, 180))
	return result, nil
}

func normalizeWireAPI(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "chat_completions", "chat-completions", "chat.completions":
		return "chat_completions"
	default:
		return "responses"
	}
}

func extractCodexConnectionErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if errorPayload, ok := payload["error"].(map[string]any); ok {
		return strings.TrimSpace(stringValue(errorPayload["message"]))
	}
	return strings.TrimSpace(stringValue(payload["message"]))
}
