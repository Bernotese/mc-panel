import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Badge } from './badge'
import { Button } from './button'

function cls(el: Element): string[] {
  return (el.getAttribute('class') ?? '').split(/\s+/)
}

describe('Button variants', () => {
  it('default is a green raised block with glow and a visible focus outline', () => {
    render(<Button>Start</Button>)
    const b = screen.getByRole('button', { name: 'Start' })
    expect(b).toHaveAttribute('data-variant', 'default')
    expect(cls(b)).toEqual(expect.arrayContaining(['bg-primary', 'text-primary-foreground', 'bevel-primary', 'glow', 'focus-visible:outline-2', 'focus-visible:outline-ring']))
  })
  it('destructive is a solid red block in both modes', () => {
    render(<Button variant="destructive">Löschen</Button>)
    expect(cls(screen.getByRole('button', { name: 'Löschen' }))).toEqual(
      expect.arrayContaining(['bg-destructive-solid', 'text-destructive-solid-foreground', 'bevel-danger']),
    )
  })
  it('outline and secondary are neutral bevels that invert when pressed', () => {
    render(
      <>
        <Button variant="outline">A</Button>
        <Button variant="secondary">B</Button>
      </>,
    )
    for (const name of ['A', 'B']) expect(cls(screen.getByRole('button', { name }))).toEqual(expect.arrayContaining(['bevel', 'active:bevel-pressed']))
  })
  it('ghost and link stay flat', () => {
    render(
      <>
        <Button variant="ghost">G</Button>
        <Button variant="link">L</Button>
      </>,
    )
    for (const name of ['G', 'L']) {
      const c = cls(screen.getByRole('button', { name }))
      expect(c.some((x) => x.startsWith('bevel'))).toBe(false)
    }
  })
  it('keeps data-size and has no rounded-lg left', () => {
    render(<Button size="icon-sm" aria-label="x" />)
    const b = screen.getByRole('button', { name: 'x' })
    expect(b).toHaveAttribute('data-size', 'icon-sm')
    expect(cls(b).some((x) => /rounded-(lg|xl|md)|rounded-\[/.test(x))).toBe(false)
  })
})

describe('Badge', () => {
  it('uses the pixel font at 12 px or more with a bevel', () => {
    render(<Badge>Läuft</Badge>)
    const b = screen.getByText('Läuft')
    expect(cls(b)).toEqual(expect.arrayContaining(['font-heading', 'text-xs', 'uppercase', 'bevel-primary']))
    expect(cls(b).some((x) => /^text-\[(0\.[0-6]|[0-9]px|1[01]px)/.test(x))).toBe(false)
  })
  it('destructive badge is solid red', () => {
    render(<Badge variant="destructive">Fehler</Badge>)
    expect(cls(screen.getByText('Fehler'))).toEqual(expect.arrayContaining(['bg-destructive-solid', 'bevel-danger']))
  })
})
