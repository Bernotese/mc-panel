import { describe, expect, it } from 'vitest'
import type { Catalog, InstanceConfig } from '@/api/types'
import {
  effectiveJava,
  effectiveVersion,
  emptyServerForm,
  formFromInstance,
  resolveServerForm,
  sameFormValue,
  validateServerForm,
  type FormContext,
  type ServerFormValue,
} from './form-model'

const paperCatalog: Catalog = {
  versions: [
    { id: '26.2', java: 'java25' },
    { id: '1.21.11', java: 'java21' },
    { id: '1.20.4', java: 'java17' },
  ],
  loaders: [],
}

function ctx(mode: FormContext['mode'], catalog?: Catalog, curseforgeKeySet = false): FormContext {
  return { mode, catalog, curseforgeKeySet }
}

function form(patch: Partial<ServerFormValue>): ServerFormValue {
  return { ...emptyServerForm(), name: 'skyblock', eulaAccepted: true, ...patch }
}

const survival: InstanceConfig = {
  type: 'paper',
  version: '1.21.11',
  loader_version: null,
  modpack: null,
  java: 'java21',
  memory_mib: 4352,
  properties: {
    motd: 'Hallo',
    difficulty: 'hard',
    gamemode: 'survival',
    max_players: 8,
    pvp: false,
    hardcore: false,
    allow_flight: true,
    enable_command_block: false,
    online_mode: true,
    whitelist_enabled: true,
    view_distance: 12,
    simulation_distance: 8,
    spawn_protection: 0,
    seed: '42',
    level_type: 'flat',
  },
}

