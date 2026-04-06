import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { ElMessage } from 'element-plus'
import CodexConfigsView from '@/views/CodexConfigsView.vue'
import { createTestI18n } from '@/test/i18n'

let mockStore: any

vi.mock('@/stores/codexLocalConfig', () => ({
  useCodexLocalConfigStore: () => mockStore,
}))

vi.mock('element-plus', async () => {
  const actual = await vi.importActual<typeof import('element-plus')>('element-plus')
  return {
    ...actual,
    ElMessage: {
      success: vi.fn(),
      error: vi.fn(),
    },
    ElMessageBox: {
      confirm: vi.fn(),
    },
  }
})

function createStoreMock(currentFilesReady = true) {
  return {
    snapshot: {
      profiles: [],
      activeProfileName: '',
      defaultDirectory: 'C:/Users/demo/.codex',
      configPath: 'C:/Users/demo/.codex/config.toml',
      authPath: 'C:/Users/demo/.codex/auth.json',
      currentConfigExists: currentFilesReady,
      currentAuthExists: currentFilesReady,
      backups: [],
    },
    profileContent: null,
    busy: false,
    loading: false,
    contentLoading: false,
    contentSaving: false,
    contentTesting: false,
    connectionTestingName: '',
    lastConnectionResults: {},
    activeProfile: '',
    selectedProfile: '',
    loadSnapshot: vi.fn().mockResolvedValue(undefined),
    selectProfile: vi.fn().mockResolvedValue(undefined),
    importCurrent: vi.fn().mockResolvedValue(undefined),
    openDirectory: vi.fn().mockResolvedValue(undefined),
    testProfileContent: vi.fn().mockResolvedValue({ ok: true, message: '' }),
    createProfileContent: vi.fn().mockResolvedValue(undefined),
    loadProfileContent: vi.fn().mockResolvedValue(undefined),
    saveProfileContent: vi.fn().mockResolvedValue(null),
    switchProfile: vi.fn().mockResolvedValue(undefined),
    deleteProfile: vi.fn().mockResolvedValue(undefined),
    testSavedProfileConnection: vi.fn().mockResolvedValue({ ok: true, message: '' }),
  }
}

function mountView() {
  return mount(CodexConfigsView, {
    global: {
      plugins: [createTestI18n()],
      stubs: {
        ElButton: {
          props: ['disabled', 'loading', 'plain', 'text', 'type', 'size'],
          emits: ['click'],
          template: '<button :disabled="disabled" @click="$emit(\'click\')"><slot /></button>',
        },
        ElInput: {
          props: ['modelValue', 'placeholder', 'disabled', 'type', 'rows', 'resize'],
          emits: ['update:modelValue'],
          template: `
            <textarea
              v-if="type === 'textarea'"
              :value="modelValue"
              :disabled="disabled"
              @input="$emit('update:modelValue', $event.target.value)"
            />
            <input
              v-else
              :value="modelValue"
              :placeholder="placeholder"
              :disabled="disabled"
              @input="$emit('update:modelValue', $event.target.value)"
            />
          `,
        },
      },
    },
  })
}

describe('CodexConfigsView', () => {
  it('restores importing the current local Codex files from the page', async () => {
    mockStore = createStoreMock(true)

    const wrapper = mountView()
    await flushPromises()

    expect(mockStore.loadSnapshot).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('Import Current Files')

    const input = wrapper.get('.codex-config-import input')
    expect(input.attributes('placeholder')).toBe('Enter a supplier name and import current ~/.codex files')
    await input.setValue('OpenAI')

    const importButton = wrapper.findAll('button').find((button) => button.text() === 'Import Current Files')
    expect(importButton?.attributes('disabled')).toBeUndefined()

    await importButton!.trigger('click')
    await flushPromises()

    expect(mockStore.importCurrent).toHaveBeenCalledWith('OpenAI')
    expect((wrapper.get('.codex-config-import input').element as HTMLInputElement).value).toBe('')
    expect(ElMessage.success).toHaveBeenCalledWith('Current Codex files imported.')
  })

  it('disables importing when the current local files are incomplete', async () => {
    mockStore = createStoreMock(false)

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('Missing file')

    const importButton = wrapper.findAll('button').find((button) => button.text() === 'Import Current Files')
    expect(importButton?.attributes('disabled')).toBeDefined()
  })
})
