import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderApp } from '@/features/servers/test-utils'
import { loginAs } from '@/mocks/db'
import { SOURCE_REPO, sourceUrl } from '@/lib/source'
import { SourceLink } from './source-link'

describe('sourceUrl', () => {
  it('links a release to its tag and anything else to the repository', () => {
    expect(sourceUrl('0.5.0')).toBe(`${SOURCE_REPO}/tree/v0.5.0`)
    expect(sourceUrl('dev')).toBe(SOURCE_REPO)
    expect(sourceUrl('0.5.1-snapshot+abc1234')).toBe(SOURCE_REPO)
    expect(sourceUrl(undefined)).toBe(SOURCE_REPO)
  })
})

describe('SourceLink', () => {
  it('opens the source in a new tab without an opener', () => {
    render(<SourceLink version="0.5.0" />)
    const link = screen.getByRole('link', { name: 'Quellcode von mc-panel 0.5.0 (AGPL-3.0)' })
    expect(link).toHaveAttribute('href', 'https://github.com/Bernotese/mc-panel/tree/v0.5.0')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('names only the license without a version', () => {
    render(<SourceLink />)
    expect(screen.getByRole('link', { name: 'Quellcode (AGPL-3.0)' })).toHaveAttribute('href', SOURCE_REPO)
  })

  it('shows the running version in the rail', async () => {
    await renderApp('/', loginAs('alice'))
    const link = await screen.findByRole('link', { name: 'Quellcode von mc-panel dev (AGPL-3.0)' })
    expect(link).toHaveAttribute('href', SOURCE_REPO)
  })
})
