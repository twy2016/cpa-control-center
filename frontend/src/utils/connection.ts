const localHostAliases = new Set([
  '127.0.0.1',
  '0.0.0.0',
  'localhost',
  '::1',
])

function normalizeHostname(hostname: string): string {
  const lowered = String(hostname || '').trim().toLowerCase()
  const unwrapped = lowered.replace(/^\[(.*)\]$/, '$1')
  if (localHostAliases.has(unwrapped)) {
    return 'localhost'
  }
  return unwrapped
}

function normalizePort(url: URL): string {
  if (url.port) {
    return url.port
  }
  if (url.protocol === 'https:') {
    return '443'
  }
  if (url.protocol === 'http:') {
    return '80'
  }
  return ''
}

export function normalizeServiceOrigin(rawUrl: string): string {
  const candidate = String(rawUrl || '').trim()
  if (!candidate) {
    return ''
  }

  try {
    const parsed = new URL(candidate)
    const protocol = parsed.protocol.toLowerCase()
    const hostname = normalizeHostname(parsed.hostname)
    const port = normalizePort(parsed)
    return `${protocol}//${hostname}${port ? `:${port}` : ''}`
  } catch {
    return candidate.toLowerCase().replace(/\/+$/, '')
  }
}

export function isSameServiceOrigin(left: string, right: string): boolean {
  const normalizedLeft = normalizeServiceOrigin(left)
  const normalizedRight = normalizeServiceOrigin(right)
  if (!normalizedLeft || !normalizedRight) {
    return false
  }
  return normalizedLeft === normalizedRight
}
