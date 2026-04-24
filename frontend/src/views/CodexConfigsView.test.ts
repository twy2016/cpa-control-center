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
      profiles: [
        {
          name: 'OpenAI',
          createdAt: '2026-04-09T08:00:00Z',
          updatedAt: '2026-04-09T08:00:00Z',
          lastActivatedAt: '',
          hasConfigToml: true,
          hasAuthJson: true,
        },
      ],
      activeProfileName: '',
      defaultDirectory: 'C:/Users/demo/.codex',
      configPath: 'C:/Users/demo/.codex/config.toml',
      authPath: 'C:/Users/demo/.codex/auth.json',
      currentConfigExists: currentFilesReady,
      currentAuthExists: currentFilesReady,
      backups: [],
    },
    profileContent: {
      name: 'OpenAI',
      originalName: 'OpenAI',
      configToml: 'model = "gpt-5"\n',
      authJson: '{\n  "OPENAI_API_KEY": "test-key"\n}\n',
      updatedAt: '2026-04-09T08:00:00Z',
    },
    busy: false,
    loading: false,
    contentLoading: false,
    contentSaving: false,
    contentTesting: false,
    connectionTestingName: '',
    lastConnectionResults: {},
    activeProfile: '',
    selectedProfile: 'OpenAI',
    loadSnapshot: vi.fn().mockResolvedValue(undefined),
    selectProfile: vi.fn().mockResolvedValue(undefined),
    importCurrent: vi.fn().mockResolvedValue(undefined),
    importProfileFromFile: vi.fn().mockResolvedValue('Imported Profile'),
    importProfilesFromFile: vi.fn().mockResolvedValue({ count: 2, path: 'C:/exports/codex-profiles.bundle.json', names: ['OpenAI', 'OpenRouter'] }),
    exportProfile: vi.fn().mockResolvedValue('C:/exports/OpenAI.codex-profile.json'),
    exportAllProfiles: vi.fn().mockResolvedValue({ count: 1, path: 'C:/exports/codex-profiles.bundle.json', names: ['OpenAI'] }),
    openDirectory: vi.fn().mockResolvedValue(undefined),
    createProfileTemplate: vi.fn().mockReturnValue({
      name: '',
      originalName: '',
      configToml: 'model = "gpt-5"\nmodel_provider = "openai"\n',
      authJson: '{\n  "OPENAI_API_KEY": "sk-your-api-key"\n}\n',
      updatedAt: '',
    }),
    testProfileContent: vi.fn().mockResolvedValue({ ok: true, message: '' }),
    createProfileContent: vi.fn().mockResolvedValue(undefined),
    loadProfileContent: vi.fn().mockResolvedValue(undefined),
    reloadProfileContent: vi.fn().mockResolvedValue(undefined),
    saveProfileContent: vi.fn().mockResolvedValue({
      name: 'OpenAI',
      originalName: 'OpenAI',
      configToml: 'model = "gpt-5"\n',
      authJson: '{\n  "OPENAI_API_KEY": "test-key"\n}\n',
      updatedAt: '2026-04-09T08:00:00Z',
    }),
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
  it('fills a starter template when opening the new profile editor', async () => {
    mockStore = createStoreMock(true)

    const wrapper = mountView()
    await flushPromises()

    expect(mockStore.loadSnapshot).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('New Profile')

    const toggleButton = wrapper.findAll('button').find((button) => button.text() === 'New Profile')
    await toggleButton!.trigger('click')
    await flushPromises()

    expect(mockStore.createProfileTemplate).toHaveBeenCalledTimes(1)

    const textareas = wrapper.findAll('.codex-config-editor--create textarea')
    expect(textareas).toHaveLength(2)
    expect((textareas[0].element as HTMLTextAreaElement).value).toContain('model_provider = "openai"')
    expect((textareas[1].element as HTMLTextAreaElement).value).toContain('"OPENAI_API_KEY"')
  })

  it('imports and exports all profiles from the page', async () => {
    mockStore = createStoreMock(true)

    const wrapper = mountView()
    await flushPromises()

    const importButton = wrapper.findAll('button').find((button) => button.text() === 'Import All Profiles')
    await importButton!.trigger('click')
    await flushPromises()

    expect(mockStore.importProfilesFromFile).toHaveBeenCalledTimes(1)
    expect(ElMessage.success).toHaveBeenCalledWith('Imported 2 supplier profiles.')

    const exportAllButton = wrapper.findAll('button').find((button) => button.text() === 'Export All Profiles')
    await exportAllButton!.trigger('click')
    await flushPromises()

    expect(mockStore.exportAllProfiles).toHaveBeenCalledTimes(1)
    expect(ElMessage.success).toHaveBeenCalledWith('Exported 1 supplier profiles to C:/exports/codex-profiles.bundle.json.')

    const exportButton = wrapper.findAll('button').find((button) => button.text() === 'Export')
    await exportButton!.trigger('click')
    await flushPromises()

    expect(mockStore.exportProfile).toHaveBeenCalledWith('OpenAI')
    expect(ElMessage.success).toHaveBeenCalledWith('Supplier profile exported to C:/exports/OpenAI.codex-profile.json.')
  })

  it('allows editing and saving the selected profile name', async () => {
    mockStore = createStoreMock(true)

    const wrapper = mountView()
    await flushPromises()

    const inputs = wrapper.findAll('.codex-config-editor input')
    const nameInput = inputs.find((input) => input.element instanceof HTMLInputElement)
    expect(nameInput).toBeTruthy()

    await nameInput!.setValue('OpenAI Renamed')
    await flushPromises()

    const saveButton = wrapper.findAll('button').find((button) => button.text() === 'Save Files')
    await saveButton!.trigger('click')
    await flushPromises()

    expect(mockStore.profileContent.name).toBe('OpenAI Renamed')
    expect(mockStore.testProfileContent).toHaveBeenCalledWith(
      'OpenAI Renamed',
      'model = "gpt-5"\n',
      '{\n  "OPENAI_API_KEY": "test-key"\n}\n',
    )
    expect(mockStore.saveProfileContent).toHaveBeenCalledTimes(1)
    expect(ElMessage.success).toHaveBeenCalledWith('Supplier profile files saved.')
  })

  it('reloads the selected profile from the dedicated reload action', async () => {
    mockStore = createStoreMock(true)

    const wrapper = mountView()
    await flushPromises()

    const reloadButton = wrapper.findAll('button').find((button) => button.text() === 'Reload')
    await reloadButton!.trigger('click')
    await flushPromises()

    expect(mockStore.reloadProfileContent).toHaveBeenCalledWith('OpenAI')
    expect(mockStore.loadProfileContent).not.toHaveBeenCalledWith('OpenAI')
  })
})
