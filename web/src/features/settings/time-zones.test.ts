import { describe, expect, it } from 'vitest'
import { timeZoneOptions } from './time-zones'

describe('timeZoneOptions', () => {
  it('puts the current value and UTC first, without duplicates', () => {
    expect(timeZoneOptions('Europe/Berlin', ['Africa/Abidjan', 'Europe/Berlin', 'UTC'])).toEqual(['Europe/Berlin', 'UTC', 'Africa/Abidjan'])
  })

  it('keeps an unknown stored value selectable', () => {
    expect(timeZoneOptions('Etc/GMT+1', ['Europe/Berlin'])).toEqual(['Etc/GMT+1', 'UTC', 'Europe/Berlin'])
  })

  it('falls back to current value and UTC without Intl support', () => {
    expect(timeZoneOptions('Europe/Berlin', [])).toEqual(['Europe/Berlin', 'UTC'])
  })
})
