import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { LoginBarChart, niceMax } from './bar-chart'

const labels = { time: 'Zeit', success: 'Erfolgreich', failure: 'Fehlgeschlagen' }
const data = [
  { t: 1, success: 0, failure: 10 },
  { t: 2, success: 5, failure: 0 },
  { t: 3, success: 3, failure: 2 },
]

describe('niceMax', () => {
  it('rounds up to 1/2/5 × 10^n', () => {
    expect([0, 7, 10, 12, 45, 100, 101].map(niceMax)).toEqual([1, 10, 10, 20, 50, 100, 200])
  })
})

describe('LoginBarChart', () => {
  it('exposes title and totals to assistive technology', () => {
    render(<LoginBarChart data={data} title="Login-Versuche" formatTick={(t) => `T${t}`} labels={labels} />)
    expect(screen.getByRole('img', { name: 'Login-Versuche Erfolgreich: 8, Fehlgeschlagen: 12' })).toBeInTheDocument()
    const table = screen.getByRole('table', { name: 'Login-Versuche' })
    expect(within(table).getAllByRole('row')).toHaveLength(4)
  })

  it('scales stacked bars against a nice maximum with a 2px gap', () => {
    render(<LoginBarChart data={data} title="Login-Versuche" formatTick={(t) => `T${t}`} labels={labels} />)
    const bars = screen.getAllByTestId('chart-bar')
    expect(bars).toHaveLength(3)
    expect(bars[0].querySelector('[data-series="failure"]')).toHaveAttribute('height', '180')
    expect(bars[0].querySelector('[data-series="success"]')).toBeNull()
    expect(bars[1].querySelector('[data-series="success"]')).toHaveAttribute('height', '90')
    expect(bars[2].querySelector('[data-series="failure"]')).toHaveAttribute('y', '100')
  })

  it('shows a legend for both series', () => {
    render(<LoginBarChart data={data} title="Login-Versuche" formatTick={(t) => `T${t}`} labels={labels} />)
    const legend = screen.getByTestId('chart-legend')
    expect(within(legend).getByText('Erfolgreich')).toBeInTheDocument()
    expect(within(legend).getByText('Fehlgeschlagen')).toBeInTheDocument()
  })
})
