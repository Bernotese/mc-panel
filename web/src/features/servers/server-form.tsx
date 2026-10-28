import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useId, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { isSudoCancelled } from '@/api/errors'
import type { Catalog, InstanceType, JavaVersion, ServerMemoryLimits } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Section } from '@/components/section'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { cn } from '@/lib/utils'
import { catalogQueryOptions, memoryLimitsQueryOptions } from './api'
import { CollapsibleSection } from './collapsible-section'
import { FieldRow } from './field-row'
import {
  effectiveJava,
  effectiveVersion,
  EULA_URL,
  memoryLimitsFrom,
  memoryOvercommit,
  PROPERTY_FIELDS,
  resolveServerForm,
  sameFormValue,
  validateServerForm,
  type FormContext,
  type FormField,
  type ServerFormResult,
  type ServerFormValue,
} from './form-model'
import { javaLabel, memoryInputValue, serverAddress } from './format'
import { PropertiesFields } from './properties-fields'
import { INSTANCE_TYPES, isModpack, JAVA_VERSIONS, memoryMiBFromGiB, type FieldError, type MemoryLimits } from './validation'

const LEFT = '@4xl/server-form:col-start-1 @4xl/server-form:row-span-3 @4xl/server-form:row-start-1'
const RIGHT = '@4xl/server-form:col-start-2'
const FULL = '@4xl/server-form:col-span-2'

type Shown = (field: FormField) => FieldError | undefined
type Update = (patch: Partial<ServerFormValue>, ...fields: FormField[]) => void

export interface ServerFormProps {
  mode: 'create' | 'edit'
  initial: ServerFormValue
  /** create only: live address preview */
  baseDomain?: string | null
  /** create only: CurseForge can only be chosen with a stored API key */
  curseforgeKeySet?: boolean
  submitLabel: string
  pending: boolean
  /** Error of the surrounding mutation, rendered below the form (a cancelled sudo dialog is not an error). */
  error: unknown
  notice?: ReactNode
  onSubmit(result: ServerFormResult): void
}

