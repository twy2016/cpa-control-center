<script setup lang="ts">
import { computed, inject, onMounted, ref, watch } from 'vue'
import {
  ElButton,
  ElDrawer,
  ElMessage,
  ElOption,
  ElPagination,
  ElSelect,
  ElTable,
  ElTableColumn,
} from 'element-plus'
import { useI18n } from 'vue-i18n'
import StatusPill from '@/components/StatusPill.vue'
import { useAccountsStore } from '@/stores/accounts'
import { formatDateTime } from '@/utils/format'
import { toErrorMessage } from '@/utils/errors'
import { resolveDashboardDrawerSize, shellModeKey, type ShellMode } from '@/layout/shell'
import { taskStatusLabel } from '@/utils/status'

const { t } = useI18n()
const accountsStore = useAccountsStore()
const injectedShellMode = inject(shellModeKey, null)

const statusFilter = ref('all')
const comparisonRunId = ref<number | null>(null)
const drawerOpen = ref(false)
const detailLoading = ref(false)
const detailRunId = ref<number | null>(null)
const detailPage = ref(1)
const detailPageSize = ref(20)
const detailPageSizes = [20, 50, 100]

const shellMode = computed<ShellMode>(() => injectedShellMode?.value ?? 'desktop')
const drawerSize = computed(() => resolveDashboardDrawerSize(shellMode.value))
const historyRows = computed(() => {
  const items = accountsStore.historyArchive
  if (statusFilter.value === 'all') {
    return items
  }
  return items.filter((item) => item.status === statusFilter.value)
})
const latestRun = computed(() => accountsStore.historyArchive[0] ?? null)
const comparisonRun = computed(() => (
  accountsStore.historyArchive.find((item) => item.runId === comparisonRunId.value) ??
  accountsStore.historyArchive[0] ??
  null
))
const comparisonBaseRun = computed(() => {
  if (!comparisonRun.value) {
    return null
  }
  const index = accountsStore.historyArchive.findIndex((item) => item.runId === comparisonRun.value?.runId)
  if (index < 0) {
    return null
  }
  return accountsStore.historyArchive[index + 1] ?? null
})
const recentWindow = computed(() => accountsStore.historyArchive.slice(0, 10))
const recentSuccessRate = computed(() => {
  const total = recentWindow.value.length
  if (total <= 0) {
    return 0
  }
  const successCount = recentWindow.value.filter((item) => item.status === 'success').length
  return Math.round((successCount / total) * 100)
})

const scanDetailSummary = computed(() => accountsStore.scanDetail?.summary ?? null)
const scanDetailRecords = computed(() => accountsStore.scanDetail?.records ?? [])
const scanDetailTotal = computed(() => accountsStore.scanDetail?.totalRecords ?? 0)
const scanDetailStatusState = computed(() => taskStatusState(scanDetailSummary.value?.status || 'running'))

const comparisonCards = computed(() => {
  const current = comparisonRun.value
  const previous = comparisonBaseRun.value
  if (!current) {
    return []
  }

  return [
    {
      key: 'invalid401',
      label: t('historyView.cards.invalid401'),
      current: current.invalid401Count,
      previous: previous?.invalid401Count ?? 0,
    },
    {
      key: 'quotaLimited',
      label: t('historyView.cards.quotaLimited'),
      current: current.quotaLimitedCount,
      previous: previous?.quotaLimitedCount ?? 0,
    },
    {
      key: 'recovered',
      label: t('historyView.cards.recovered'),
      current: current.recoveredCount,
      previous: previous?.recoveredCount ?? 0,
    },
    {
      key: 'error',
      label: t('historyView.cards.error'),
      current: current.errorCount,
      previous: previous?.errorCount ?? 0,
    },
  ]
})

function taskStatusState(status: string) {
  switch (status.toLowerCase()) {
    case 'success':
      return 'normal'
    case 'failed':
    case 'cancelled':
      return 'error'
    case 'skipped':
      return 'quota_limited'
    default:
      return 'pending'
  }
}

