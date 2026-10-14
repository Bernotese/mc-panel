import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { McText } from './mc-text'
import { PageHeader } from './page-header'
import { PingBars } from './ping-bars'
import { PixelIcon } from './pixel-icon'
import { Section } from './section'
import { StatSlot } from './stat-slot'

describe('PageHeader', () => {
  it('renders an h1 with breadcrumb and actions', () => {
    render(<PageHeader title="Server" description="Alle Server" breadcrumb={<span>Panel</span>} actions={<button type="button">Neu</button>} />)
    expect(screen.getByRole('heading', { level: 1, name: 'Server' })).toBeInTheDocument()
    expect(screen.getByText('Alle Server')).toBeInTheDocument()
    expect(screen.getByText('Panel')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Neu' })).toBeInTheDocument()
  })
})

describe('Section', () => {
  it('labels the region with its title', () => {
    render(<Section title="Zeitplan">x</Section>)
    expect(screen.getByRole('region', { name: 'Zeitplan' })).toHaveTextContent('x')
    expect(screen.getByRole('heading', { level: 2, name: 'Zeitplan' })).toBeInTheDocument()
  })
  it('supports h3 and danger tone', () => {
    render(<Section title="Gefahrenzone" tone="danger" headingLevel={3}>y</Section>)
    expect(screen.getByRole('heading', { level: 3, name: 'Gefahrenzone' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Gefahrenzone' })).toHaveAttribute('data-tone', 'danger')
  })
  it('renders without title', () => {
    const { container } = render(<Section>z</Section>)
    expect(container.querySelector('section')).toHaveTextContent('z')
  })
})

describe('StatSlot', () => {
  it('shows a dash for missing values but keeps 0', () => {
    const { rerender } = render(<StatSlot label="TPS" value={null} />)
    expect(screen.getByText('–')).toBeInTheDocument()
    rerender(<StatSlot label="TPS" value={undefined} />)
    expect(screen.getByText('–')).toBeInTheDocument()
    rerender(<StatSlot label="Spieler" value={0} />)
    expect(screen.getByText('0')).toBeInTheDocument()
    expect(screen.queryByText('–')).not.toBeInTheDocument()
  })
  it('clamps the bar and hides it for NaN/null', () => {
    const { rerender } = render(<StatSlot label="RAM" value="5 GiB" bar={1.7} />)
    expect(screen.getByRole('meter', { name: 'RAM' })).toHaveAttribute('aria-valuenow', '100')
    rerender(<StatSlot label="RAM" value="5 GiB" bar={-1} />)
    expect(screen.getByRole('meter', { name: 'RAM' })).toHaveAttribute('aria-valuenow', '0')
    rerender(<StatSlot label="RAM" value="5 GiB" bar={Number.NaN} />)
    expect(screen.queryByRole('meter')).not.toBeInTheDocument()
    rerender(<StatSlot label="RAM" value="5 GiB" bar={null} />)
    expect(screen.queryByRole('meter')).not.toBeInTheDocument()
  })
  it('marks the tone', () => {
    const { container } = render(<StatSlot label="Disk" value="5 GiB" tone="danger" />)
    expect(container.firstElementChild).toHaveAttribute('data-tone', 'danger')
  })
})

describe('PingBars', () => {
  it('is an image with a label and lights `level` bars', () => {
    const { container, rerender } = render(<PingBars level={3} label="Erreichbar" />)
    expect(screen.getByRole('img', { name: 'Erreichbar' })).toBeInTheDocument()
    expect(container.querySelectorAll('[data-on="true"]')).toHaveLength(3)
    rerender(<PingBars level={null} label="Unbekannt" />)
    expect(container.querySelectorAll('[data-on="true"]')).toHaveLength(0)
  })
})

describe('PixelIcon', () => {
  it('is hidden from assistive tech', () => {
    const { container } = render(<PixelIcon kind="server" state="offline" size={32} />)
    const el = container.firstElementChild
    expect(el).toHaveAttribute('aria-hidden', 'true')
    expect(el).toHaveAttribute('data-state', 'offline')
    expect(el).toHaveStyle({ width: '32px', height: '32px' })
  })
})

describe('McText', () => {
  it('colours § segments without producing HTML', () => {
    const { container } = render(<McText text={'§aGrün §c<b>rot</b>'} />)
    expect(container).toHaveTextContent('Grün <b>rot</b>')
    expect(container.querySelector('b')).toBeNull()
    expect(container.querySelector('.text-mc-a')).toHaveTextContent('Grün')
    expect(container.querySelector('.text-mc-c')).toHaveTextContent('<b>rot</b>')
  })
  it('survives unknown codes, a trailing § and empty text', () => {
    expect(() => render(<McText text={'§zA§'} />)).not.toThrow()
    expect(() => render(<McText text="" />)).not.toThrow()
  })
})
