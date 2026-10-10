import { describe, expect, it, vi } from 'vitest'

describe('test setup', () => {
  // jsdom does not implement window.scrollTo and prints "Not implemented" for every call; the
  // router scrolls on navigation, so the setup replaces it with a no-op mock.
  it('stubs window.scrollTo', () => {
    expect(vi.isMockFunction(window.scrollTo)).toBe(true)
    expect(() => window.scrollTo(0, 0)).not.toThrow()
  })
})
