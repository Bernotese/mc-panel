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
})
