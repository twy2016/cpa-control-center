export function loadWorkspaceState<T>(key: string, fallback: T): T {
  if (typeof window === 'undefined' || !window.localStorage) {
    return fallback
  }

  try {
    const raw = window.localStorage.getItem(`cpa-control-center:${key}`)
    if (!raw) {
      return fallback
    }
    return JSON.parse(raw) as T
  } catch {
    return fallback
  }
}

export function saveWorkspaceState<T>(key: string, value: T) {
  if (typeof window === 'undefined' || !window.localStorage) {
    return
  }

  try {
    window.localStorage.setItem(`cpa-control-center:${key}`, JSON.stringify(value))
  } catch {
    // Ignore local persistence failures so the UI keeps working.
  }
}
