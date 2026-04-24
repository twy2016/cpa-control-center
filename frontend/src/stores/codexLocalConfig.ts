import { defineStore } from 'pinia'
import {
  DeleteCodexLocalConfigProfile,
  ExportCodexLocalConfigProfile,
  ExportCodexLocalConfigProfiles,
  GetCodexLocalConfigSnapshot,
  GetCodexLocalConfigProfileContent,
  ImportCodexLocalConfigProfile,
  ImportCodexLocalConfigProfiles,
  ImportCurrentCodexLocalConfig,
  OpenCodexLocalConfigDirectory,
  ReloadCodexLocalConfigProfileContent,
  SaveCodexLocalConfigProfileContent,
  TestCodexLocalConfigProfileContent,
  TestCodexLocalConfigProfileConnection,
  SwitchCodexLocalConfigProfile,
} from '../../wailsjs/go/main/App'
import { backend as backendModels } from '../../wailsjs/go/models'
import type {
  CodexLocalConfigConnectionTestResult,
  CodexLocalConfigProfileContent,
  CodexLocalConfigSnapshot,
  CodexLocalConfigTransferResult,
  CodexLocalConfigValidationResult,
} from '@/types'

interface CodexLocalConfigState {
  snapshot: CodexLocalConfigSnapshot
  selectedProfileName: string
  profileContent: CodexLocalConfigProfileContent | null
  loading: boolean
  busy: boolean
  contentLoading: boolean
  contentSaving: boolean
  contentTesting: boolean
  connectionTestingName: string
  lastConnectionResults: Record<string, CodexLocalConfigConnectionTestResult>
}

function createEmptySnapshot(): CodexLocalConfigSnapshot {
  return {
    profiles: [],
    activeProfileName: '',
    defaultDirectory: '',
    configPath: '',
    authPath: '',
    currentConfigExists: false,
    currentAuthExists: false,
    backups: [],
  }
}

function createEmptyProfileContent(): CodexLocalConfigProfileContent {
  return {
    name: '',
    originalName: '',
    configToml: '',
    authJson: '',
    updatedAt: '',
  }
}

function createCodexProfileTemplate(name = ''): CodexLocalConfigProfileContent {
  return {
    name,
    originalName: '',
    configToml: [
      'model = "gpt-5"',
      'model_provider = "openai"',
      '',
      '[model_providers.openai]',
      'name = "openai"',
      'base_url = "https://api.openai.com/v1"',
      'wire_api = "responses"',
      '',
    ].join('\n'),
    authJson: [
      '{',
      '  "OPENAI_API_KEY": "sk-your-api-key"',
      '}',
      '',
    ].join('\n'),
    updatedAt: '',
  }
}

