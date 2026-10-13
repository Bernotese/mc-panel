import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { LineChart, type ChartSeries } from './line-chart'
import type { ChartPoint } from './thin'

const STEP = 30_000
function pts(values: (number | null)[]): ChartPoint[] {
  return values.flatMap((v, i) => (v === null ? [] : [{ t: i * STEP, v, min: v, max: v }]))
}
const base = {
  title: 'Spieler',
  from: 0,
  to: 9 * STEP,
  stepMs: STEP,
  formatValue: (v: number) => String(Math.round(v * 10) / 10),
  formatTime: (t: number) => `T${t / STEP}`,
}
const players = (values: (number | null)[]): ChartSeries => ({ id: 'players', label: 'Spieler', tone: 'primary', points: pts(values) })

describe('LineChart', () => {
  it('summarises each series for assistive technology', () => {
    render(<LineChart {...base} series={[players([1, 5, 3])]} />)
    const chart = screen.getByRole('img', { name: 'Spieler' })
    expect(chart).toHaveAccessibleDescription(/Spieler: aktuell 3, Minimum 1, Maximum 5/)
  })

  it('shows a hint instead of an empty chart', () => {
    render(<LineChart {...base} series={[players([])]} />)
    expect(screen.getByText('Keine Daten in diesem Zeitraum.')).toBeInTheDocument()
    expect(screen.queryByRole('img')).not.toBeInTheDocument()
  })

  it('draws one line per segment and never bridges a gap', () => {
    const { container } = render(<LineChart {...base} series={[players([1, 2, null, null, null, 4, 5])]} />)
    expect(container.querySelectorAll('[data-testid="chart-line"][data-series="players"]')).toHaveLength(2)
  })

  it('marks a single isolated point with a dot', () => {
    const { container } = render(<LineChart {...base} series={[players([null, null, null, null, 7])]} />)
    expect(container.querySelectorAll('[data-testid="chart-dot"][data-series="players"]')).toHaveLength(1)
  })

  it('walks through the values with the keyboard and announces them', async () => {
    const user = userEvent.setup()
    render(<LineChart {...base} series={[players([1, 5, 3])]} />)
    const chart = screen.getByRole('img', { name: 'Spieler' })
    chart.focus()
    await user.keyboard('{End}')
    expect(screen.getByRole('status')).toHaveTextContent('T2 – Spieler: 3')
    await user.keyboard('{ArrowLeft}')
    expect(screen.getByRole('status')).toHaveTextContent('T1 – Spieler: 5')
    await user.keyboard('{Home}')
    expect(screen.getByRole('status')).toHaveTextContent('T0 – Spieler: 1')
    await user.keyboard('{Escape}')
    expect(screen.getByRole('status')).toHaveTextContent('')
  })

  it('follows the mouse to the nearest value', () => {
    render(<LineChart {...base} series={[players([1, 5, 3, 4, 4, 4, 4, 4, 4, 4])]} />)
    const chart = screen.getByRole('img', { name: 'Spieler' })
    vi.spyOn(chart, 'getBoundingClientRect').mockReturnValue({ left: 0, top: 0, width: 640, height: 200, right: 640, bottom: 200, x: 0, y: 0, toJSON: () => ({}) })
    // Inner width 640 - 48 - 12 = 580; t = 1 step sits at 48 + 580 / 9 ≈ 112.
    fireEvent.mouseMove(chart, { clientX: 112, clientY: 50 })
    expect(screen.getByRole('status')).toHaveTextContent('T1 – Spieler: 5')
    fireEvent.mouseLeave(chart)
    expect(screen.getByRole('status')).toHaveTextContent('')
  })

  it('offers the values as a table', async () => {
    const user = userEvent.setup()
    render(<LineChart {...base} series={[players([1, 5, 3])]} />)
    await user.click(screen.getByRole('button', { name: 'Als Tabelle anzeigen' }))
    const table = screen.getByRole('table', { name: 'Spieler' })
    expect(within(table).getAllByRole('row')).toHaveLength(4)
    expect(within(table).getByRole('columnheader', { name: 'Zeit' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Tabelle ausblenden' }))
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('puts a second series on a right axis', () => {
    const series: ChartSeries[] = [
      { id: 'tps', label: 'TPS', tone: 'primary', points: pts([20, 19.5]) },
      { id: 'mspt', label: 'MSPT', tone: 'secondary', axis: 'right', points: pts([3, 40]) },
    ]
    const { container } = render(<LineChart {...base} title="TPS und MSPT" series={series} />)
    expect(container.querySelectorAll('[data-axis="right"]').length).toBeGreaterThan(0)
    expect(container.querySelectorAll('[data-testid="chart-line"]')).toHaveLength(2)
  })

  it('draws a limit line and includes it in the scale', () => {
    const { container } = render(<LineChart {...base} series={[players([1, 2])]} limit={{ value: 10, label: 'Limit' }} />)
    expect(container.querySelector('[data-testid="chart-limit"]')).not.toBeNull()
    expect(Array.from(container.querySelectorAll('[data-axis="left"]')).map((n) => n.textContent)).toContain('10')
  })

  it('fills the min–max band for averaged data', () => {
    const series: ChartSeries[] = [{ id: 'tps', label: 'TPS', tone: 'primary', points: [{ t: 0, v: 19, min: 15, max: 20 }, { t: STEP, v: 19.5, min: 19, max: 20 }] }]
    const { container } = render(<LineChart {...base} series={series} band />)
    expect(container.querySelector('[data-testid="chart-band"]')).not.toBeNull()
  })

  it('steps a whole-number axis by at least 1', () => {
    const { container } = render(<LineChart {...base} series={[players([0, 1, 0])]} integer />)
    expect(Array.from(container.querySelectorAll('[data-axis="left"]')).map((n) => n.textContent)).toEqual(['0', '1'])
  })

  it('derives the tick decimals from the step', () => {
    const fmt = (v: number, _axis: string, digits?: number) => v.toFixed(digits ?? 1)
    const cpu: ChartSeries = { id: 'cpu', label: 'CPU', tone: 'primary', points: pts([0.01, 0.08, 0.03]) }
    const { container } = render(<LineChart {...base} formatValue={fmt} series={[cpu]} />)
    expect(Array.from(container.querySelectorAll('[data-axis="left"]')).map((n) => n.textContent)).toEqual(['0.00', '0.02', '0.04', '0.06', '0.08'])
  })

  it('names min and max of the band in the summary and the table', async () => {
    const user = userEvent.setup()
    const series: ChartSeries[] = [{ id: 'tps', label: 'TPS', tone: 'primary', points: [{ t: 0, v: 19, min: 15, max: 20 }, { t: STEP, v: 19.5, min: 19, max: 20 }] }]
    render(<LineChart {...base} series={series} band />)
    expect(screen.getByRole('img', { name: 'Spieler' })).toHaveAccessibleDescription(/TPS: aktuell 19.5, Minimum 15, Maximum 20/)
    await user.click(screen.getByRole('button', { name: 'Als Tabelle anzeigen' }))
    expect(screen.getByRole('table', { name: 'Spieler' })).toHaveTextContent('19 (15 – 20)')
  })

  it('leaves out points outside the window', () => {
    const series: ChartSeries[] = [{ id: 'players', label: 'Spieler', tone: 'primary', points: [{ t: -5 * STEP, v: 99, min: 99, max: 99 }, ...pts([1, 2])] }]
    render(<LineChart {...base} series={series} />)
    expect(screen.getByRole('img', { name: 'Spieler' })).toHaveAccessibleDescription(/Maximum 2/)
  })

  it('announces the readout only for keyboard use', async () => {
    const user = userEvent.setup()
    render(<LineChart {...base} series={[players([1, 5, 3])]} />)
    const chart = screen.getByRole('img', { name: 'Spieler' })
    vi.spyOn(chart, 'getBoundingClientRect').mockReturnValue({ left: 0, top: 0, width: 640, height: 200, right: 640, bottom: 200, x: 0, y: 0, toJSON: () => ({}) })
    fireEvent.mouseMove(chart, { clientX: 112, clientY: 50 })
    expect(screen.getByRole('status')).toHaveAttribute('aria-live', 'off')
    chart.focus()
    await user.keyboard('{End}')
    expect(screen.getByRole('status')).toHaveAttribute('aria-live', 'polite')
  })

  it('shows a custom text when there is no data', () => {
    render(<LineChart {...base} series={[players([])]} emptyText="TPS für diese Version nicht verfügbar" />)
    expect(screen.getByText('TPS für diese Version nicht verfügbar')).toBeInTheDocument()
  })
})
