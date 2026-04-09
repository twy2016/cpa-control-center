import { defineStore } from 'pinia'
import type { ViewKey } from '@/types'
import { useAccountsStore } from '@/stores/accounts'
import { saveWorkspaceState, loadWorkspaceState } from '@/utils/workspaceState'

interface WorkspaceState {
  activeView: ViewKey
}

function createDefaultWorkspaceState(): WorkspaceState {
  return {
    activeView: 'dashboard',
  }
}

function loadInitialWorkspaceState(): WorkspaceState {
  return {
    ...createDefaultWorkspaceState(),
    ...loadWorkspaceState<Partial<WorkspaceState>>('workspace', {}),
  }
}

export const useWorkspaceStore = defineStore('workspaceStore', {
  state: (): WorkspaceState => loadInitialWorkspaceState(),
  actions: {
    persist() {
      saveWorkspaceState('workspace', {
        activeView: this.activeView,
      })
    },
    setActiveView(view: ViewKey) {
      this.activeView = view
      this.persist()
    },
    async openAccounts(filters?: {
      query?: string
      stateFilter?: string
      providerFilter?: string
      planFilter?: string
      disabledFilter?: boolean | null
    }) {
      const accountsStore = useAccountsStore()
      await accountsStore.applyWorkspaceFilters({
        query: filters?.query ?? '',
        stateFilter: filters?.stateFilter ?? '',
        providerFilter: filters?.providerFilter ?? '',
        planFilter: filters?.planFilter ?? '',
        disabledFilter: filters?.disabledFilter ?? null,
      }, {
        resetPage: true,
        reload: true,
      })
      this.setActiveView('accounts')
    },
  },
})