/** Shared form for creating (name, type, EULA) and editing (everything else) a server. */
export function ServerForm({
  mode,
  initial,
  baseDomain = null,
  curseforgeKeySet = false,
  submitLabel,
  pending,
  error,
  notice,
  onSubmit,
}: ServerFormProps) {
  const { t } = useTranslation()
  const id = useId()
  const [draft, setValue] = useState<ServerFormValue>(initial)
  const [touched, setTouched] = useState<ReadonlySet<FormField>>(() => new Set())
  const [submitted, setSubmitted] = useState(false)
  const modpack = isModpack(draft.type)
  const catalog = useQuery({ ...catalogQueryOptions(draft.type), enabled: !modpack })
  // Without the host limits (still loading or failed) the form falls back to 1..32 GiB; the panel checks again.
  const memoryLimits = useQuery(memoryLimitsQueryOptions)
  // A new server follows the recommendation for its type until the admin changes the memory. Without a safe size
  // (null: not even the minimum fits the free budget) the field starts at the minimum and says so.
  const recommendedMiB = mode === 'create' ? memoryLimits.data?.recommended_mib[draft.type] : undefined
  const prefillMiB = recommendedMiB === null ? memoryLimits.data?.min_mib : recommendedMiB
  const value = prefillMiB !== undefined && !touched.has('memory') ? { ...draft, memoryGiB: memoryInputValue(prefillMiB) } : draft
  const ctx: FormContext = { mode, catalog: catalog.data, curseforgeKeySet, memory: memoryLimitsFrom(memoryLimits.data) }
  const errors = validateServerForm(value, ctx)
  const hasErrors = Object.keys(errors).length > 0
  const dirty = mode === 'create' || !sameFormValue(value, initial)
  const shown: Shown = (field) => (submitted || touched.has(field) ? errors[field] : undefined)

  const update: Update = (patch, ...fields) => {
    setValue({ ...value, ...patch })
    if (fields.length > 0) setTouched(new Set([...touched, ...fields]))
  }

  function changeType(type: InstanceType) {
    // Version, loader, modpack and java override belong to the previous type.
    update({ type, version: '', manualVersion: false, loaderVersion: '', modpack: '', javaOverride: '' })
  }

  return (
    <form
      // Issue #86: in a wide container (callers wrap the form in @container/server-form) basics fill the left column and the
      // other sections stack on the right; the last right row (1fr) absorbs the height difference. One column below.
      className="grid gap-6 @4xl/server-form:grid-cols-2 @4xl/server-form:grid-rows-[auto_auto_1fr] @4xl/server-form:items-start"
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        setSubmitted(true)
        const result = resolveServerForm(value, ctx)
        if (result) onSubmit(result)
      }}
    >
      <Section title={t('servers.form.section.basics')} className={LEFT}>
        <div className="flex flex-col gap-4">
          {mode === 'create' ? (
            <>
              <NameField id={`${id}-name`} name={value.name} baseDomain={baseDomain} error={shown('name')} onChange={(name) => update({ name }, 'name')} />
              <TypeField id={`${id}-type`} type={value.type} curseforgeKeySet={curseforgeKeySet} error={shown('type')} onChange={changeType} />
            </>
          ) : (
            <p className="text-sm">
              <span className="font-medium">{t('servers.form.type')}:</span> {t(`servers.type.${value.type}`)}
            </p>
          )}
          <VersionFields
            id={id}
            value={value}
            catalog={catalog.data}
            catalogPending={!modpack && catalog.isPending}
            catalogFailed={catalog.isError}
            shown={shown}
            update={update}
          />
        </div>
      </Section>
      <Section title={t('servers.form.section.resources')} className={RIGHT}>
        <MemoryField
          id={`${id}-memory`}
          text={value.memoryGiB}
          error={shown('memory')}
          limits={memoryLimits.data}
          ownMiB={mode === 'edit' ? (memoryMiBFromGiB(initial.memoryGiB) ?? 0) : 0}
          recommendedMiB={recommendedMiB}
          onChange={(memoryGiB) => update({ memoryGiB }, 'memory')}
        />
      </Section>
      <CollapsibleSection
        className={RIGHT}
        title={t('servers.form.advanced')}
        description={t('servers.form.advanced_description')}
        forceOpen={submitted && (errors.java !== undefined || (value.type === 'neoforge' && errors.loader_version !== undefined))}
      >
        <AdvancedFields id={id} value={value} catalog={catalog.data} shown={shown} update={update} />
      </CollapsibleSection>
      <CollapsibleSection
        className={RIGHT}
        title={t('servers.form.properties')}
        description={t('servers.form.properties_description')}
        forceOpen={submitted && PROPERTY_FIELDS.some((f) => errors[f] !== undefined)}
      >
        <PropertiesFields
          idPrefix={`${id}-prop`}
          mode={mode}
          serverName={value.name.trim()}
          value={value.properties}
          error={shown}
          onChange={(patch, field) => update({ properties: { ...value.properties, ...patch } }, ...(field ? [field] : []))}
        />
      </CollapsibleSection>
      {mode === 'create' ? (
        <EulaField className={FULL} id={`${id}-eula`} checked={value.eulaAccepted} error={shown('eula')} onChange={(eulaAccepted) => update({ eulaAccepted }, 'eula')} />
      ) : null}
      <div className={cn('flex flex-col items-start gap-3', FULL)}>
        {notice ? <p className="text-muted-foreground text-sm">{notice}</p> : null}
        {submitted && hasErrors ? (
          <p role="alert" className="text-destructive text-sm">
            {t('servers.form.fix_errors')}
          </p>
        ) : null}
        {error && !isSudoCancelled(error) ? (
          <p role="alert" className="text-destructive text-sm">
            {errorMessage(error, t)}
          </p>
        ) : null}
        <Button type="submit" disabled={pending || !dirty}>
          {submitLabel}
        </Button>
      </div>
    </form>
  )
}

function NameField({
  id,
  name,
  baseDomain,
  error,
  onChange,
}: {
  id: string
  name: string
  baseDomain: string | null
  error: FieldError | undefined
  onChange(name: string): void
}) {
  const { t } = useTranslation()
  const address = serverAddress(name.trim() || '<name>', baseDomain)
  return (
    <div className="space-y-2">
      <FieldRow id={id} label={t('servers.form.name')} hint={t('servers.form.name_hint')} error={error}>
        {(c) => (
          <Input
            {...c}
            className="sm:w-80"
            value={name}
            autoCapitalize="none"
            autoComplete="off"
            spellCheck={false}
            maxLength={32}
            onChange={(e) => onChange(e.target.value)}
          />
        )}
      </FieldRow>
      <p className="text-sm" aria-live="polite">
        {address ? t('servers.form.address_preview', { address }) : t('servers.form.address_unavailable')}
      </p>
    </div>
  )
}

