import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { AuthLayout } from './auth-layout'

describe('AuthLayout', () => {
  it('renders brand, an h1 title and the description inside main', () => {
    render(
      <AuthLayout title="Anmelden" description="Willkommen zurück">
        <p>Formular</p>
      </AuthLayout>,
    )
    const main = screen.getByRole('main')
    expect(main).toHaveAttribute('id', 'main')
    expect(screen.getByRole('heading', { level: 1, name: 'Anmelden' })).toBeInTheDocument()
    expect(screen.getByText('Willkommen zurück')).toBeInTheDocument()
    expect(screen.getByText('mc-panel')).toBeInTheDocument()
    expect(screen.getByText('Formular')).toBeInTheDocument()
  })

  it('uses the narrow panel by default and the wide one for setup', () => {
    const { rerender } = render(<AuthLayout title="A">x</AuthLayout>)
    expect(screen.getByRole('main')).toHaveClass('max-w-md')
    rerender(
      <AuthLayout title="A" wide>
        x
      </AuthLayout>,
    )
    expect(screen.getByRole('main')).toHaveClass('max-w-lg')
  })

  it('draws the pixel pattern on the page background', () => {
    const { container } = render(<AuthLayout title="A">x</AuthLayout>)
    expect(container.firstElementChild).toHaveClass('pixel-pattern')
  })
})
