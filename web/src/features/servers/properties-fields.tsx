import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { Difficulty, Gamemode, InstanceType, LevelType } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { FieldRow } from './field-row'
import { isLowLoadDistances, withLowLoadDistances, type PropertiesDraft, type PropertyField } from './form-model'
import { DIFFICULTIES, GAMEMODES, LEVEL_TYPES, type FieldError } from './validation'

type SwitchKey = 'pvp' | 'hardcore' | 'allow_flight' | 'enable_command_block' | 'online_mode' | 'whitelist_enabled'
type NumberKey = 'max_players' | 'spawn_protection' | 'view_distance' | 'simulation_distance'

const SWITCHES: readonly SwitchKey[] = ['pvp', 'hardcore', 'allow_flight', 'enable_command_block', 'online_mode', 'whitelist_enabled']

const NUMBERS: readonly { key: NumberKey; field: PropertyField; min: number; max: number }[] = [
  { key: 'max_players', field: 'properties.max_players', min: 1, max: 500 },
  { key: 'spawn_protection', field: 'properties.spawn_protection', min: 0, max: 256 },
  { key: 'view_distance', field: 'properties.view_distance', min: 3, max: 32 },
  { key: 'simulation_distance', field: 'properties.simulation_distance', min: 3, max: 32 },
]

export interface PropertiesFieldsProps {
  idPrefix: string
  mode: 'create' | 'edit'
  serverName: string
  /** Decides whether plugins, mods or packs may override the distances (issue #159). */
  type: InstanceType
  value: PropertiesDraft
  error(field: PropertyField): FieldError | undefined
  onChange(patch: Partial<PropertiesDraft>, ...fields: PropertyField[]): void
}

export function PropertiesFields({ idPrefix, mode, serverName, type, value, error, onChange }: PropertiesFieldsProps) {
  const { t } = useTranslation()
  return (
    <div className="space-y-4">
      <FieldRow
        id={`${idPrefix}-motd`}
        label={t('servers.props.motd')}
        hint={mode === 'create' ? t('servers.props.motd_hint_create') : undefined}
        error={error('properties.motd')}
      >
        {(c) => (
          <Input
            {...c}
            value={value.motd}
            autoComplete="off"
            placeholder={mode === 'create' && serverName !== '' ? serverName : undefined}
            onChange={(e) => onChange({ motd: e.target.value }, 'properties.motd')}
          />
        )}
      </FieldRow>
      <div className="grid gap-4 sm:grid-cols-2">
        <FieldRow id={`${idPrefix}-difficulty`} label={t('servers.props.difficulty')}>
          {(c) => (
            <NativeSelect {...c} className="w-full" value={value.difficulty} onChange={(e) => onChange({ difficulty: e.target.value as Difficulty })}>
              {DIFFICULTIES.map((d) => (
                <option key={d} value={d}>
                  {t(`servers.difficulty.${d}`)}
                </option>
              ))}
            </NativeSelect>
          )}
        </FieldRow>
        <FieldRow id={`${idPrefix}-gamemode`} label={t('servers.props.gamemode')}>
          {(c) => (
            <NativeSelect {...c} className="w-full" value={value.gamemode} onChange={(e) => onChange({ gamemode: e.target.value as Gamemode })}>
              {GAMEMODES.map((g) => (
                <option key={g} value={g}>
                  {t(`servers.gamemode.${g}`)}
                </option>
              ))}
            </NativeSelect>
          )}
        </FieldRow>
        {NUMBERS.map(({ key, field, min, max }) => (
          <FieldRow key={key} id={`${idPrefix}-${key}`} label={t(`servers.props.${key}`)} error={error(field)}>
            {(c) => (
              <Input
                {...c}
                type="number"
                inputMode="numeric"
                min={min}
                max={max}
                step={1}
                value={value[key]}
                onChange={(e) => {
                  const patch: Partial<PropertiesDraft> = {}
                  patch[key] = e.target.value
                  onChange(patch, field)
                }}
              />
            )}
          </FieldRow>
        ))}
      </div>
      <DistancePreset type={type} value={value} onChange={onChange} />
      <div className="grid gap-3 sm:grid-cols-2">
        {SWITCHES.map((key) => (
          <div key={key} className="flex items-center justify-between gap-3 rounded-md border px-3 py-2">
            <Label htmlFor={`${idPrefix}-${key}`}>{t(`servers.props.${key}`)}</Label>
            <Switch
              id={`${idPrefix}-${key}`}
              checked={value[key]}
              onCheckedChange={(checked) => {
                const patch: Partial<PropertiesDraft> = {}
                patch[key] = checked
                onChange(patch)
              }}
            />
          </div>
        ))}
      </div>
      {value.online_mode ? null : <p className="text-destructive text-sm">{t('servers.props.online_mode_warning')}</p>}
      <p className="text-muted-foreground text-sm">{t('servers.props.lists_hint')}</p>
      <p className="text-muted-foreground text-sm">{t('servers.props.world_hint')}</p>
      <div className="grid gap-4 sm:grid-cols-2">
        <FieldRow id={`${idPrefix}-seed`} label={t('servers.props.seed')} error={error('properties.seed')}>
          {(c) => <Input {...c} value={value.seed} autoComplete="off" spellCheck={false} onChange={(e) => onChange({ seed: e.target.value }, 'properties.seed')} />}
        </FieldRow>
        <FieldRow id={`${idPrefix}-level-type`} label={t('servers.props.level_type')}>
          {(c) => (
            <NativeSelect {...c} className="w-full" value={value.level_type} onChange={(e) => onChange({ level_type: e.target.value as LevelType })}>
              {LEVEL_TYPES.map((l) => (
                <option key={l} value={l}>
                  {t(`servers.level_type.${l}`)}
                </option>
              ))}
            </NativeSelect>
          )}
        </FieldRow>
      </div>
    </div>
  )
}