function TypeField({
  id,
  type,
  curseforgeKeySet,
  error,
  onChange,
}: {
  id: string
  type: InstanceType
  curseforgeKeySet: boolean
  error: FieldError | undefined
  onChange(type: InstanceType): void
}) {
  const { t } = useTranslation()
  return (
    <fieldset className="space-y-2" aria-describedby={error ? `${id}-error` : undefined}>
      <legend className="mb-2 text-sm font-medium">{t('servers.form.type')}</legend>
      {/* Two cards per row unless the form puts basics into a narrow left column. */}
      <div className="grid gap-2 @md/server-form:grid-cols-2 @4xl/server-form:grid-cols-1 @6xl/server-form:grid-cols-2">
        {INSTANCE_TYPES.map((option) => {
          const optionId = `${id}-${option}`
          const disabled = option === 'curseforge' && !curseforgeKeySet
          return (
            <div key={option} className={cn('bg-surface-2 flex items-start gap-3 border p-3', type === option && 'border-primary bevel-inset', disabled && 'opacity-60')}>
              <input
                id={optionId}
                type="radio"
                name={id}
                value={option}
                checked={type === option}
                disabled={disabled}
                aria-describedby={`${optionId}-description`}
                className="accent-primary mt-0.5 size-4"
                onChange={() => onChange(option)}
              />
              <div className="space-y-0.5">
                <Label htmlFor={optionId}>{t(`servers.type.${option}`)}</Label>
                <p id={`${optionId}-description`} className="text-muted-foreground text-sm">
                  {t(`servers.type_description.${option}`)}
                </p>
              </div>
            </div>
          )
        })}
      </div>
      {curseforgeKeySet ? null : (
        <p className="text-muted-foreground text-sm">
          {t('servers.form.curseforge_key_hint')}{' '}
          <Link to="/settings" className="text-primary underline underline-offset-4">
            {t('settings.link')}
          </Link>
        </p>
      )}
      {error ? (
        <p id={`${id}-error`} className="text-destructive text-sm">
          {t(error.key, error.params ?? {})}
        </p>
      ) : null}
    </fieldset>
  )
}

interface VersionFieldsProps {
  id: string
  value: ServerFormValue
  catalog: Catalog | undefined
  catalogPending: boolean
  catalogFailed: boolean
  shown: Shown
  update: Update
}

