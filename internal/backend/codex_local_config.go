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
