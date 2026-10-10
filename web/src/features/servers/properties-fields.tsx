import { useTranslation } from 'react-i18next'
import type { Difficulty, Gamemode, LevelType } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { FieldRow } from './field-row'
import type { PropertiesDraft, PropertyField } from './form-model'
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

// Same look as the shadcn Input; there is no shadcn textarea in this project.
const TEXTAREA_CLASS =
  'border-input focus-visible:border-ring focus-visible:ring-ring/50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:bg-input/30 min-h-20 w-full rounded-lg border bg-transparent px-2.5 py-1.5 text-base outline-none focus-visible:ring-3 aria-invalid:ring-3 md:text-sm'

export interface PropertiesFieldsProps {
  idPrefix: string
  mode: 'create' | 'edit'
  serverName: string
  value: PropertiesDraft
  error(field: PropertyField): FieldError | undefined
  onChange(patch: Partial<PropertiesDraft>, field?: PropertyField): void
}

export function PropertiesFields({ idPrefix, mode, serverName, value, error, onChange }: PropertiesFieldsProps) {
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
      <div className="grid gap-4 sm:grid-cols-2">
        <FieldRow id={`${idPrefix}-whitelist`} label={t('servers.props.whitelist')} hint={t('servers.props.whitelist_hint')} error={error('properties.whitelist')}>
          {(c) => (
            <textarea
              {...c}
              rows={3}
              className={TEXTAREA_CLASS}
              spellCheck={false}
              value={value.whitelist}
              onChange={(e) => onChange({ whitelist: e.target.value }, 'properties.whitelist')}
            />
          )}
        </FieldRow>
        <FieldRow id={`${idPrefix}-ops`} label={t('servers.props.ops')} hint={t('servers.props.ops_hint')} error={error('properties.ops')}>
          {(c) => (
            <textarea
              {...c}
              rows={3}
              className={TEXTAREA_CLASS}
              spellCheck={false}
              value={value.ops}
              onChange={(e) => onChange({ ops: e.target.value }, 'properties.ops')}
            />
          )}
        </FieldRow>
      </div>
      <p className="text-muted-foreground text-sm">{t('servers.props.lists_sync_hint')}</p>
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