function VersionFields({ id, value, catalog, catalogPending, catalogFailed, shown, update }: VersionFieldsProps) {
  const { t } = useTranslation()
  if (isModpack(value.type)) {
    const modrinth = value.type === 'modrinth'
    return (
      <FieldRow
        id={`${id}-modpack`}
        label={modrinth ? t('servers.form.modpack_modrinth') : t('servers.form.modpack_curseforge')}
        hint={modrinth ? t('servers.form.modpack_modrinth_hint') : t('servers.form.modpack_curseforge_hint')}
        error={shown('modpack')}
      >
        {(c) => (
          <Input
            {...c}
            value={value.modpack}
            autoCapitalize="none"
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => update({ modpack: e.target.value }, 'modpack')}
          />
        )}
      </FieldRow>
    )
  }

  const versions = catalog?.versions ?? []
  // A failed or empty catalog turns the version into free text (spec §6: 502 → Freitext).
  const listAvailable = !catalogFailed && (catalogPending || versions.length > 0)
  const current = effectiveVersion(value, catalog)
  const listed = current === 'LATEST' || versions.some((v) => v.id === current)
  const useList = listAvailable && !value.manualVersion && (catalogPending || value.version === '' || listed)
  let hint = t('servers.form.version_manual_hint')
  if (useList) hint = t('servers.form.version_hint')
  else if (!listAvailable) hint = t('servers.form.version_catalog_failed')
  const loaders = catalog?.loaders ?? []
  const loaderOptions = value.loaderVersion !== '' && !loaders.includes(value.loaderVersion) ? [value.loaderVersion, ...loaders] : loaders

  return (
    <div className="space-y-4">
      <div className="space-y-1">
        <FieldRow id={`${id}-version`} label={t('servers.form.version')} hint={hint} error={shown('version')}>
          {(c) =>
            useList ? (
              <NativeSelect
                {...c}
                className="w-full sm:w-64"
                value={catalogPending ? '' : current}
                disabled={catalogPending}
                onChange={(e) => update({ version: e.target.value }, 'version')}
              >
                {catalogPending ? <option value="">{t('servers.form.version_loading')}</option> : null}
                {versions.map((v) => (
                  <option key={v.id} value={v.id}>
                    {v.id}
                  </option>
                ))}
                <option value="LATEST">{t('servers.form.version_latest')}</option>
              </NativeSelect>
            ) : (
              <Input
                {...c}
                className="sm:w-64"
                value={value.version}
                autoComplete="off"
                spellCheck={false}
                placeholder="1.21.11"
                onChange={(e) => update({ version: e.target.value }, 'version')}
              />
            )
          }
        </FieldRow>
        {listAvailable && !catalogPending ? (
          <Button
            type="button"
            variant="link"
            size="sm"
            className="h-auto px-0"
            onClick={() => (useList ? update({ manualVersion: true, version: current }) : update({ manualVersion: false, version: '' }))}
          >
            {useList ? t('servers.form.version_manual') : t('servers.form.version_from_list')}
          </Button>
        ) : null}
      </div>
      {value.type === 'fabric' ? (
        <FieldRow id={`${id}-loader`} label={t('servers.form.fabric_loader')} error={shown('loader_version')}>
          {(c) => (
            <NativeSelect {...c} className="w-full sm:w-64" value={value.loaderVersion} onChange={(e) => update({ loaderVersion: e.target.value }, 'loader_version')}>
              <option value="">{t('servers.form.loader_latest')}</option>
              {loaderOptions.map((loader) => (
                <option key={loader} value={loader}>
                  {loader}
                </option>
              ))}
            </NativeSelect>
          )}
        </FieldRow>
      ) : null}
    </div>
  )
}

interface MemoryFieldProps {
  id: string
  text: string
  error: FieldError | undefined
  /** Host limits from GET /servers/memory; undefined = static 1..32 GiB, no host hints. */
  limits: ServerMemoryLimits | undefined
  /** Memory this server already counts in limits.allocated_mib (edit), 0 for a new one. */
  ownMiB: number
  /** create only: the suggested size for the chosen type; null = no size fits the free budget safely */
  recommendedMiB: number | null | undefined
  onChange(text: string): void
}

export function MemoryField({ id, text, error, limits, ownMiB, recommendedMiB, onChange }: MemoryFieldProps) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const bounds: MemoryLimits = memoryLimitsFrom(limits)
  const gib = (mib: number) => new Intl.NumberFormat(fmt.locale, { maximumFractionDigits: 2 }).format(mib / 1024)
  const mib = memoryMiBFromGiB(text)
  const fallback = Math.min(Math.max(recommendedMiB ?? ownMiB, bounds.min), bounds.max)
  const slider = mib !== null && mib >= bounds.min && mib <= bounds.max ? mib / 1024 : fallback / 1024
  const range = { min: gib(bounds.min), max: gib(bounds.max) }
  let hint = t('servers.form.memory_hint', range)
  if (limits?.host_total_mib != null) {
    hint = t('servers.form.memory_hint_host', { ...range, host: gib(limits.host_total_mib), reserve: gib(limits.host_reserve_mib), overhead: limits.container_overhead_percent })
  } else if (limits) {
    hint = t('servers.form.memory_hint_overhead', { ...range, overhead: limits.container_overhead_percent })
  }
  const overcommit = memoryOvercommit(mib, limits, ownMiB)
  // The range in the error follows the locale like the hint (11,5 instead of 11.5).
  const shownError = error?.key === 'servers.validation.memory' ? { ...error, params: range } : error
  return (
    <div className="space-y-2">
      <FieldRow id={id} label={t('servers.form.memory')} hint={hint} error={shownError}>
        {(c) => (
          <div className="flex items-center gap-3">
            <input
              type="range"
              min={bounds.min / 1024}
              max={bounds.max / 1024}
              step={bounds.step / 1024}
              value={slider}
              aria-label={t('servers.form.memory_slider')}
              aria-valuetext={`${slider} GiB`}
              className="accent-primary h-2 min-w-0 flex-1"
              onChange={(e) => onChange(e.target.value)}
            />
            <Input
              {...c}
              type="number"
              inputMode="decimal"
              min={bounds.min / 1024}
              max={bounds.max / 1024}
              step={bounds.step / 1024}
              className="w-24"
              value={text}
              onChange={(e) => onChange(e.target.value)}
            />
          </div>
        )}
      </FieldRow>
      {recommendedMiB != null ? <p className="text-muted-foreground text-sm">{t('servers.form.memory_recommended', { value: gib(recommendedMiB) })}</p> : null}
      {recommendedMiB === null ? (
        <p role="note" className="text-status-starting text-sm">
          {t('servers.form.memory_no_recommendation', { min: gib(bounds.min) })}
        </p>
      ) : null}
      {overcommit ? (
        <p role="status" className="text-status-starting text-sm">
          {t('servers.form.memory_overcommit', {
            total: gib(overcommit.totalMiB),
            budget: gib(overcommit.budgetMiB),
            host: gib(overcommit.hostMiB),
            reserve: gib(overcommit.reserveMiB),
          })}
        </p>
      ) : null}
    </div>
  )
}

