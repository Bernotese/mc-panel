import type { TFunction } from 'i18next'
import { changedFormKeys, type FormChangeKey, type PropertiesDraft, type ServerFormValue } from './form-model'
import { javaLabel } from './format'
import type { DraftChange } from './draft-conflict'

const ENUM_PROPERTIES: ReadonlySet<string> = new Set(['difficulty', 'gamemode', 'level_type'])

function label(key: FormChangeKey, t: TFunction): string {
  if (key.startsWith('properties.')) return t(`servers.props.${key.slice('properties.'.length)}`)
  if (key === 'version') return t('servers.form.version')
  if (key === 'loaderVersion') return t('servers.draft.loader')
  if (key === 'modpack') return t('servers.draft.modpack')
  if (key === 'javaOverride') return t('servers.form.java')
  return t('servers.form.memory')
}

function display(v: ServerFormValue, key: FormChangeKey, t: TFunction): string {
  if (key.startsWith('properties.')) {
    const p = key.slice('properties.'.length) as keyof PropertiesDraft
    const value = v.properties[p]
    if (typeof value === 'boolean') return t(value ? 'common.yes' : 'common.no')
    if (ENUM_PROPERTIES.has(p)) return t(`servers.${p}.${value}`)
    return value === '' ? t('servers.draft.none') : value
  }
  if (key === 'version') return v.version || t('servers.form.version_latest')
  if (key === 'loaderVersion') return v.loaderVersion || t('servers.form.loader_latest')
  if (key === 'modpack') return v.modpack || t('servers.draft.none')
  if (key === 'javaOverride') return v.javaOverride ? javaLabel(v.javaOverride) : t('servers.draft.java_auto')
  return t('servers.draft.gib', { value: v.memoryGiB })
}

/** Issue #154: what changed on the server since `base`, and the own value where the draft changed it differently. */
export function serverFormChanges(base: ServerFormValue, draft: ServerFormValue, remote: ServerFormValue, t: TFunction): DraftChange[] {
  const mine = new Set(changedFormKeys(base, draft))
  return changedFormKeys(base, remote).map((key) => ({
    key,
    label: label(key, t),
    theirs: display(remote, key, t),
    mine: mine.has(key) && changedFormKeys(draft, remote).includes(key) ? display(draft, key, t) : null,
  }))
}
