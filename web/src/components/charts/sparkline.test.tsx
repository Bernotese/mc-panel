import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Sparkline } from './sparkline'

describe('Sparkline', () => {
  it('scales from zero to the maximum', () => {
    render(<Sparkline points={[[0, 0], [1, 10]]} label="Spieler" />)
    const svg = screen.getByRole('img', { name: 'Spieler' })
    expect(svg.querySelector('path')).toHaveAttribute('d', 'M0,23 L100,1')
  })

  it('renders nothing for fewer than two points', () => {
    const { container } = render(<Sparkline points={[[0, 3]]} label="Spieler" />)
    expect(container).toBeEmptyDOMElement()
  })

  it('keeps a flat zero line at the bottom', () => {
    render(<Sparkline points={[[0, 0], [5, 0]]} label="Spieler" />)
    expect(screen.getByRole('img', { name: 'Spieler' }).querySelector('path')).toHaveAttribute('d', 'M0,23 L100,23')
  })

  it('does not bridge gaps', () => {
    render(<Sparkline points={[[0, 1], [30_000, 1], [300_000, 1], [330_000, 1]]} label="Spieler" />)
    const d = screen.getByRole('img', { name: 'Spieler' }).querySelector('path')?.getAttribute('d') ?? ''
    expect(d.match(/M/g)).toHaveLength(2)
  })

  it('spans the given period up to the newest point', () => {
    render(<Sparkline points={[[3_000_000, 0], [3_600_000, 10]]} label="Spieler" spanMs={3_600_000} stepMs={600_000} />)
    expect(screen.getByRole('img', { name: 'Spieler' }).querySelector('path')).toHaveAttribute('d', 'M83.33,23 L100,1')
  })
})
