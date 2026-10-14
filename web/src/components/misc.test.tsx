import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderWithProviders } from '@/test/render'
import { NotFoundPage } from './not-found-page'
import { ThemeToggle } from './theme-toggle'

describe('ThemeToggle', () => {
  it('passes className to the button', () => {
    render(<ThemeToggle className="size-10" />)
    expect(screen.getByRole('button')).toHaveClass('size-10')
  })
})

describe('NotFoundPage', () => {
  it('shows a pixel heading and the grass block', async () => {
    const { container } = await renderWithProviders(<NotFoundPage />)
    const h1 = await screen.findByRole('heading', { level: 1 })
    expect(h1).toHaveClass('pixel-heading')
    expect(container.querySelector('svg[data-kind="grass"]')).not.toBeNull()
  })
})
