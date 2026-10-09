import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { setViewport } from '@/test/media'
import { DataList, type Column } from './data-list'

interface Row { id: number; name: string; ip: string }
const rows: Row[] = [
  { id: 1, name: 'admin', ip: '203.0.113.5' },
  { id: 2, name: 'alice', ip: '198.51.100.23' },
]
const columns: Column<Row>[] = [
  { id: 'name', header: 'Name', cell: (r) => r.name },
  { id: 'ip', header: 'IP', cell: (r) => r.ip },
]

describe('DataList', () => {
  it('renders a table with caption on desktop', () => {
    render(<DataList items={rows} columns={columns} rowKey={(r) => r.id} caption="Benutzer" empty="Leer" />)
    const table = screen.getByRole('table', { name: 'Benutzer' })
    expect(within(table).getAllByRole('row')).toHaveLength(3)
    expect(within(table).getByRole('columnheader', { name: 'IP' })).toBeInTheDocument()
  })

  it('collapses into labelled cards on a 360px viewport', () => {
    setViewport('mobile')
    render(<DataList items={rows} columns={columns} rowKey={(r) => r.id} caption="Benutzer" empty="Leer" />)
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    const list = screen.getByRole('list', { name: 'Benutzer' })
    const cards = within(list).getAllByRole('listitem')
    expect(cards).toHaveLength(2)
    expect(within(cards[1]).getByText('IP')).toBeInTheDocument()
    expect(within(cards[1]).getByText('198.51.100.23')).toBeInTheDocument()
  })

  it('shows the empty text when there are no rows', () => {
    render(<DataList items={[]} columns={columns} rowKey={(r: Row) => r.id} caption="Benutzer" empty="Keine Einträge" />)
    expect(screen.getByText('Keine Einträge')).toBeInTheDocument()
  })
})
