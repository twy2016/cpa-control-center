import { defineStore } from 'pinia'
import { EventsOff, EventsOn } from '../../wailsjs/runtime/runtime'
import { GetSchedulerStatus, GetSettings, SaveSettings, TestAndSaveSettings, TestConnection } from '../../wailsjs/go/main/App'
import { backend as backendModels } from '../../wailsjs/go/models'
import { i18n, setI18nLocale } from '@/i18n'
import type { AppSettings, ConnectionResult, SchedulerStatus } from '@/types'
import { createDefaultLauncherSettings, createDefaultScheduleSettings, createDefaultSettings, validateSettings } from '@/utils/settings'
import { toErrorMessage } from '@/utils/errors'
import { detectPreferredLocale, normalizeLocaleCode } from '@/utils/locale'

interface SettingsState {
  settings: AppSettings
  connection: ConnectionResult | null
  schedulerStatus: SchedulerStatus
  loading: boolean
  saving: boolean
  errors: Record<string, string>
  schedulerBridgeReady: boolean
}

function createDefaultSchedulerStatus(): SchedulerStatus {
  return {
    enabled: false,
    mode: 'scan',
    cron: '',
    valid: true,
    validationMessage: '',
    running: false,
    nextRunAt: '',
    lastStartedAt: '',
    lastFinishedAt: '',
    lastStatus: '',
    lastMessage: '',
  }
}

export const useSettingsStore = defineStore('settingsStore', {
  state: (): SettingsState => ({
    settings: createDefaultSettings(),
    connection: null,
    schedulerStatus: createDefaultSchedulerStatus(),
    loading: false,
    saving: false,
    errors: {},
    schedulerBridgeReady: false,
  }),
  getters: {
    connectionTone: (state) => {
      if (!state.connection) {
        return 'idle'
      }
      return state.connection.ok ? 'ok' : 'error'
    },
    currentLocale: (state) => normalizeLocaleCode(state.settings.locale || i18n.global.locale.value),
  },
  actions: {
    setConnectionResult(result?: Partial<ConnectionResult> | null) {
      if (!result) {
        this.connection = null
        return
      }
      this.connection = {
        ok: Boolean(result.ok),
        message: result.message || '',
        accountCount: Number(result.accountCount || 0),
        checkedAt: result.checkedAt || new Date().toISOString(),
      }
    },
    mergeSettings(result: Partial<AppSettings>) {
      this.settings = {
        ...createDefaultSettings(),
        ...result,
        schedule: {
          ...createDefaultScheduleSettings(),
          ...(result.schedule ?? {}),
        },
        launcher: {
          ...createDefaultLauncherSettings(),
          ...(result.launcher ?? {}),
        },
      }
      this.applyLocale(this.settings.locale)
    },
    applySchedulerStatus(status?: Partial<SchedulerStatus> | null) {
      this.schedulerStatus = {
        ...createDefaultSchedulerStatus(),
        ...(status ?? {}),
      }
    },
    initSchedulerBridge() {
      if (this.schedulerBridgeReady) {
        return
      }
      EventsOn('scheduler:status', (status: SchedulerStatus) => this.applySchedulerStatus(status))
      this.schedulerBridgeReady = true
    },
    destroySchedulerBridge() {
      if (!this.schedulerBridgeReady) {
        return
      }
      EventsOff('scheduler:status')
      this.schedulerBridgeReady = false
    },
    applyLocale(locale?: string) {
      const next = setI18nLocale(locale || detectPreferredLocale())
      this.settings.locale = next
    },
    async loadSchedulerStatus() {
      const status = await GetSchedulerStatus()
      this.applySchedulerStatus(status as unknown as Partial<SchedulerStatus>)
      return this.schedulerStatus
    },
    async persistSettings() {
      const saved = await SaveSettings(new backendModels.AppSettings(this.settings))
      this.mergeSettings(saved as unknown as Partial<AppSettings>)
      await this.loadSchedulerStatus()
      return this.settings
    },
    async loadSettings() {
      this.loading = true
      try {
        const result = await GetSettings()
        this.mergeSettings(result as unknown as Partial<AppSettings>)
        await this.loadSchedulerStatus()
      } finally {
        this.loading = false
      }
    },
    async refreshConnectionStatus(silent = true) {
      if (!this.settings.baseUrl.trim() || !this.settings.managementToken.trim()) {
        this.connection = null
        return this.connection
      }

      try {
        const result = await TestConnection(new backendModels.AppSettings(this.settings))
        this.setConnectionResult(result as unknown as Partial<ConnectionResult>)
        return this.connection
      } catch (error) {
        const message = toErrorMessage(error)
        this.setConnectionResult({
          ok: false,
          message,
          accountCount: 0,
          checkedAt: new Date().toISOString(),
        })
        if (!silent) {
          throw new Error(message)
        }
        return this.connection
      }
    },
    async saveLocalePreference(locale: string) {
      const previous = this.currentLocale
      this.applyLocale(locale)
      try {
        await this.persistSettings()
      } catch (error) {
        this.applyLocale(previous)
        throw new Error(toErrorMessage(error))
      }
    },
    async testConnection() {
      this.errors = validateSettings(this.settings, i18n.global.t)
      if (Object.keys(this.errors).length > 0) {
        throw new Error(i18n.global.t('validation.fixBeforeTesting'))
      }
      return await this.refreshConnectionStatus(false)
    },
    async saveSettings() {
      this.errors = validateSettings(this.settings, i18n.global.t)
      if (Object.keys(this.errors).length > 0) {
        throw new Error(i18n.global.t('validation.fixBeforeSaving'))
      }
      this.saving = true
      try {
        return await this.persistSettings()
      } finally {
        this.saving = false
      }
    },
    async testAndSave() {
      try {
        this.errors = validateSettings(this.settings, i18n.global.t)
        if (Object.keys(this.errors).length > 0) {
          throw new Error(i18n.global.t('validation.fixBeforeSaving'))
        }
        this.saving = true
        const connection = await TestAndSaveSettings(new backendModels.AppSettings(this.settings))
        await this.loadSettings()
        this.setConnectionResult(connection as unknown as Partial<ConnectionResult>)
        return connection
      } catch (error) {
        throw new Error(toErrorMessage(error))
      } finally {
        this.saving = false
      }
    },
  },
})