function deltaLabel(current: number, previous: number) {
  const delta = current - previous
  if (delta === 0) {
    return t('historyView.comparison.flat')
  }
  return delta > 0
    ? t('historyView.comparison.up', { value: delta })
    : t('historyView.comparison.down', { value: Math.abs(delta) })
}

async function loadHistory() {
  try {
    await accountsStore.loadHistory(60)
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

async function loadScanDetailPage(page = detailPage.value, pageSize = detailPageSize.value) {
  if (detailRunId.value === null) {
    return
  }
  detailLoading.value = true
  try {
    const detail = await accountsStore.loadScanDetail(detailRunId.value, page, pageSize)
    detailPage.value = detail.page
    detailPageSize.value = detail.pageSize
  } finally {
    detailLoading.value = false
  }
}

async function openDetail(runId: number) {
  detailRunId.value = runId
  detailPage.value = 1
  try {
    await loadScanDetailPage(1, detailPageSize.value)
    drawerOpen.value = true
  } catch (error) {
    ElMessage.error(toErrorMessage(error))
  }
}

function changeDetailPage(page: number) {
  detailPage.value = page
  void loadScanDetailPage(page, detailPageSize.value)
}

function changeDetailPageSize(pageSize: number) {
  detailPageSize.value = pageSize
  detailPage.value = 1
  void loadScanDetailPage(1, pageSize)
}

watch(
  () => accountsStore.historyArchive,
  (history) => {
    if (!history.length) {
      comparisonRunId.value = null
      return
    }
    if (!history.some((item) => item.runId === comparisonRunId.value)) {
      comparisonRunId.value = history[0].runId
    }
  },
  { immediate: true },
)

onMounted(() => {
  void loadHistory()
})
</script>

<template>
  <div class="view-shell view-shell--history-page">
    <section class="hero-panel history-hero">
      <div>
        <p class="eyebrow">{{ t('historyView.eyebrow') }}</p>
        <h2>{{ t('historyView.title') }}</h2>
        <p class="lead">{{ t('historyView.lead') }}</p>
      </div>
      <div class="history-hero__actions">
        <div class="history-hero__meta muted">
          <span>{{ t('historyView.loadedRuns', { count: accountsStore.historyArchive.length }) }}</span>
          <span v-if="latestRun">{{ t('historyView.latestFinishedAt', { value: formatDateTime(latestRun.finishedAt) }) }}</span>
        </div>
        <el-button :loading="accountsStore.historyLoading" @click="loadHistory">
          {{ t('historyView.refresh') }}
        </el-button>
      </div>
    </section>

    <section class="stats-grid">
      <article class="stat-card stat-card--accent">
        <span class="stat-label">{{ t('historyView.summary.latestStatus') }}</span>
        <strong>{{ latestRun ? taskStatusLabel(latestRun.status) : t('common.notAvailable') }}</strong>
        <small>{{ latestRun ? t('historyView.summary.latestRun', { id: latestRun.runId }) : t('historyView.empty') }}</small>
      </article>
      <article class="stat-card">
        <span class="stat-label">{{ t('historyView.summary.successRate') }}</span>
        <strong>{{ recentSuccessRate }}%</strong>
        <small>{{ t('historyView.summary.recentWindow') }}</small>
      </article>
      <article class="stat-card">
        <span class="stat-label">{{ t('historyView.summary.latestFiltered') }}</span>
        <strong>{{ latestRun?.filteredAccounts ?? 0 }}</strong>
        <small>{{ t('historyView.summary.latestFilteredHint') }}</small>
      </article>
      <article class="stat-card">
        <span class="stat-label">{{ t('historyView.summary.latestErrors') }}</span>
        <strong>{{ latestRun?.errorCount ?? 0 }}</strong>
        <small>{{ t('historyView.summary.latestErrorsHint') }}</small>
      </article>
    </section>

    <section class="history-compare-grid">
      <article class="panel panel--fill history-panel">
        <div class="panel-head panel-head--tight">
          <div>
            <p class="panel-kicker">{{ t('historyView.comparison.kicker') }}</p>
            <h3>{{ t('historyView.comparison.title') }}</h3>
          </div>
          <el-select
            :model-value="comparisonRunId"
            class="history-panel__select"
            :placeholder="t('historyView.comparison.selectRun')"
            @change="comparisonRunId = $event"
          >
            <el-option
              v-for="item in accountsStore.historyArchive"
              :key="item.runId"
              :label="t('historyView.comparison.runOption', { id: item.runId, time: formatDateTime(item.finishedAt) })"
              :value="item.runId"
            />
          </el-select>
        </div>

        <div v-if="comparisonRun" class="history-comparison">
          <div class="history-comparison__head">
            <div>
              <strong>{{ t('historyView.comparison.currentRun', { id: comparisonRun.runId }) }}</strong>
              <span class="muted">{{ formatDateTime(comparisonRun.finishedAt) }}</span>
            </div>
            <div class="muted">
              {{ comparisonBaseRun ? t('historyView.comparison.baseRun', { id: comparisonBaseRun.runId }) : t('historyView.comparison.noBase') }}
            </div>
          </div>

          <div class="history-comparison__cards">
            <article v-for="item in comparisonCards" :key="item.key" class="history-comparison__card">
              <span class="history-comparison__label">{{ item.label }}</span>
              <strong>{{ item.current }}</strong>
              <small>{{ deltaLabel(item.current, item.previous) }}</small>
            </article>
          </div>
        </div>

        <div v-else class="history-panel__empty muted">
          {{ t('historyView.empty') }}
        </div>
      </article>

      <article class="panel panel--fill history-panel">
        <div class="panel-head panel-head--tight">
          <div>
            <p class="panel-kicker">{{ t('historyView.trend.kicker') }}</p>
            <h3>{{ t('historyView.trend.title') }}</h3>
          </div>
        </div>

        <div v-if="recentWindow.length > 0" class="history-trend-list">
          <div v-for="item in recentWindow" :key="item.runId" class="history-trend-row">
            <div class="history-trend-row__main">
              <strong>#{{ item.runId }}</strong>
              <span>{{ formatDateTime(item.finishedAt) }}</span>
            </div>
            <div class="history-trend-row__metrics muted">
              <span>{{ t('historyView.cards.invalid401') }} {{ item.invalid401Count }}</span>
              <span>{{ t('historyView.cards.quotaLimited') }} {{ item.quotaLimitedCount }}</span>
              <span>{{ t('historyView.cards.error') }} {{ item.errorCount }}</span>
            </div>
          </div>
        </div>

        <div v-else class="history-panel__empty muted">
          {{ t('historyView.empty') }}
        </div>
      </article>
    </section>

    <section class="panel panel--fill history-table-panel">
      <div class="panel-head panel-head--tight">
        <div>
          <p class="panel-kicker">{{ t('historyView.table.kicker') }}</p>
          <h3>{{ t('historyView.table.title') }}</h3>
        </div>
        <el-select
          v-model="statusFilter"
          class="history-table-panel__filter"
          :placeholder="t('historyView.table.statusFilter')"
        >
          <el-option :label="t('historyView.table.statusAll')" value="all" />
          <el-option :label="t('tasks.statuses.success')" value="success" />
          <el-option :label="t('tasks.statuses.failed')" value="failed" />
          <el-option :label="t('tasks.statuses.cancelled')" value="cancelled" />
          <el-option :label="t('settings.scheduleStatus.skipped')" value="skipped" />
        </el-select>
      </div>

      <div class="panel__body panel__body--table">
        <div class="table-wrap">
          <el-table :data="historyRows" height="100%">
            <el-table-column prop="runId" :label="t('dashboard.historyColumns.run')" width="76" />
            <el-table-column :label="t('dashboard.historyColumns.status')" width="120">
              <template #default="{ row }">
                <StatusPill :state="taskStatusState(row.status)" :label="taskStatusLabel(row.status)" />
              </template>
            </el-table-column>
            <el-table-column prop="filteredAccounts" :label="t('dashboard.historyColumns.filtered')" width="96" />
            <el-table-column prop="invalid401Count" :label="t('dashboard.historyColumns.invalid')" width="84" />
            <el-table-column prop="quotaLimitedCount" :label="t('dashboard.historyColumns.quota')" width="92" />
            <el-table-column prop="recoveredCount" :label="t('dashboard.historyColumns.recovered')" width="104" />
            <el-table-column prop="errorCount" :label="t('states.error')" width="76" />
            <el-table-column :label="t('dashboard.historyColumns.finished')" min-width="180">
              <template #default="{ row }">
                {{ formatDateTime(row.finishedAt) }}
              </template>
            </el-table-column>
            <el-table-column :label="t('historyView.table.message')" min-width="240" show-overflow-tooltip>
              <template #default="{ row }">
                {{ row.message || t('common.notAvailable') }}
              </template>
            </el-table-column>
            <el-table-column label="" width="128">
              <template #default="{ row }">
                <el-button text @click="openDetail(row.runId)">
                  {{ t('historyView.table.inspect') }}
                </el-button>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </div>
    </section>

    <el-drawer
      v-model="drawerOpen"
      :class="['scan-detail-drawer', `scan-detail-drawer--${shellMode}`]"
      modal-class="scan-detail-overlay"
      :size="drawerSize"
      @closed="detailRunId = null"
    >
      <template #header>
        <div v-if="scanDetailSummary" class="scan-detail-header">
          <div class="scan-detail-header__copy">
            <p class="panel-kicker">{{ t('historyView.table.title') }}</p>
            <h3>{{ t('dashboard.scanDetail') }}</h3>
            <p class="muted">
              {{ t('dashboard.runLabel', { id: scanDetailSummary.runId }) }}
              <span class="scan-detail-header__dot">|</span>
              {{ t('dashboard.historyColumns.finished') }}
              {{ formatDateTime(scanDetailSummary.finishedAt) }}
            </p>
          </div>
          <StatusPill :state="scanDetailStatusState" :label="taskStatusLabel(scanDetailSummary.status)" />
        </div>
      </template>

      <template v-if="scanDetailSummary">
        <div class="scan-detail-shell">
          <div class="scan-detail-metrics">
            <article class="scan-detail-metric">
              <span class="scan-detail-metric__label">{{ t('dashboard.historyColumns.run') }}</span>
              <strong>#{{ scanDetailSummary.runId }}</strong>
            </article>
            <article class="scan-detail-metric scan-detail-metric--status">
              <span class="scan-detail-metric__label">{{ t('dashboard.historyColumns.status') }}</span>
              <StatusPill :state="scanDetailStatusState" :label="taskStatusLabel(scanDetailSummary.status)" />
            </article>
            <article class="scan-detail-metric">
              <span class="scan-detail-metric__label">{{ t('dashboard.historyColumns.filtered') }}</span>
              <strong>{{ scanDetailSummary.filteredAccounts }}</strong>
            </article>
            <article class="scan-detail-metric">
              <span class="scan-detail-metric__label">{{ t('states.error') }}</span>
              <strong>{{ scanDetailSummary.errorCount }}</strong>
            </article>
          </div>

          <div class="scan-detail-table-shell" :class="{ 'scan-detail-table-shell--loading': detailLoading }">
            <div class="scan-detail-table-frame">
              <el-table class="scan-detail-table" :data="scanDetailRecords" height="100%">
                <el-table-column :label="t('dashboard.detailColumns.name')" min-width="320" show-overflow-tooltip>
                  <template #default="{ row }">
                    <div class="scan-detail-name">
                      <strong>{{ row.name }}</strong>
                      <span>{{ row.provider || t('common.unknown') }}</span>
                    </div>
                  </template>
                </el-table-column>
                <el-table-column :label="t('dashboard.detailColumns.state')" width="132">
                  <template #default="{ row }">
                    <StatusPill :state="row.stateKey || row.state" />
                  </template>
                </el-table-column>
                <el-table-column :label="t('dashboard.detailColumns.plan')" width="110">
                  <template #default="{ row }">
                    {{ row.planType || t('common.notAvailable') }}
                  </template>
                </el-table-column>
                <el-table-column :label="t('dashboard.detailColumns.probeError')" min-width="320" show-overflow-tooltip>
                  <template #default="{ row }">
                    {{ row.probeErrorText || row.statusMessage || t('common.notAvailable') }}
                  </template>
                </el-table-column>
              </el-table>
            </div>

            <div class="scan-detail-pagination">
              <span class="muted">
                {{ t('historyView.detailTotal', { count: scanDetailTotal }) }}
              </span>
              <el-pagination
                :current-page="detailPage"
                :page-size="detailPageSize"
                background
                :page-sizes="detailPageSizes"
                :total="scanDetailTotal"
                layout="total, sizes, prev, pager, next, jumper"
                @current-change="changeDetailPage"
                @size-change="changeDetailPageSize"
              />
            </div>
          </div>
        </div>
      </template>
    </el-drawer>
  </div>
</template>

<style scoped>
.view-shell--history-page {
  display: flex;
  flex-direction: column;
  gap: 0.95rem;
  min-height: 0;
}

.history-hero,
.history-compare-grid {
  display: grid;
  gap: 0.9rem;
}

.history-hero {
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: end;
}

.history-hero__actions {
  display: grid;
  gap: 0.55rem;
  justify-items: end;
}

.history-hero__meta {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 0.65rem;
}

.history-compare-grid {
  grid-template-columns: minmax(0, 1.1fr) minmax(300px, 0.9fr);
}

.history-panel {
  min-height: 0;
}

.history-panel__select,
.history-table-panel__filter {
  width: min(320px, 100%);
}

.history-comparison {
  display: grid;
  gap: 0.85rem;
}

.history-comparison__head,
.history-trend-row,
.history-trend-row__main,
.history-trend-row__metrics {
  display: flex;
  gap: 0.65rem;
}

.history-comparison__head,
.history-trend-row {
  justify-content: space-between;
  align-items: center;
}

.history-comparison__cards {
  display: grid;
  gap: 0.75rem;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.history-comparison__card {
  display: grid;
  gap: 0.25rem;
  padding: 0.9rem 1rem;
  border-radius: 18px;
  background: rgba(255, 255, 255, 0.65);
  border: 1px solid rgba(112, 121, 89, 0.14);
}

.history-comparison__label {
  font-size: 0.82rem;
  color: rgba(67, 76, 58, 0.72);
}

.history-trend-list {
  display: grid;
  gap: 0.65rem;
}

.history-trend-row {
  padding: 0.8rem 0.95rem;
  border-radius: 18px;
  background: rgba(255, 255, 255, 0.56);
  border: 1px solid rgba(112, 121, 89, 0.12);
}

.history-panel__empty {
  padding: 1.2rem 0;
}

@media (max-width: 1280px) {
  .history-hero,
  .history-compare-grid {
    grid-template-columns: 1fr;
  }

  .history-hero__actions {
    justify-items: stretch;
  }

  .history-hero__meta {
    justify-content: flex-start;
  }
}

@media (max-width: 860px) {
  .history-comparison__cards {
    grid-template-columns: 1fr;
  }

  .history-comparison__head,
  .history-trend-row,
  .history-trend-row__main,
  .history-trend-row__metrics {
    flex-direction: column;
    align-items: flex-start;
  }
}
</style>
