import { describe, expect, it } from 'vitest'
import { visibleName } from './file-name'

describe('file names (SEC-38)', () => {
  it('shows format characters visibly', () => {
    expect(visibleName('evil\u202Egpj.jar')).toBe('evil⟨U+202E⟩gpj.jar')
    expect(visibleName('a\u200Bb\u2066c\u2069\uFEFF')).toBe('a⟨U+200B⟩b⟨U+2066⟩c⟨U+2069⟩⟨U+FEFF⟩')
    expect(visibleName('server.properties')).toBe('server.properties')
    expect(visibleName('Welt ✨ 世界 عالم.zip')).toBe('Welt ✨ 世界 عالم.zip')
  })
})