export const useCodexLocalConfigStore = defineStore('codexLocalConfigStore', {
  state: (): CodexLocalConfigState => ({
    snapshot: createEmptySnapshot(),
    selectedProfileName: '',
    profileContent: null,
    loading: false,
    busy: false,
    contentLoading: false,
    contentSaving: false,
    contentTesting: false,
    connectionTestingName: '',
    lastConnectionResults: {},
  }),
  getters: {
    activeProfile: (state) => state.snapshot.activeProfileName || '',
    selectedProfile: (state) => state.selectedProfileName || '',
  },
  actions: {
    applySnapshot(snapshot?: Partial<CodexLocalConfigSnapshot> | null) {
      this.snapshot = {
        ...createEmptySnapshot(),
        ...(snapshot ?? {}),
        profiles: Array.isArray(snapshot?.profiles) ? snapshot.profiles : [],
        backups: Array.isArray(snapshot?.backups) ? snapshot.backups : [],
      }
      const profileNames = this.snapshot.profiles.map((profile) => profile.name)
      if (profileNames.length === 0) {
        this.selectedProfileName = ''
        this.profileContent = null
        return
      }
      if (!this.selectedProfileName || !profileNames.includes(this.selectedProfileName)) {
        this.selectedProfileName = profileNames.includes(this.snapshot.activeProfileName)
          ? this.snapshot.activeProfileName
          : profileNames[0]
        this.profileContent = null
      }
    },
    async loadProfileContent(name?: string) {
      const target = (name || this.selectedProfileName || '').trim()
      if (!target) {
        this.profileContent = null
        return null
      }
      this.selectedProfileName = target
      this.contentLoading = true
      try {
        const content = await GetCodexLocalConfigProfileContent(target)
        this.profileContent = {
          ...createEmptyProfileContent(),
          ...(content as unknown as Partial<CodexLocalConfigProfileContent>),
        }
        return this.profileContent
      } finally {
        this.contentLoading = false
      }
    },
    async selectProfile(name: string) {
      this.selectedProfileName = name
      return await this.loadProfileContent(name)
    },
    async reloadProfileContent(name?: string) {
      const target = (name || this.selectedProfileName || '').trim()
      if (!target) {
        this.profileContent = null
        return null
      }
      this.selectedProfileName = target
      this.contentLoading = true
      try {
        const content = await ReloadCodexLocalConfigProfileContent(target)
        this.profileContent = {
          ...createEmptyProfileContent(),
          ...(content as unknown as Partial<CodexLocalConfigProfileContent>),
        }
        const snapshot = await GetCodexLocalConfigSnapshot()
        this.applySnapshot(snapshot as unknown as Partial<CodexLocalConfigSnapshot>)
        return this.profileContent
      } finally {
        this.contentLoading = false
      }
    },
    async loadSnapshot() {
      this.loading = true
      try {
        const snapshot = await GetCodexLocalConfigSnapshot()
        this.applySnapshot(snapshot as unknown as Partial<CodexLocalConfigSnapshot>)
        if (this.selectedProfileName) {
          await this.loadProfileContent(this.selectedProfileName)
        }
        return this.snapshot
      } finally {
        this.loading = false
      }
    },
    async importCurrent(name: string) {
      this.busy = true
      try {
        const snapshot = await ImportCurrentCodexLocalConfig(new backendModels.CodexLocalConfigImportInput({ name }))
        this.applySnapshot(snapshot as unknown as Partial<CodexLocalConfigSnapshot>)
        this.selectedProfileName = name
        await this.loadProfileContent(name)
        return this.snapshot
      } finally {
        this.busy = false
      }
    },
    createProfileTemplate(name = '') {
      return createCodexProfileTemplate(name)
    },
    async importProfileFromFile() {
      this.busy = true
      try {
        const name = (await ImportCodexLocalConfigProfile())?.trim() || ''
        if (!name) {
          return ''
        }
        await this.loadSnapshot()
        this.selectedProfileName = name
        await this.loadProfileContent(name)
        return name
      } finally {
        this.busy = false
      }
    },
    async importProfilesFromFile() {
      this.busy = true
      try {
        const result = await ImportCodexLocalConfigProfiles()
        const typed = {
          path: result?.path || '',
          count: result?.count || 0,
          names: Array.isArray(result?.names) ? result.names : [],
        } as CodexLocalConfigTransferResult
        if (!typed.count) {
          return typed
        }
        await this.loadSnapshot()
        const selectedName = typed.names[typed.names.length - 1] || ''
        if (selectedName) {
          this.selectedProfileName = selectedName
          await this.loadProfileContent(selectedName)
        }
        return typed
      } finally {
        this.busy = false
      }
    },
    async exportProfile(name: string) {
      this.busy = true
      try {
        return (await ExportCodexLocalConfigProfile(name))?.trim() || ''
      } finally {
        this.busy = false
      }
    },
    async exportAllProfiles() {
      this.busy = true
      try {
        const result = await ExportCodexLocalConfigProfiles()
        return {
          path: result?.path || '',
          count: result?.count || 0,
          names: Array.isArray(result?.names) ? result.names : [],
        } as CodexLocalConfigTransferResult
      } finally {
        this.busy = false
      }
    },
    async switchProfile(name: string) {
      this.busy = true
      try {
        const snapshot = await SwitchCodexLocalConfigProfile(new backendModels.CodexLocalConfigSwitchInput({ name }))
        this.applySnapshot(snapshot as unknown as Partial<CodexLocalConfigSnapshot>)
        this.selectedProfileName = name
        await this.loadProfileContent(name)
        return this.snapshot
      } finally {
        this.busy = false
      }
    },
    async saveProfileContent() {
      if (!this.profileContent) {
        return null
      }
      this.contentSaving = true
      try {
        const saved = await SaveCodexLocalConfigProfileContent(new backendModels.CodexLocalConfigSaveInput({
          name: this.profileContent.name,
          originalName: this.profileContent.originalName,
          configToml: this.profileContent.configToml,
          authJson: this.profileContent.authJson,
        }))
        this.profileContent = {
          ...createEmptyProfileContent(),
          ...(saved as unknown as Partial<CodexLocalConfigProfileContent>),
        }
        this.selectedProfileName = this.profileContent.name
        await this.loadSnapshot()
        return this.profileContent
      } finally {
        this.contentSaving = false
      }
    },
    async createProfileContent(name: string, configToml: string, authJson: string) {
      this.contentSaving = true
      try {
        const saved = await SaveCodexLocalConfigProfileContent(new backendModels.CodexLocalConfigSaveInput({
          name,
          originalName: '',
          configToml,
          authJson,
        }))
        this.selectedProfileName = name
        this.profileContent = {
          ...createEmptyProfileContent(),
          ...(saved as unknown as Partial<CodexLocalConfigProfileContent>),
        }
        await this.loadSnapshot()
        return this.profileContent
      } finally {
        this.contentSaving = false
      }
    },
    async testProfileContent(name: string, configToml: string, authJson: string) {
      this.contentTesting = true
      try {
        const result = await TestCodexLocalConfigProfileContent(new backendModels.CodexLocalConfigSaveInput({
          name,
          configToml,
          authJson,
        }))
        return result as unknown as CodexLocalConfigValidationResult
      } finally {
        this.contentTesting = false
      }
    },
    async testSavedProfileConnection(name: string) {
      this.connectionTestingName = name
      try {
        const result = await TestCodexLocalConfigProfileConnection(name)
        const typed = result as unknown as CodexLocalConfigConnectionTestResult
        this.lastConnectionResults = {
          ...this.lastConnectionResults,
          [name]: typed,
        }
        return typed
      } finally {
        this.connectionTestingName = ''
      }
    },
    async deleteProfile(name: string) {
      this.busy = true
      try {
        const snapshot = await DeleteCodexLocalConfigProfile(name)
        this.applySnapshot(snapshot as unknown as Partial<CodexLocalConfigSnapshot>)
        if (this.selectedProfileName) {
          await this.loadProfileContent(this.selectedProfileName)
        }
        return this.snapshot
      } finally {
        this.busy = false
      }
    },
    async openDirectory() {
      await OpenCodexLocalConfigDirectory()
    },
  },
})
