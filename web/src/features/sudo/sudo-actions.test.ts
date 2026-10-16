import { describe, expect, it } from 'vitest'
import { de } from '@/i18n/de'
import { SUDO_ACTIONS, sudoActionKey } from './sudo-actions'

describe('sudoActionKey', () => {
  it('maps method and path template to a translated label', () => {
    expect(sudoActionKey({ method: 'DELETE', path: '/servers/{id}' })).toBe('sudo.action.server_delete')
    expect(sudoActionKey({ method: 'post', path: '/servers/{id}/backups/{snapshot}/restore' })).toBe('sudo.action.backup_restore')
    expect(sudoActionKey({ method: 'GET', path: '/servers/{id}' })).toBe('sudo.action.generic')
    expect(sudoActionKey({ method: 'POST', path: '/nope' })).toBe('sudo.action.generic')
  })

  it('has a text for every action (en has the same keys by type)', () => {
    const texts: Record<string, string> = de.sudo.action
    for (const key of [...Object.values(SUDO_ACTIONS), 'generic']) {
      expect(texts[key], key).toEqual(expect.any(String))
    }
  })
})
