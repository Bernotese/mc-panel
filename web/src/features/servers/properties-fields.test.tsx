import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import type { InstanceType } from '@/api/types'
import { defaultPropertiesDraft, type PropertiesDraft, type PropertyField } from './form-model'
import { PropertiesFields } from './properties-fields'

const VIEW = 'Sichtweite (Chunks)'
const SIMULATION = 'Simulationsweite (Chunks)'

function Harness({ type, initial, touched }: { type: InstanceType; initial?: Partial<PropertiesDraft>; touched: PropertyField[] }) {
  const [value, setValue] = useState<PropertiesDraft>({ ...defaultPropertiesDraft(), ...initial })
  return (
    <PropertiesFields
      idPrefix="p"
      mode="edit"
      serverName="survival"
      type={type}
      value={value}
      error={() => undefined}
      onChange={(patch, ...fields) => {
        setValue((v) => ({ ...v, ...patch }))
        touched.push(...fields)
      }}
    />
  )
}

function setup(type: InstanceType = 'vanilla', initial?: Partial<PropertiesDraft>) {
  const touched: PropertyField[] = []
  render(<Harness type={type} initial={initial} touched={touched} />)
  return { user: userEvent.setup(), touched, preset: screen.getByRole('group', { name: 'Weniger Last' }) }
}

describe('lower-load distance preset (issue #159)', () => {
  it('shows the gameplay tradeoff before anything changes and applies 8/6 only on confirmation', async () => {
    const { user, touched, preset } = setup()
    expect(screen.getByLabelText(VIEW)).toHaveValue(10)
    expect(within(preset).queryByText(/Farmen, Redstone und Mobs/)).not.toBeInTheDocument()

    await user.click(within(preset).getByRole('button', { name: 'Weniger Last übernehmen …' }))
    expect(within(preset).getByText(/Entferntes Gelände verschwindet früher/)).toBeInTheDocument()
    expect(within(preset).getByText(/Farmen, Redstone und Mobs still/)).toBeInTheDocument()
    expect(screen.getByLabelText(VIEW)).toHaveValue(10)

    await user.click(within(preset).getByRole('button', { name: 'Abbrechen' }))
    expect(within(preset).queryByText(/Farmen, Redstone und Mobs/)).not.toBeInTheDocument()
    expect([screen.getByLabelText(VIEW), screen.getByLabelText(SIMULATION)].map((i) => (i as HTMLInputElement).value)).toEqual(['10', '10'])

    await user.click(within(preset).getByRole('button', { name: 'Weniger Last übernehmen …' }))
    await user.click(within(preset).getByRole('button', { name: 'Übernehmen' }))
    expect(screen.getByLabelText(VIEW)).toHaveValue(8)
    expect(screen.getByLabelText(SIMULATION)).toHaveValue(6)
    expect(touched).toEqual(expect.arrayContaining(['properties.view_distance', 'properties.simulation_distance']))
    expect(within(preset).getByText('Aktiv: Sichtweite 8, Simulationsweite 6.')).toBeInTheDocument()
    expect(within(preset).queryByRole('button', { name: 'Weniger Last übernehmen …' })).not.toBeInTheDocument()

    // The fields stay the source of truth: still editable after the preset.
    await user.clear(screen.getByLabelText(SIMULATION))
    await user.type(screen.getByLabelText(SIMULATION), '7')
    expect(screen.getByLabelText(SIMULATION)).toHaveValue(7)
    expect(within(preset).getByRole('button', { name: 'Weniger Last übernehmen …' })).toBeInTheDocument()
  })

  it('says that plugins, mods and packs may override the distances, except on vanilla', () => {
    setup('vanilla')
    expect(screen.queryByText(/können die Weiten in ihren eigenen Einstellungen/)).not.toBeInTheDocument()
  })

  it.each(['paper', 'fabric', 'modrinth'] as const)('shows the override note for %s', (type) => {
    const { preset } = setup(type)
    expect(within(preset).getByText(/können die Weiten in ihren eigenen Einstellungen/)).toBeInTheDocument()
  })
})
