import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { LanguageCard } from './language-card'

describe('LanguageCard', () => {
  it('stores the language and switches the UI immediately', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<LanguageCard />, { me: loginAs('admin') })
    await user.selectOptions(screen.getByLabelText('Sprache der Oberfläche'), 'en')
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Language' })).toBeInTheDocument())
    expect(document.documentElement.lang).toBe('en')
    expect(db.users.find((u) => u.username === 'admin')?.locale).toBe('en')
  })
})
