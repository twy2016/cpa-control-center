import { describe, expect, it } from 'vitest'
import { isSameServiceOrigin, normalizeServiceOrigin } from '@/utils/connection'

describe('connection helpers', () => {
  it('treats localhost aliases as the same local service origin', () => {
    expect(isSameServiceOrigin('http://localhost:8317', 'http://127.0.0.1:8317/')).toBe(true)
    expect(isSameServiceOrigin('http://0.0.0.0:8317', 'http://localhost:8317')).toBe(true)
    expect(isSameServiceOrigin('https://[::1]', 'https://localhost:443')).toBe(true)
  })

  it('keeps different ports or schemes distinct', () => {
    expect(isSameServiceOrigin('http://localhost:8317', 'http://localhost:9000')).toBe(false)
    expect(isSameServiceOrigin('http://localhost:8317', 'https://localhost:8317')).toBe(false)
  })

  it('normalizes origins to a stable comparable shape', () => {
    expect(normalizeServiceOrigin('HTTP://127.0.0.1:8317/management.html#/login')).toBe('http://localhost:8317')
  })
})
