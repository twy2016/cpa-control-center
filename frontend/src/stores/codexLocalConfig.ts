import { defineStore } from 'pinia'
import {
  DeleteCodexLocalConfigProfile,
  GetCodexLocalConfigSnapshot,
  GetCodexLocalConfigProfileContent,
  ImportCurrentCodexLocalConfig,
  OpenCodexLocalConfigDirectory,
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
    configToml: '',
    authJson: '',
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
          configToml: this.profileContent.configToml,
          authJson: this.profileContent.authJson,
        }))
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
    async createProfileContent(name: string, configToml: string, authJson: string) {
      this.contentSaving = true
      try {
        const saved = await SaveCodexLocalConfigProfileContent(new backendModels.CodexLocalConfigSaveInput({
          name,
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
