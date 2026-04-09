import { defineStore } from 'pinia'
import { EventsOff, EventsOn } from '../../wailsjs/runtime/runtime'
import {
  ApplyLauncherConnection,
  CheckLauncherForUpdate,
  ClearLauncherLogs,
  GenerateLauncherConfig,
  GetLauncherStatus,
  InstallLauncherLatest,
  OpenLauncherConfigDirectory,
  OpenLauncherExecutableDirectory,
  OpenLauncherLogsDirectory,
  OpenLauncherManagementPage,
  RefreshLauncherStatus,
  SaveLauncherSettings,
  SelectLauncherConfigPath,
  SelectLauncherConfigSavePath,
  SelectLauncherExecutablePath,
  SelectLauncherInstallDirectory,
  StartLauncherService,
  StopLauncherService,
  UpdateLauncherCPA,
} from '../../wailsjs/go/main/App'
import { backend as backendModels } from '../../wailsjs/go/models'
import type {
  LauncherConfigTemplateInput,
  LauncherSettings,
  LauncherStatusSnapshot,
  LauncherUpdateInfo,
  LogEntry,
} from '@/types'
import { createDefaultLauncherSettings } from '@/utils/settings'
import { useSettingsStore } from '@/stores/settings'

interface LauncherState {
  settings: LauncherSettings
  status: LauncherStatusSnapshot
  logs: LogEntry[]
  loading: boolean
  busy: boolean
  bridgeReady: boolean
}

function createEmptyUpdate(): LauncherUpdateInfo {
  return {
    available: false,
    currentVersion: '',
    tagName: '',
    assetSize: 0,
    releaseUrl: '',
    checkedAt: '',
    message: '',
    checkSource: '',
  }
}

function createEmptyStatus(): LauncherStatusSnapshot {
  return {
    status: 'unconfigured',
    statusText: '',
    statusDetail: '',
    managed: false,
    serviceReachable: false,
    managedProcessId: 0,
    settings: createDefaultLauncherSettings(),
    runtime: null,
    update: createEmptyUpdate(),
    logs: [],
  }
}