describe('server form model', () => {
  it('starts with the spec defaults', () => {
    const value = emptyServerForm()
    expect(value).toMatchObject({ type: 'paper', version: '', memoryGiB: '4', javaOverride: '', eulaAccepted: false })
    expect(value.properties).toMatchObject({
      motd: '',
      difficulty: 'normal',
      gamemode: 'survival',
      max_players: '20',
      pvp: true,
      online_mode: true,
      view_distance: '10',
      simulation_distance: '10',
      spawn_protection: '16',
      level_type: 'normal',
    })
  })

  it('resolves a new paper server with the newest catalog version and spec defaults', () => {
    expect(resolveServerForm(form({}), ctx('create', paperCatalog))).toEqual({
      name: 'skyblock',
      type: 'paper',
      version: '26.2',
      loader_version: null,
      modpack: null,
      java: 'java25',
      memory_mib: 4096,
      properties: {
        difficulty: 'normal',
        gamemode: 'survival',
        max_players: 20,
        pvp: true,
        hardcore: false,
        allow_flight: false,
        enable_command_block: false,
        online_mode: true,
        whitelist_enabled: false,
        view_distance: 10,
        simulation_distance: 10,
        spawn_protection: 16,
        seed: '',
        level_type: 'normal',
      },
    })
  })

  it('requires name and EULA only when creating', () => {
    expect(Object.keys(validateServerForm(emptyServerForm(), ctx('create', paperCatalog))).sort()).toEqual(['eula', 'name'])
    expect(validateServerForm(formFromInstance('survival', survival), ctx('edit', paperCatalog))).toEqual({})
  })

  it('needs a version until the catalog has loaded', () => {
    expect(effectiveVersion(form({}), undefined)).toBe('')
    expect(validateServerForm(form({}), ctx('create')).version?.key).toBe('servers.validation.version_required')
  })

  it('derives java from the catalog, falls back to javaFor and honours an override', () => {
    expect(effectiveJava(form({ version: '1.20.4' }), paperCatalog)).toBe('java17')
    expect(effectiveJava(form({ version: '1.12.2', manualVersion: true }), undefined)).toBe('java8')
    expect(effectiveJava(form({ version: '1.12.2', javaOverride: 'java17' }), undefined)).toBe('java17')
  })

  it('sends no version for modpacks and checks the slug', () => {
    expect(validateServerForm(form({ type: 'modrinth', modpack: 'Bad Slug' }), ctx('create')).modpack?.key).toBe(
      'servers.validation.modpack_modrinth',
    )
    expect(resolveServerForm(form({ type: 'modrinth', modpack: ' cobblemon-fabric ' }), ctx('create'))).toMatchObject({
      version: '',
      modpack: 'cobblemon-fabric',
      loader_version: null,
      java: 'java21',
    })
  })

  it('maps an empty fabric loader to the latest one', () => {
    const fabric: Catalog = { versions: [{ id: '26.3', java: 'java25' }], loaders: ['0.19.5', '0.19.4'] }
    expect(resolveServerForm(form({ type: 'fabric' }), ctx('create', fabric))?.loader_version).toBeNull()
    expect(resolveServerForm(form({ type: 'fabric', loaderVersion: '0.19.4' }), ctx('create', fabric))?.loader_version).toBe('0.19.4')
  })

  it('blocks CurseForge without a stored key and with java8', () => {
    const curseforge = form({ type: 'curseforge', modpack: 'atm10' })
    expect(validateServerForm(curseforge, ctx('create')).type?.key).toBe('servers.validation.curseforge_key_missing')
    expect(validateServerForm({ ...curseforge, javaOverride: 'java8' }, ctx('create', undefined, true)).java?.key).toBe(
      'servers.validation.java_curseforge',
    )
    expect(validateServerForm(curseforge, ctx('create', undefined, true))).toEqual({})
  })

  it('accepts memory in quarter GiB steps only', () => {
    expect(resolveServerForm(form({ memoryGiB: '6.25' }), ctx('create', paperCatalog))?.memory_mib).toBe(6400)
    for (const memoryGiB of ['4.3', '0.5', '33', '']) {
      expect(validateServerForm(form({ memoryGiB }), ctx('create', paperCatalog)).memory, memoryGiB).toBeDefined()
    }
  })

  it('validates properties', () => {
    const value = form({ properties: { ...emptyServerForm().properties, max_players: '0', motd: 'a\nb' } })
    expect(Object.keys(validateServerForm(value, ctx('create', paperCatalog))).sort()).toEqual(['properties.max_players', 'properties.motd'])
    const fixed = { ...value, properties: { ...value.properties, max_players: '8', motd: 'Hallo' } }
    const properties = resolveServerForm(fixed, ctx('create', paperCatalog))?.properties
    expect(properties).toMatchObject({ motd: 'Hallo', max_players: 8 })
    expect(properties).not.toHaveProperty('whitelist')
    expect(properties).not.toHaveProperty('ops')
  })

  it('round-trips an existing instance in edit mode', () => {
    expect(resolveServerForm(formFromInstance('survival', survival), ctx('edit', paperCatalog))).toEqual({
      name: 'survival',
      type: 'paper',
      version: '1.21.11',
      loader_version: null,
      modpack: null,
      java: 'java21',
      memory_mib: 4352,
      properties: survival.properties,
    })
  })

  it('re-derives java when an existing server changes its version', () => {
    const edited = { ...formFromInstance('survival', survival), version: '1.20.4' }
    expect(resolveServerForm(edited, ctx('edit', paperCatalog))?.java).toBe('java17')
  })

  it('keeps an explicit java override', () => {
    const value = formFromInstance('survival', { ...survival, java: 'java25' })
    expect(value.javaOverride).toBe('java25')
    expect(resolveServerForm({ ...value, version: '1.20.4' }, ctx('edit', paperCatalog))?.java).toBe('java25')
  })

  it('detects changes against the initial value', () => {
    const initial = formFromInstance('survival', survival)
    expect(sameFormValue(initial, formFromInstance('survival', survival))).toBe(true)
    expect(sameFormValue(initial, { ...initial, memoryGiB: '5' })).toBe(false)
  })
})
