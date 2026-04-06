<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElButton, ElInput, ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { useCodexLocalConfigStore } from '@/stores/codexLocalConfig'
import { formatDateTime } from '@/utils/format'
import { toErrorMessage } from '@/utils/errors'

const { t } = useI18n()
const codexLocalConfigStore = useCodexLocalConfigStore()
const codexImportName = ref('')
const newCodexProfileName = ref('')
const newCodexConfigToml = ref('')
const newCodexAuthJson = ref('')
const createEditorOpen = ref(false)

const codexProfiles = computed(() => codexLocalConfigStore.snapshot.profiles)
const codexProfileContent = computed(() => codexLocalConfigStore.profileContent)
const orderedCodexProfiles = computed(() => {
  return [...codexProfiles.value].sort((left, right) => {
    const leftActive = left.name === codexLocalConfigStore.activeProfile
    const rightActive = right.name === codexLocalConfigStore.activeProfile
    if (leftActive !== rightActive) {
      return leftActive ? -1 : 1
    }
    return left.name.localeCompare(right.name, undefined, { sensitivity: 'base' })
  })
})
const lastCodexBackup = computed(() => (
  Array.isArray(codexLocalConfigStore.snapshot.backups) && codexLocalConfigStore.snapshot.backups.length > 0
    ? codexLocalConfigStore.snapshot.backups[0]
    : null
))
const codexContentBusy = computed(() => (
  codexLocalConfigStore.busy ||
  codexLocalConfigStore.contentLoading ||
  codexLocalConfigStore.contentSaving ||
  codexLocalConfigStore.contentTesting ||
  Boolean(codexLocalConfigStore.connectionTestingName)
))