/**
 * Issue #159: fills view distance 8 / simulation distance 6 into the fields above, only after the gameplay tradeoff was
 * shown and confirmed. Nothing changes on its own: defaults stay 10/10, stored values stay until the user saves.
 */
function DistancePreset({ type, value, onChange }: { type: InstanceType; value: PropertiesDraft; onChange: PropertiesFieldsProps['onChange'] }) {
  const { t } = useTranslation()
  const id = useId()
  const [confirming, setConfirming] = useState(false)
  const active = isLowLoadDistances(value)
  return (
    <div role="group" aria-labelledby={`${id}-title`} className="bevel-inset space-y-2 px-3 py-2 text-sm">
      <p id={`${id}-title`} className="font-medium">
        {t('servers.props.distance_preset.title')}
      </p>
      <p className="text-muted-foreground">{t('servers.props.distance_preset.description')}</p>
      {active ? (
        <p className="font-medium">{t('servers.props.distance_preset.active')}</p>
      ) : confirming ? (
        <div className="space-y-2">
          <p className="font-medium">{t('servers.props.distance_preset.tradeoff_title')}</p>
          <ul className="list-disc space-y-1 pl-5">
            <li>{t('servers.props.distance_preset.tradeoff_view')}</li>
            <li>{t('servers.props.distance_preset.tradeoff_simulation')}</li>
            <li>{t('servers.props.distance_preset.tradeoff_restart')}</li>
          </ul>
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              size="sm"
              onClick={() => {
                onChange(withLowLoadDistances(value), 'properties.view_distance', 'properties.simulation_distance')
                setConfirming(false)
              }}
            >
              {t('servers.props.distance_preset.apply')}
            </Button>
            <Button type="button" size="sm" variant="outline" onClick={() => setConfirming(false)}>
              {t('common.cancel')}
            </Button>
          </div>
        </div>
      ) : (
        <Button type="button" size="sm" variant="outline" onClick={() => setConfirming(true)}>
          {t('servers.props.distance_preset.choose')}
        </Button>
      )}
      {type === 'vanilla' ? null : <p className="text-muted-foreground">{t('servers.props.distance_preset.override_hint')}</p>}
    </div>
  )
}
