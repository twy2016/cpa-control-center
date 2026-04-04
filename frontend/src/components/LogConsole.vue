<script setup lang="ts">
import { computed, inject, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LogEntry } from '@/types'
import { formatDateTime } from '@/utils/format'
import { logKindLabel, logLevelLabel } from '@/utils/status'
import { shellModeKey } from '@/layout/shell'

const { t } = useI18n()
const injectedShellMode = inject(shellModeKey, null)
const shellMode = computed(() => injectedShellMode?.value ?? 'desktop')
const consoleRef = ref<HTMLDivElement | null>(null)

const props = defineProps<{
  entries: LogEntry[]
}>()

function scrollToBottom() {
  const el = consoleRef.value
  if (!el) {
    return
  }
  el.scrollTop = el.scrollHeight
}

function isNearBottom() {
  const el = consoleRef.value
  if (!el) {
    return true
  }
  return el.scrollHeight - el.scrollTop - el.clientHeight < 56
}

watch(
  () => props.entries.length,
  async (_, previousLength) => {
    const stickToBottom = previousLength === undefined || isNearBottom()
    await nextTick()
    if (stickToBottom) {
      scrollToBottom()
    }
  },
  { flush: 'post', immediate: true },
)
</script>

<template>
  <div ref="consoleRef" class="log-console" :class="{ 'log-console--compact': shellMode === 'compact' }">
    <div v-if="entries.length === 0" class="log-empty">
      {{ t('logs.empty') }}
    </div>
    <div v-for="entry in entries" :key="entry.id || `${entry.timestamp}-${entry.message}`" class="log-row" :class="{ 'log-row--progress': entry.progress }">
      <span class="log-time">{{ formatDateTime(entry.timestamp) }}</span>
      <span class="log-kind">{{ logKindLabel(entry.kind) }}</span>
      <span class="log-level" :data-level="entry.level">{{ logLevelLabel(entry.level) }}</span>
      <span class="log-message">{{ entry.message }}</span>
    </div>
  </div>
</template>

<style scoped>
.log-console {
  height: 100%;
  min-height: 0;
  overflow: auto;
  padding: 1rem;
  border-radius: 20px;
  background:
    radial-gradient(circle at top right, rgba(250, 204, 21, 0.12), transparent 35%),
    linear-gradient(180deg, rgba(20, 20, 18, 0.96), rgba(35, 33, 28, 0.96));
  color: #f6f0de;
  font-family: "Consolas", "SFMono-Regular", monospace;
  text-align: left;
  box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.06);
}

.log-empty {
  color: rgba(246, 240, 222, 0.72);
}

.log-row {
  display: grid;
  grid-template-columns: minmax(130px, 170px) 70px 80px minmax(0, 1fr);
  gap: 0.8rem;
  padding: 0.45rem 0;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  align-items: start;
}

.log-row--progress {
  background: rgba(103, 232, 249, 0.06);
}

.log-console--compact {
  padding: 0.85rem;
}

.log-console--compact .log-row {
  grid-template-columns: 1fr;
  gap: 0.2rem;
}

.log-time {
  color: rgba(246, 240, 222, 0.64);
}

.log-kind {
  text-transform: uppercase;
  color: #facc15;
}

.log-level[data-level="error"] {
  color: #f97316;
}

.log-level[data-level="warning"] {
  color: #fb7185;
}

.log-level[data-level="info"] {
  color: #67e8f9;
}
</style>