onMounted(async () => {
  try {
    await codexLocalConfigStore.loadSnapshot()
    if (codexLocalConfigStore.activeProfile) {
      await codexLocalConfigStore.selectProfile(codexLocalConfigStore.activeProfile)
    }
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
})

function isDialogDismissed(error: unknown) {
  const action = String(error)
  return action === 'cancel' || action === 'close'
}

async function openCodexLocalDirectory() {
  try {
    await codexLocalConfigStore.openDirectory()
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function importCurrentCodexProfile() {
  if (!codexImportName.value.trim()) {
    ElMessage.error(t('launcher.codexLocal.errors.nameRequired'))
    return
  }
  try {
    await codexLocalConfigStore.importCurrent(codexImportName.value)
    codexImportName.value = ''
    ElMessage.success(t('launcher.codexLocal.messages.imported'))
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function validateCodexProfileBeforeSave(name: string, configToml: string, authJson: string) {
  const result = await codexLocalConfigStore.testProfileContent(name, configToml, authJson)
  if (!result.ok) {
    ElMessage.error(result.message)
    return false
  }
  return true
}

async function createCodexProfile() {
  if (!newCodexProfileName.value.trim()) {
    ElMessage.error(t('launcher.codexLocal.errors.nameRequired'))
    return
  }
  if (codexProfiles.value.some((profile) => profile.name.trim().toLowerCase() === newCodexProfileName.value.trim().toLowerCase())) {
    ElMessage.error(t('launcher.codexLocal.errors.nameExists'))
    return
  }
  try {
    const valid = await validateCodexProfileBeforeSave(
      newCodexProfileName.value,
      newCodexConfigToml.value,
      newCodexAuthJson.value,
    )
    if (!valid) {
      return
    }
    await codexLocalConfigStore.createProfileContent(
      newCodexProfileName.value,
      newCodexConfigToml.value,
      newCodexAuthJson.value,
    )
    newCodexProfileName.value = ''
    newCodexConfigToml.value = ''
    newCodexAuthJson.value = ''
    createEditorOpen.value = false
    ElMessage.success(t('launcher.codexLocal.messages.created'))
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

function toggleCreateEditor() {
  createEditorOpen.value = !createEditorOpen.value
}

async function selectCodexProfile(name: string) {
  try {
    await codexLocalConfigStore.selectProfile(name)
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function reloadSelectedCodexProfile() {
  if (!codexLocalConfigStore.selectedProfile) {
    return
  }
  try {
    await codexLocalConfigStore.loadProfileContent(codexLocalConfigStore.selectedProfile)
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function saveSelectedCodexProfile() {
  if (!codexProfileContent.value) {
    return
  }
  try {
    const valid = await validateCodexProfileBeforeSave(
      codexProfileContent.value.name,
      codexProfileContent.value.configToml,
      codexProfileContent.value.authJson,
    )
    if (!valid) {
      return
    }
    const result = await codexLocalConfigStore.saveProfileContent()
    if (!result) {
      return
    }
    ElMessage.success(t('launcher.codexLocal.messages.saved'))
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function switchCodexProfile(name: string) {
  try {
    await codexLocalConfigStore.switchProfile(name)
    ElMessage.success(t('launcher.codexLocal.messages.switched'))
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function deleteCodexProfile(name: string) {
  try {
    await ElMessageBox.confirm(
      t('launcher.codexLocal.dialogs.deleteMessage', { name }),
      t('launcher.codexLocal.dialogs.deleteTitle'),
      {
        confirmButtonText: t('launcher.codexLocal.deleteProfile'),
        cancelButtonText: t('launcher.dialogs.cancel'),
        customClass: 'cpa-message-box',
        type: 'warning',
      },
    )
    await codexLocalConfigStore.deleteProfile(name)
    ElMessage.success(t('launcher.codexLocal.messages.deleted'))
  } catch (error) {
    if (!isDialogDismissed(error)) {
      ElMessage.error(toErrorMessage(error))
    }
  }
}

async function testCodexProfileConnection(name: string) {
  try {
    const result = await codexLocalConfigStore.testSavedProfileConnection(name)
    if (result.ok) {
      ElMessage.success(result.message)
      return
    }
    ElMessage.error(result.message)
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}
</script>

<template>
  <div class="view-shell view-shell--codex-configs">
    <article class="panel panel--fill codex-config-panel">
      <div class="panel-head panel-head--tight">
        <div>
          <p class="panel-kicker">{{ t('launcher.codexLocal.section') }}</p>
          <h3>{{ t('launcher.codexLocal.title') }}</h3>
        </div>
        <el-button plain :disabled="codexContentBusy" @click="openCodexLocalDirectory">
          {{ t('launcher.codexLocal.openDirectory') }}
        </el-button>
      </div>
      <div class="panel__body codex-config-body">
        <p class="muted">{{ t('launcher.codexLocal.lead') }}</p>

        <div class="codex-config-summary-grid">
          <div class="codex-config-detail-item">
            <strong>{{ t('launcher.codexLocal.defaultDirectory') }}</strong>
            <span>{{ codexLocalConfigStore.snapshot.defaultDirectory || t('common.notAvailable') }}</span>
          </div>
          <div class="codex-config-detail-item">
            <strong>{{ t('launcher.codexLocal.activeProfile') }}</strong>
            <span>{{ codexLocalConfigStore.snapshot.activeProfileName || t('common.notAvailable') }}</span>
          </div>
          <div class="codex-config-detail-item">
            <strong>{{ t('launcher.codexLocal.lastBackup') }}</strong>
            <span>{{ lastCodexBackup ? formatDateTime(lastCodexBackup.createdAt) : t('common.notAvailable') }}</span>
          </div>
        </div>

        <div class="codex-config-list-head">
          <strong>{{ t('launcher.codexLocal.currentFiles') }}</strong>
          <span class="muted">{{ t('launcher.codexLocal.importLabel') }}</span>
        </div>
        <div class="codex-config-file-list">
          <div class="codex-config-file-pill" :data-ready="codexLocalConfigStore.snapshot.currentConfigExists">
            <strong>{{ t('launcher.codexLocal.configToml') }}</strong>
            <span>{{ codexLocalConfigStore.snapshot.currentConfigExists ? t('launcher.codexLocal.ready') : t('launcher.codexLocal.missing') }}</span>
          </div>
          <div class="codex-config-file-pill" :data-ready="codexLocalConfigStore.snapshot.currentAuthExists">
            <strong>{{ t('launcher.codexLocal.authJson') }}</strong>
            <span>{{ codexLocalConfigStore.snapshot.currentAuthExists ? t('launcher.codexLocal.ready') : t('launcher.codexLocal.missing') }}</span>
          </div>
        </div>
        <div class="codex-config-import">
          <el-input
            v-model="codexImportName"
            :placeholder="t('launcher.codexLocal.importPlaceholder')"
            :disabled="codexContentBusy"
          />
          <el-button
            type="primary"
            :disabled="codexContentBusy || !codexLocalConfigStore.snapshot.currentConfigExists || !codexLocalConfigStore.snapshot.currentAuthExists"
            @click="importCurrentCodexProfile"
          >
            {{ t('launcher.codexLocal.importCurrent') }}
          </el-button>
        </div>

        <div class="codex-config-editor codex-config-editor--create">
          <div class="codex-config-list-head">
            <div class="codex-config-editor__head">
              <strong>{{ t('launcher.codexLocal.createTitle') }}</strong>
              <span class="muted">{{ t('launcher.codexLocal.createHint') }}</span>
            </div>
            <div class="codex-config-editor__actions">
              <el-button plain :disabled="codexContentBusy" @click="toggleCreateEditor">
                {{ createEditorOpen ? t('launcher.codexLocal.createCollapse') : t('launcher.codexLocal.createExpand') }}
              </el-button>
              <el-button
                v-if="createEditorOpen"
                type="primary"
                :loading="codexLocalConfigStore.contentSaving"
                :disabled="codexContentBusy"
                @click="createCodexProfile"
              >
                {{ t('launcher.codexLocal.createProfile') }}
              </el-button>
            </div>
          </div>
          <template v-if="createEditorOpen">
            <el-input
              v-model="newCodexProfileName"
              :placeholder="t('launcher.codexLocal.createNamePlaceholder')"
              :disabled="codexContentBusy"
            />
            <div class="codex-config-editor__grid">
              <div class="codex-config-editor__field">
                <span class="codex-config-label">{{ t('launcher.codexLocal.configToml') }}</span>
                <el-input
                  v-model="newCodexConfigToml"
                  type="textarea"
                  :rows="14"
                  resize="vertical"
                  :disabled="codexContentBusy"
                />
              </div>
              <div class="codex-config-editor__field">
                <span class="codex-config-label">{{ t('launcher.codexLocal.authJson') }}</span>
                <el-input
                  v-model="newCodexAuthJson"
                  type="textarea"
                  :rows="14"
                  resize="vertical"
                  :disabled="codexContentBusy"
                />
              </div>
            </div>
          </template>
        </div>

        <div class="codex-config-list-head">
          <strong>{{ t('launcher.codexLocal.profiles') }}</strong>
          <span class="muted">{{ t('launcher.codexLocal.backupCount', { count: codexLocalConfigStore.snapshot.backups.length }) }}</span>
        </div>

        <div v-if="orderedCodexProfiles.length > 0" class="codex-config-profile-list">
          <article
            v-for="profile in orderedCodexProfiles"
            :key="profile.name"
            class="codex-config-profile codex-config-profile--row"
            :class="{
              'codex-config-profile--active': profile.name === codexLocalConfigStore.activeProfile,
              'codex-config-profile--selected': profile.name === codexLocalConfigStore.selectedProfile,
            }"
          >
            <div class="codex-config-profile__main">
            <div class="codex-config-profile__head">
              <div class="codex-config-profile__title">
                <strong>{{ profile.name }}</strong>
                <span class="muted">{{ profile.name === codexLocalConfigStore.activeProfile ? t('launcher.codexLocal.current') : t('common.notAvailable') }}</span>
              </div>
            </div>
              <div class="codex-config-profile__meta muted">
                <span>{{ t('launcher.codexLocal.importedAt') }}: {{ formatDateTime(profile.createdAt) }}</span>
                <span>{{ t('launcher.codexLocal.activatedAt') }}: {{ profile.lastActivatedAt ? formatDateTime(profile.lastActivatedAt) : t('common.notAvailable') }}</span>
                <span>{{ t('launcher.codexLocal.updatedAt') }}: {{ profile.updatedAt ? formatDateTime(profile.updatedAt) : t('common.notAvailable') }}</span>
              </div>
              <span
                v-if="codexLocalConfigStore.lastConnectionResults[profile.name]"
                :data-ok="codexLocalConfigStore.lastConnectionResults[profile.name].ok"
                class="codex-config-profile__test"
              >
                {{ codexLocalConfigStore.lastConnectionResults[profile.name].message }}
              </span>
            </div>
            <div class="codex-config-profile__actions codex-config-profile__actions--row">
              <el-button plain :disabled="codexContentBusy" @click="selectCodexProfile(profile.name)">
                {{ t('launcher.codexLocal.editProfile') }}
              </el-button>
              <el-button
                plain
                :loading="codexLocalConfigStore.connectionTestingName === profile.name"
                :disabled="codexContentBusy && codexLocalConfigStore.connectionTestingName !== profile.name"
                @click="testCodexProfileConnection(profile.name)"
              >
                {{ t('launcher.codexLocal.testConnection') }}
              </el-button>
              <el-button
                plain
                :disabled="profile.name === codexLocalConfigStore.activeProfile || codexContentBusy || !profile.hasConfigToml || !profile.hasAuthJson"
                @click="switchCodexProfile(profile.name)"
              >
                {{ profile.name === codexLocalConfigStore.activeProfile ? t('launcher.codexLocal.current') : t('launcher.codexLocal.switchTo') }}
              </el-button>
              <el-button
                text
                :disabled="profile.name === codexLocalConfigStore.activeProfile || codexContentBusy"
                @click="deleteCodexProfile(profile.name)"
              >
                {{ t('launcher.codexLocal.deleteProfile') }}
              </el-button>
            </div>
          </article>
        </div>
        <div v-else class="codex-config-empty">
          <strong>{{ t('launcher.codexLocal.emptyTitle') }}</strong>
          <p class="muted">{{ t('launcher.codexLocal.emptyBody') }}</p>
        </div>

        <div class="codex-config-editor">
          <div class="codex-config-list-head">
            <div class="codex-config-editor__head">
              <strong>{{ t('launcher.codexLocal.editorTitle') }}</strong>
              <span class="muted">{{ codexProfileContent?.name || t('launcher.codexLocal.noSelection') }}</span>
            </div>
            <div class="codex-config-editor__actions">
              <el-button plain :disabled="!codexLocalConfigStore.selectedProfile || codexContentBusy" @click="reloadSelectedCodexProfile">
                {{ t('launcher.codexLocal.reloadContent') }}
              </el-button>
              <el-button type="primary" :loading="codexLocalConfigStore.contentSaving" :disabled="!codexProfileContent || codexContentBusy && !codexLocalConfigStore.contentSaving" @click="saveSelectedCodexProfile">
                {{ t('launcher.codexLocal.saveContent') }}
              </el-button>
            </div>
          </div>
          <p class="muted">{{ t('launcher.codexLocal.editorHint') }}</p>
          <div v-if="codexProfileContent" class="codex-config-editor__grid">
            <div class="codex-config-editor__field">
              <span class="codex-config-label">{{ t('launcher.codexLocal.configToml') }}</span>
              <el-input
                v-model="codexProfileContent.configToml"
                type="textarea"
                :rows="14"
                resize="vertical"
                :disabled="codexLocalConfigStore.contentLoading || codexLocalConfigStore.contentSaving"
              />
            </div>
            <div class="codex-config-editor__field">
              <span class="codex-config-label">{{ t('launcher.codexLocal.authJson') }}</span>
              <el-input
                v-model="codexProfileContent.authJson"
                type="textarea"
                :rows="14"
                resize="vertical"
                :disabled="codexLocalConfigStore.contentLoading || codexLocalConfigStore.contentSaving"
              />
            </div>
          </div>
          <div v-else class="codex-config-empty">
            <strong>{{ t('launcher.codexLocal.noSelection') }}</strong>
          </div>
        </div>
      </div>
    </article>
  </div>
</template>

<style scoped>
.view-shell--codex-configs {
  display: flex;
  flex-direction: column;
  gap: 0.95rem;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  padding-right: 0.25rem;
  overscroll-behavior: contain;
  scrollbar-gutter: stable both-edges;
}

.codex-config-panel {
  flex: 0 0 auto;
}

.codex-config-body {
  display: grid;
  gap: 0.85rem;
}

.codex-config-summary-grid {
  display: grid;
  gap: 0.52rem 0.7rem;
  grid-template-columns: repeat(4, minmax(0, 1fr));
}

.codex-config-detail-item {
  display: grid;
  gap: 0.12rem;
  min-width: 0;
}

.codex-config-detail-item strong {
  color: rgba(69, 60, 42, 0.7);
  font-size: 0.76rem;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.codex-config-detail-item span {
  color: rgba(69, 60, 42, 0.9);
  font-weight: 700;
  line-height: 1.26;
  word-break: break-word;
}

.codex-config-file-list {
  display: flex;
  flex-wrap: wrap;
  gap: 0.7rem;
}

.codex-config-file-pill {
  display: inline-flex;
  align-items: center;
  gap: 0.7rem;
  padding: 0.65rem 0.85rem;
  border-radius: 16px;
  background: rgba(247, 241, 231, 0.78);
  border: 1px solid rgba(69, 60, 42, 0.08);
}

.codex-config-file-pill strong,
.codex-config-list-head strong,
.codex-config-profile__title strong {
  color: rgba(69, 60, 42, 0.9);
}

.codex-config-file-pill span {
  font-size: 0.85rem;
  font-weight: 700;
  color: #8c3d2f;
}

.codex-config-file-pill[data-ready='true'] span {
  color: #185f4a;
}

.codex-config-import {
  display: grid;
  gap: 0.7rem;
  grid-template-columns: minmax(0, 1fr) auto;
}

.codex-config-list-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.8rem;
}

.codex-config-profile-list {
  display: grid;
  gap: 0.8rem;
  grid-template-columns: 1fr;
}

.codex-config-profile {
  display: grid;
  gap: 0.7rem;
  padding: 0.95rem 1rem;
  border-radius: 20px;
  border: 1px solid rgba(69, 60, 42, 0.08);
  background: linear-gradient(180deg, rgba(255, 250, 242, 0.95), rgba(243, 235, 221, 0.95));
}

.codex-config-profile--row {
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 0.9rem 1rem;
}

.codex-config-profile--active {
  border-color: rgba(24, 95, 74, 0.25);
  box-shadow: inset 0 0 0 1px rgba(24, 95, 74, 0.08);
}

.codex-config-profile--selected {
  background: linear-gradient(180deg, rgba(245, 249, 246, 0.98), rgba(235, 242, 237, 0.98));
}

.codex-config-profile__main {
  display: grid;
  gap: 0.45rem;
  min-width: 0;
}

.codex-config-profile__head,
.codex-config-profile__actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.8rem;
}

.codex-config-profile__title {
  display: grid;
  gap: 0.16rem;
  min-width: 0;
}

.codex-config-profile__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 0.28rem 1rem;
}

.codex-config-profile__test {
  font-weight: 700;
  color: #8c3d2f;
}

.codex-config-profile__test[data-ok='true'] {
  color: #185f4a;
}

.codex-config-profile__actions--row {
  display: grid;
  grid-template-columns: 88px 110px 136px 72px;
  justify-content: end;
  align-items: center;
  gap: 0.65rem;
}

.codex-config-profile__actions--row :deep(.el-button) {
  width: 100%;
  margin-left: 0;
}

.codex-config-empty {
  display: grid;
  gap: 0.3rem;
  padding: 1rem;
  border-radius: 18px;
  background: rgba(247, 241, 231, 0.6);
}

.codex-config-editor {
  display: grid;
  gap: 0.75rem;
  padding: 1rem;
  border-radius: 20px;
  border: 1px solid rgba(69, 60, 42, 0.08);
  background: rgba(255, 252, 246, 0.82);
}

.codex-config-editor__head {
  display: grid;
  gap: 0.18rem;
}

.codex-config-editor__actions {
  display: flex;
  gap: 0.6rem;
}

.codex-config-editor__grid {
  display: grid;
  gap: 0.8rem;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.codex-config-editor__field {
  display: grid;
  gap: 0.45rem;
}

.codex-config-label {
  color: rgba(69, 60, 42, 0.9);
  font-weight: 700;
  line-height: 1.3;
}

.codex-config-editor__field :deep(textarea) {
  min-height: 320px;
  font-family: Consolas, 'SFMono-Regular', Menlo, Monaco, monospace;
  font-size: 0.86rem;
  line-height: 1.45;
}

@media (max-width: 1080px) {
  .codex-config-summary-grid,
  .codex-config-editor__grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 920px) {
  .codex-config-import {
    grid-template-columns: 1fr;
  }

  .codex-config-profile__actions,
  .codex-config-list-head {
    flex-wrap: wrap;
  }

  .codex-config-profile--row {
    grid-template-columns: 1fr;
    align-items: stretch;
  }

  .codex-config-profile__actions--row {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-start;
  }
}
</style>