export const useLauncherStore = defineStore('launcherStore', {
  state: (): LauncherState => ({
    settings: createDefaultLauncherSettings(),
    status: createEmptyStatus(),
    logs: [],
    loading: false,
    busy: false,
    bridgeReady: false,
  }),
  getters: {
    hasRuntime: (state) => Boolean(state.status.runtime),
    canStart: (state) => ['stopped', 'start_failed'].includes(state.status.status),
    canStop: (state) => ['starting', 'running', 'stopping'].includes(state.status.status) && state.status.managed,
  },
  actions: {
    mergeSnapshot(snapshot?: Partial<LauncherStatusSnapshot> | null, syncSettings = false) {
      this.status = {
        ...createEmptyStatus(),
        ...(snapshot ?? {}),
        settings: {
          ...createDefaultLauncherSettings(),
          ...(snapshot?.settings ?? {}),
        },
        update: {
          ...createEmptyUpdate(),
          ...(snapshot?.update ?? {}),
        },
        logs: Array.isArray(snapshot?.logs) ? snapshot.logs : [],
      }

      this.logs = Array.isArray(this.status.logs) ? [...this.status.logs] : []
      if (syncSettings) {
        this.settings = {
          ...createDefaultLauncherSettings(),
          ...this.status.settings,
        }
      }
    },
    pushLog(entry: LogEntry) {
      this.logs.unshift(entry)
      this.logs = this.logs.slice(0, 400)
    },
    initBridge() {
      if (this.bridgeReady) {
        return
      }
      EventsOn('launcher:status', (payload: LauncherStatusSnapshot) => this.mergeSnapshot(payload, false))
      EventsOn('launcher:log', (entry: LogEntry) => this.pushLog(entry))
      this.bridgeReady = true
    },
    destroyBridge() {
      if (!this.bridgeReady) {
        return
      }
      EventsOff('launcher:status')
      EventsOff('launcher:log')
      this.bridgeReady = false
    },
    async loadStatus(syncSettings = true) {
      this.loading = true
      try {
        const snapshot = await GetLauncherStatus()
        this.mergeSnapshot(snapshot as unknown as Partial<LauncherStatusSnapshot>, syncSettings)
        return this.status
      } finally {
        this.loading = false
      }
    },
    async refresh(syncSettings = false) {
      this.loading = true
      try {
        const snapshot = await RefreshLauncherStatus()
        this.mergeSnapshot(snapshot as unknown as Partial<LauncherStatusSnapshot>, syncSettings)
        return this.status
      } finally {
        this.loading = false
      }
    },
    async saveSettings() {
      this.busy = true
      try {
        const snapshot = await SaveLauncherSettings(new backendModels.LauncherSettings(this.settings))
        this.mergeSnapshot(snapshot as unknown as Partial<LauncherStatusSnapshot>, true)
        return this.status
      } finally {
        this.busy = false
      }
    },
    async startService() {
      this.busy = true
      try {
        const snapshot = await StartLauncherService()
        this.mergeSnapshot(snapshot as unknown as Partial<LauncherStatusSnapshot>, false)
        return this.status
      } finally {
        this.busy = false
      }
    },
    async stopService() {
      this.busy = true
      try {
        const snapshot = await StopLauncherService()
        this.mergeSnapshot(snapshot as unknown as Partial<LauncherStatusSnapshot>, false)
        return this.status
      } finally {
        this.busy = false
      }
    },
    async clearLogs() {
      const snapshot = await ClearLauncherLogs()
      this.mergeSnapshot(snapshot as unknown as Partial<LauncherStatusSnapshot>, false)
      return this.status
    },
    async checkForUpdate() {
      this.busy = true
      try {
        const snapshot = await CheckLauncherForUpdate()
        this.mergeSnapshot(snapshot as unknown as Partial<LauncherStatusSnapshot>, false)
        return this.status
      } finally {
        this.busy = false
      }
    },
    async installLatest(targetDirectory: string) {
      this.busy = true
      try {
        const snapshot = await InstallLauncherLatest(targetDirectory)
        this.mergeSnapshot(snapshot as unknown as Partial<LauncherStatusSnapshot>, true)
        return this.status
      } finally {
        this.busy = false
      }
    },
    async updateCPA() {
      this.busy = true
      try {
        const snapshot = await UpdateLauncherCPA()
        this.mergeSnapshot(snapshot as unknown as Partial<LauncherStatusSnapshot>, true)
        return this.status
      } finally {
        this.busy = false
      }
    },
    async generateConfig(input: LauncherConfigTemplateInput) {
      this.busy = true
      try {
        const snapshot = await GenerateLauncherConfig(new backendModels.LauncherConfigTemplateInput(input))
        this.mergeSnapshot(snapshot as unknown as Partial<LauncherStatusSnapshot>, true)
        return this.status
      } finally {
        this.busy = false
      }
    },
    async applyConnection() {
      await ApplyLauncherConnection()
      await useSettingsStore().loadSettings()
    },
    async chooseExecutablePath() {
      const result = await SelectLauncherExecutablePath()
      if (result) {
        this.settings.executablePath = result
      }
      return result
    },
    async chooseConfigPath() {
      const result = await SelectLauncherConfigPath()
      if (result) {
        this.settings.configPath = result
      }
      return result
    },
    async chooseInstallDirectory() {
      return await SelectLauncherInstallDirectory()
    },
    async chooseConfigSavePath() {
      return await SelectLauncherConfigSavePath()
    },
    async openManagementPage() {
      await OpenLauncherManagementPage()
    },
    async openLogsDirectory() {
      await OpenLauncherLogsDirectory()
    },
    async openExecutableDirectory() {
      await OpenLauncherExecutableDirectory()
    },
    async openConfigDirectory() {
      await OpenLauncherConfigDirectory()
    },
  },
})
