<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  ElButton,
  ElForm,
  ElInput,
  ElInputNumber,
  ElMessage,
  ElMessageBox,
  ElSwitch,
} from 'element-plus'
import { useI18n } from 'vue-i18n'
import LogConsole from '@/components/LogConsole.vue'
import { useLauncherStore } from '@/stores/launcher'
import { formatDateTime } from '@/utils/format'
import { toErrorMessage } from '@/utils/errors'

const { t, te } = useI18n()
const launcherStore = useLauncherStore()
const installDirectory = ref('')

const runtime = computed(() => launcherStore.status.runtime)
const statusLabel = computed(() => {
  const key = `launcher.statuses.${launcherStore.status.status || 'unconfigured'}`
  return te(key) ? t(key) : launcherStore.status.statusText || launcherStore.status.status
})
const updateSummary = computed(() => {
  const update = launcherStore.status.update
  const currentVersion = update.currentVersion || launcherStore.settings.lastInstalledVersion

  if (update.available && update.tagName) {
    return t('launcher.updateAvailable', { version: update.tagName })
  }
  if (update.message) {
    return update.message
  }
  if (currentVersion) {
    return `${t('launcher.currentVersion')}: ${currentVersion}`
  }
  return t('common.notAvailable')
})
const updateCheckedAt = computed(() => formatDateTime(launcherStore.status.update.checkedAt))
const updateTone = computed(() => {
  const update = launcherStore.status.update
  const message = `${update.message || ''} ${updateSummary.value}`.toLowerCase()

  if (update.available) {
    return 'available'
  }
  if (/(失败|错误|超时|timeout|error|fail|http\s*\d+)/.test(message)) {
    return 'error'
  }
  return 'neutral'
})
const resolvedInstallDirectory = computed(() => installDirectory.value.trim() || runtime.value?.executableDirectory || '')
const currentVersion = computed(() => launcherStore.status.update.currentVersion || launcherStore.settings.lastInstalledVersion || t('common.notAvailable'))
const latestVersion = computed(() => launcherStore.status.update.tagName || t('common.notAvailable'))
const canInstallLatest = computed(() => Boolean(resolvedInstallDirectory.value) && !launcherStore.busy)
const canApplyUpdate = computed(
  () => launcherStore.status.update.available && Boolean(launcherStore.settings.executablePath.trim()) && !launcherStore.busy,
)

watch(
  () => runtime.value?.executableDirectory,
  (value) => {
    if (!installDirectory.value.trim() && value) {
      installDirectory.value = value
    }
  },
  { immediate: true },
)