function AdvancedFields({
  id,
  value,
  catalog,
  shown,
  update,
}: {
  id: string
  value: ServerFormValue
  catalog: Catalog | undefined
  shown: Shown
  update: Update
}) {
  const { t } = useTranslation()
  const derived = effectiveJava({ ...value, javaOverride: '' }, catalog)
  return (
    <>
      <FieldRow id={`${id}-java`} label={t('servers.form.java')} hint={t('servers.form.java_hint')} error={shown('java')}>
        {(c) => (
          <NativeSelect
            {...c}
            className="w-full sm:w-64"
            value={value.javaOverride}
            onChange={(e) => update({ javaOverride: e.target.value as JavaVersion | '' }, 'java')}
          >
            <option value="">{t('servers.form.java_auto', { java: javaLabel(derived) })}</option>
            {JAVA_VERSIONS.map((java) => (
              <option key={java} value={java}>
                {javaLabel(java)}
              </option>
            ))}
          </NativeSelect>
        )}
      </FieldRow>
      {value.type === 'neoforge' ? (
        <FieldRow id={`${id}-neoforge`} label={t('servers.form.neoforge_version')} hint={t('servers.form.neoforge_version_hint')} error={shown('loader_version')}>
          {(c) => (
            <Input
              {...c}
              className="sm:w-64"
              value={value.loaderVersion}
              autoComplete="off"
              spellCheck={false}
              placeholder="21.11.38"
              onChange={(e) => update({ loaderVersion: e.target.value }, 'loader_version')}
            />
          )}
        </FieldRow>
      ) : null}
    </>
  )
}

function EulaField({
  id,
  checked,
  error,
  className,
  onChange,
}: {
  id: string
  checked: boolean
  error: FieldError | undefined
  className?: string
  onChange(checked: boolean): void
}) {
  const { t } = useTranslation()
  return (
    <div className={cn('space-y-1', className)}>
      <div className="flex items-start gap-2">
        <input
          id={id}
          type="checkbox"
          className="accent-primary mt-0.5 size-4"
          checked={checked}
          aria-invalid={error ? true : undefined}
          aria-describedby={`${id}-message`}
          onChange={(e) => onChange(e.target.checked)}
        />
        <Label htmlFor={id} className="leading-snug">
          {t('servers.form.eula')}
        </Label>
      </div>
      <p id={`${id}-message`} className={cn('text-sm', error ? 'text-destructive' : 'text-muted-foreground')}>
        {error ? t(error.key, error.params ?? {}) : t('servers.form.eula_hint')}{' '}
        <a href={EULA_URL} target="_blank" rel="noreferrer" className="text-primary underline underline-offset-4">
          {t('servers.form.eula_link')}
        </a>
      </p>
    </div>
  )
}