onMounted(async () => {
  try {
    await launcherStore.refresh(false)
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
})

async function saveLauncherSettings() {
  try {
    await launcherStore.saveSettings()
    ElMessage.success(t('launcher.messages.settingsSaved'))
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function startService() {
  try {
    await launcherStore.startService()
    ElMessage.success(t('launcher.messages.started'))
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function stopService() {
  try {
    await launcherStore.stopService()
    ElMessage.success(t('launcher.messages.stopped'))
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function redetectRuntime() {
  try {
    await launcherStore.refresh(false)
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function checkForUpdate() {
  try {
    await launcherStore.checkForUpdate()
    ElMessage.success(t('launcher.messages.checked'))
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function chooseExecutablePath() {
  try {
    await launcherStore.chooseExecutablePath()
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function chooseConfigPath() {
  try {
    await launcherStore.chooseConfigPath()
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function chooseInstallDirectory() {
  try {
    const result = await launcherStore.chooseInstallDirectory()
    if (result) {
      installDirectory.value = result
    }
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

function isDialogDismissed(error: unknown) {
  const action = String(error)
  return action === 'cancel' || action === 'close'
}

async function installLatest() {
  if (!resolvedInstallDirectory.value) {
    ElMessage.error(t('launcher.dialogs.installDirectoryRequired'))
    return
  }
  try {
    await launcherStore.installLatest(resolvedInstallDirectory.value)
    installDirectory.value = launcherStore.status.runtime?.executableDirectory || resolvedInstallDirectory.value
    ElMessage.success(t('launcher.messages.installed'))
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function applyUpdate() {
  if (!launcherStore.status.update.available) {
    return
  }
  try {
    await ElMessageBox.confirm(
      t('launcher.dialogs.applyUpdateMessage', { version: launcherStore.status.update.tagName || t('common.notAvailable') }),
      t('launcher.dialogs.applyUpdateTitle'),
      {
        confirmButtonText: t('launcher.applyUpdate'),
        cancelButtonText: t('launcher.dialogs.cancel'),
        customClass: 'cpa-message-box',
        type: 'warning',
      },
    )
    await launcherStore.updateCPA()
    ElMessage.success(t('launcher.messages.updated'))
  } catch (error) {
    if (!isDialogDismissed(error)) {
      ElMessage.error(toErrorMessage(error))
    }
  }
}

async function clearLogs() {
  try {
    await launcherStore.clearLogs()
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function openManagementPage() {
  try {
    await launcherStore.openManagementPage()
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function openLogsDirectory() {
  try {
    await launcherStore.openLogsDirectory()
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function openExecutableDirectory() {
  try {
    await launcherStore.openExecutableDirectory()
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function openConfigDirectory() {
  try {
    await launcherStore.openConfigDirectory()
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}
</script>

<template>
  <div class="view-shell view-shell--launcher">
    <section class="launcher-overview-grid">
      <article class="panel panel--fill launcher-status-panel">
        <div class="panel-head panel-head--tight">
          <div>
            <p class="panel-kicker">{{ t('launcher.statusSection') }}</p>
            <h3>{{ t('launcher.statusLabel') }}</h3>
          </div>
        </div>
        <div class="panel__body launcher-status-body">
          <div class="launcher-status-badge" :data-state="launcherStore.status.status">
            <strong>{{ statusLabel }}</strong>
          </div>
          <p class="muted">{{ launcherStore.status.statusDetail || t('common.notAvailable') }}</p>
          <div class="launcher-status-meta">
            <strong>{{ t('launcher.managedPid') }}</strong>
            <span>{{ launcherStore.status.managedProcessId || t('common.notAvailable') }}</span>
          </div>
        </div>
      </article>

      <article class="panel panel--fill launcher-actions-panel">
        <div class="panel-head panel-head--tight">
          <div>
            <p class="panel-kicker">{{ t('launcher.quickActions') }}</p>
            <h3>{{ t('launcher.quickActions') }}</h3>
          </div>
        </div>
        <div class="panel__body launcher-actions-body">
          <div class="launcher-action-grid">
            <el-button type="primary" :disabled="!launcherStore.canStart || launcherStore.busy" @click="startService">
              {{ t('launcher.start') }}
            </el-button>
            <el-button :disabled="!launcherStore.canStop || launcherStore.busy" @click="stopService">
              {{ t('launcher.stop') }}
            </el-button>
            <el-button plain :disabled="!runtime?.managementUrl || launcherStore.busy" @click="openManagementPage">
              {{ t('launcher.openManagement') }}
            </el-button>
            <el-button plain :disabled="!runtime?.logDirectory || launcherStore.busy" @click="openLogsDirectory">
              {{ t('launcher.openLogs') }}
            </el-button>
            <el-button plain :loading="launcherStore.loading" :disabled="launcherStore.busy" @click="redetectRuntime">
              {{ t('launcher.redetect') }}
            </el-button>
            <el-button plain :loading="launcherStore.busy" @click="checkForUpdate">
              {{ t('launcher.checkUpdate') }}
            </el-button>
          </div>

          <div class="launcher-update-strip" :data-tone="updateTone">
            <div class="launcher-update-strip__head">
              <strong>{{ t('launcher.latestUpdate') }}</strong>
              <small>{{ updateCheckedAt }}</small>
            </div>
            <p class="launcher-update-strip__summary" :title="updateSummary">{{ updateSummary }}</p>
            <div v-if="launcherStore.status.update.available" class="launcher-update-strip__actions">
              <el-button type="primary" size="small" :loading="launcherStore.busy" :disabled="!canApplyUpdate" @click="applyUpdate">
                {{ t('launcher.applyUpdate') }}
              </el-button>
            </div>
          </div>
        </div>
      </article>
    </section>

    <article class="panel panel--fill launcher-path-panel">
      <div class="panel-head panel-head--tight">
        <div>
          <p class="panel-kicker">{{ t('launcher.pathSection') }}</p>
          <h3>{{ t('launcher.savedRuntime') }}</h3>
        </div>
      </div>
      <div class="panel__body launcher-paths">
        <div class="launcher-path-row">
          <span class="launcher-path-label">{{ t('launcher.executablePath') }}</span>
          <el-input v-model="launcherStore.settings.executablePath" />
          <el-button plain @click="chooseExecutablePath">{{ t('launcher.browse') }}</el-button>
          <el-button plain :disabled="!runtime?.executableDirectory" @click="openExecutableDirectory">{{ t('launcher.openDir') }}</el-button>
        </div>

        <div class="launcher-path-row">
          <span class="launcher-path-label">{{ t('launcher.configPath') }}</span>
          <el-input v-model="launcherStore.settings.configPath" />
          <el-button plain @click="chooseConfigPath">{{ t('launcher.browse') }}</el-button>
          <el-button plain :disabled="!runtime?.configDirectory" @click="openConfigDirectory">{{ t('launcher.openDir') }}</el-button>
        </div>
      </div>
    </article>

    <article class="panel panel--fill launcher-install-panel">
      <div class="panel-head panel-head--tight">
        <div>
          <p class="panel-kicker">{{ t('launcher.installSection') }}</p>
          <h3>{{ t('launcher.installAndUpdate') }}</h3>
        </div>
      </div>
      <div class="panel__body launcher-install-body">
        <div class="launcher-path-row">
          <span class="launcher-path-label">{{ t('launcher.installDirectory') }}</span>
          <el-input v-model="installDirectory" :placeholder="runtime?.executableDirectory || t('launcher.installDirectoryPlaceholder')" />
          <el-button plain :disabled="launcherStore.busy" @click="chooseInstallDirectory">
            {{ t('launcher.chooseInstallDirectory') }}
          </el-button>
          <el-button type="primary" :loading="launcherStore.busy" :disabled="!canInstallLatest" @click="installLatest">
            {{ t('launcher.installLatest') }}
          </el-button>
        </div>

        <div class="launcher-install-meta">
          <div class="launcher-detail-item">
            <strong>{{ t('launcher.currentVersion') }}</strong>
            <span>{{ currentVersion }}</span>
          </div>
          <div class="launcher-detail-item">
            <strong>{{ t('launcher.latestVersion') }}</strong>
            <span>{{ latestVersion }}</span>
          </div>
        </div>
      </div>
    </article>

    <section class="launcher-main-grid">
      <article class="panel panel--fill launcher-runtime-panel">
        <div class="panel-head panel-head--tight">
          <div>
            <p class="panel-kicker">{{ t('launcher.detailSection') }}</p>
            <h3>{{ t('launcher.runtimeDetails') }}</h3>
          </div>
        </div>
        <div class="panel__body launcher-details-grid">
          <div class="launcher-detail-item">
            <strong>{{ t('launcher.baseUrl') }}</strong>
            <span>{{ runtime?.baseUrl || t('common.notAvailable') }}</span>
          </div>
          <div class="launcher-detail-item">
            <strong>{{ t('launcher.managementUrl') }}</strong>
            <span>{{ runtime?.managementUrl || t('common.notAvailable') }}</span>
          </div>
          <div class="launcher-detail-item">
            <strong>{{ t('launcher.configDirectory') }}</strong>
            <span>{{ runtime?.configDirectory || t('common.notAvailable') }}</span>
          </div>
          <div class="launcher-detail-item">
            <strong>{{ t('launcher.logDirectory') }}</strong>
            <span>{{ runtime?.logDirectory || t('common.notAvailable') }}</span>
          </div>
          <div class="launcher-detail-item">
            <strong>{{ t('launcher.authDirectory') }}</strong>
            <span>{{ runtime?.authDirectory || t('common.notAvailable') }}</span>
          </div>
          <div class="launcher-detail-item">
            <strong>{{ t('launcher.managementSecretConfigured') }}</strong>
            <span>{{ runtime?.managementSecretConfigured ? t('common.yes') : t('common.no') }}</span>
          </div>
        </div>
      </article>

      <article class="panel panel--fill launcher-options-panel">
        <div class="panel-head panel-head--tight">
          <div>
            <p class="panel-kicker">{{ t('launcher.optionsSection') }}</p>
            <h3>{{ t('launcher.optionsSection') }}</h3>
          </div>
          <el-button type="primary" :loading="launcherStore.busy" @click="saveLauncherSettings">
            {{ t('launcher.saveSettings') }}
          </el-button>
        </div>
        <div class="panel__body launcher-options-body">
          <el-form class="launcher-options-form">
            <div class="launcher-option-grid">
              <div class="launcher-option-item">
                <span class="launcher-option-label">{{ t('launcher.autoStartService') }}</span>
                <el-switch v-model="launcherStore.settings.autoStartService" />
              </div>
              <div class="launcher-option-item">
                <span class="launcher-option-label">{{ t('launcher.autoStartDelaySeconds') }}</span>
                <el-input-number v-model="launcherStore.settings.autoStartDelaySeconds" :min="0" :max="3600" />
              </div>
            </div>

            <div class="launcher-switch-list">
              <div class="launcher-switch-item">
                <span class="launcher-option-label">{{ t('launcher.launchOnWindowsStartup') }}</span>
                <el-switch v-model="launcherStore.settings.launchOnWindowsStartup" />
              </div>
              <div class="launcher-switch-item">
                <span class="launcher-option-label">{{ t('launcher.minimizeToTrayOnClose') }}</span>
                <el-switch v-model="launcherStore.settings.minimizeToTrayOnClose" />
              </div>
              <div class="launcher-switch-item">
                <span class="launcher-option-label">{{ t('launcher.openManagementPageAfterStart') }}</span>
                <el-switch v-model="launcherStore.settings.openManagementPageAfterStart" />
              </div>
              <div class="launcher-switch-item">
                <span class="launcher-option-label">{{ t('launcher.checkForUpdatesOnStartup') }}</span>
                <el-switch v-model="launcherStore.settings.checkForUpdatesOnStartup" />
              </div>
            </div>
          </el-form>
        </div>
      </article>
    </section>

    <article class="panel panel--fill launcher-log-panel">
      <div class="panel-head panel-head--tight">
        <div>
          <p class="panel-kicker">{{ t('launcher.logSection') }}</p>
          <h3>{{ t('launcher.launcherLogs') }}</h3>
        </div>
        <el-button text @click="clearLogs">{{ t('launcher.clearLogs') }}</el-button>
      </div>
      <div class="panel__body launcher-log-body">
        <LogConsole :entries="launcherStore.logs" />
      </div>
    </article>
  </div>
</template>

<style scoped>
.view-shell--launcher {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 0.95rem;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  padding-right: 0.25rem;
  overscroll-behavior: contain;
  scrollbar-gutter: stable both-edges;
}

.launcher-overview-grid {
  display: grid;
  gap: 0.9rem;
  grid-template-columns: minmax(250px, 0.85fr) minmax(0, 1.45fr);
  flex: 0 0 auto;
}

.launcher-main-grid {
  display: grid;
  gap: 0.9rem;
  grid-template-columns: minmax(0, 1.2fr) minmax(320px, 0.8fr);
  align-items: stretch;
  min-height: 0;
  flex: 0 0 auto;
}

.launcher-path-panel {
  flex: 0 0 auto;
}

.launcher-install-panel {
  flex: 0 0 auto;
}

.launcher-status-body,
.launcher-actions-body,
.launcher-options-form,
.launcher-install-body {
  display: grid;
  gap: 0.8rem;
  min-height: 0;
}

.launcher-status-badge {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 76px;
  border-radius: 20px;
  background: rgba(205, 206, 202, 0.42);
  color: #2f382f;
}

.launcher-status-badge strong {
  font-size: 1.9rem;
  line-height: 1;
}

.launcher-status-badge[data-state="running"] {
  background: linear-gradient(180deg, rgba(192, 237, 214, 0.96), rgba(172, 224, 198, 0.96));
  color: #185f4a;
}

.launcher-status-badge[data-state="starting"],
.launcher-status-badge[data-state="stopping"] {
  background: linear-gradient(180deg, rgba(245, 232, 190, 0.95), rgba(238, 222, 168, 0.95));
  color: #6f5a18;
}

.launcher-status-badge[data-state="start_failed"],
.launcher-status-badge[data-state="external"] {
  background: linear-gradient(180deg, rgba(247, 212, 208, 0.96), rgba(240, 196, 191, 0.96));
  color: #8c3d2f;
}

.launcher-status-meta {
  display: grid;
  gap: 0.16rem;
}

.launcher-status-meta strong,
.launcher-update-strip__head strong {
  font-size: 0.82rem;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: rgba(69, 60, 42, 0.65);
}

.launcher-action-grid {
  display: grid;
  gap: 0.7rem;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  grid-auto-rows: minmax(44px, 1fr);
  align-items: stretch;
}

.launcher-action-grid :deep(.el-button) {
  width: 100%;
  min-height: 44px;
  box-sizing: border-box;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  white-space: nowrap;
}

.launcher-action-grid :deep(.el-button + .el-button) {
  margin-left: 0;
}

.launcher-update-strip {
  position: relative;
  display: grid;
  gap: 0.5rem;
  padding: 0.88rem 0.95rem;
  border-radius: 20px;
  border: 1px solid rgba(69, 60, 42, 0.08);
  background:
    linear-gradient(180deg, rgba(255, 251, 244, 0.94), rgba(238, 229, 213, 0.92));
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.68);
}

.launcher-update-strip::before {
  content: '';
  position: absolute;
  top: 0.9rem;
  bottom: 0.9rem;
  left: 0.92rem;
  width: 4px;
  border-radius: 999px;
  background: linear-gradient(180deg, rgba(24, 95, 74, 0.8), rgba(24, 95, 74, 0.18));
}

.launcher-update-strip[data-tone='available'] {
  background:
    linear-gradient(180deg, rgba(243, 251, 246, 0.96), rgba(226, 241, 233, 0.95));
}

.launcher-update-strip[data-tone='error'] {
  background:
    linear-gradient(180deg, rgba(255, 247, 241, 0.96), rgba(247, 229, 219, 0.95));
}

.launcher-update-strip[data-tone='error']::before {
  background: linear-gradient(180deg, rgba(180, 83, 9, 0.86), rgba(180, 83, 9, 0.22));
}

.launcher-update-strip__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 0.75rem;
  padding-left: 0.88rem;
}

.launcher-update-strip__head small {
  flex-shrink: 0;
  margin-top: 0.02rem;
  color: rgba(69, 60, 42, 0.62);
}

.launcher-update-strip__summary {
  margin: 0;
  padding-left: 0.88rem;
  font-weight: 700;
  color: #3d3526;
  line-height: 1.45;
  overflow-wrap: anywhere;
}

.launcher-update-strip[data-tone='error'] .launcher-update-strip__summary {
  color: #7a4321;
}

.launcher-update-strip[data-tone='available'] .launcher-update-strip__summary {
  color: #1c5c48;
}

.launcher-update-strip__actions {
  display: flex;
  justify-content: flex-end;
  padding-left: 0.88rem;
}

.launcher-paths,
.launcher-details-grid {
  display: grid;
  gap: 0.7rem;
}

.launcher-install-meta {
  display: grid;
  gap: 0.52rem 0.7rem;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.launcher-path-row {
  display: grid;
  gap: 0.6rem;
  grid-template-columns: minmax(128px, 168px) minmax(0, 1fr) auto auto;
  align-items: center;
}

.launcher-path-label {
  font-size: 0.9rem;
  font-weight: 800;
  color: rgba(69, 60, 42, 0.88);
}

.launcher-details-grid {
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.52rem 0.7rem;
}

.launcher-runtime-panel {
  height: 100%;
  padding-top: 0.9rem;
  padding-bottom: 0.86rem;
}

.launcher-options-panel {
  height: 100%;
}

.launcher-runtime-panel .panel-head--tight {
  margin-bottom: 0.72rem;
}

.launcher-detail-item {
  display: grid;
  gap: 0.12rem;
  min-width: 0;
}

.launcher-detail-item strong {
  color: rgba(69, 60, 42, 0.7);
  font-size: 0.76rem;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.launcher-detail-item span {
  color: rgba(69, 60, 42, 0.9);
  font-weight: 700;
  line-height: 1.26;
  word-break: break-word;
}

.launcher-option-grid {
  display: grid;
  gap: 0.8rem;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.launcher-option-item,
.launcher-switch-list {
  display: grid;
}

.launcher-option-item,
.launcher-switch-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.9rem;
  min-width: 0;
}

.launcher-option-item {
  min-height: 54px;
}

.launcher-option-label {
  color: rgba(69, 60, 42, 0.9);
  font-weight: 700;
  line-height: 1.3;
}

.launcher-option-item :deep(.el-input-number) {
  width: min(220px, 100%);
}

.launcher-switch-list {
  display: grid;
  gap: 0.7rem;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  align-items: stretch;
}

.launcher-switch-item {
  min-height: 40px;
  padding: 0.1rem 0;
}

.launcher-option-item :deep(.el-switch),
.launcher-switch-item :deep(.el-switch) {
  flex-shrink: 0;
}

.launcher-log-panel {
  min-height: 0;
  flex: 0 0 auto;
}

.launcher-log-body {
  display: flex;
  flex: 1;
  min-height: 260px;
  overflow: hidden;
}

.launcher-log-body :deep(.log-console) {
  flex: 1;
  min-height: 0;
  height: auto;
}

@media (max-width: 1380px) {
  .launcher-main-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 1080px) {
  .launcher-overview-grid,
  .launcher-details-grid,
  .launcher-option-grid,
  .launcher-switch-list,
  .launcher-install-meta {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 920px) {
  .launcher-action-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .launcher-path-row {
    grid-template-columns: 1fr;
    align-items: stretch;
  }
}

@media (max-width: 620px) {
  .launcher-action-grid {
    grid-template-columns: 1fr;
  }

  .launcher-update-strip__head {
    flex-direction: column;
    gap: 0.28rem;
  }
  .launcher-status-badge strong {
    font-size: 1.58rem;
  }
}
</style>
