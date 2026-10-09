import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { RecoveryCodesPanel } from './recovery-codes-panel'

const codes = ['aaaaaaaaaa', 'bbbbbbbbbb']

beforeEach(() => {
  Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:codes'), revokeObjectURL: vi.fn() })
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
})
afterEach(() => vi.restoreAllMocks())

describe('RecoveryCodesPanel', () => {
  it('lists every code', () => {
    render(<RecoveryCodesPanel codes={codes} doneLabel="Weiter" onDone={() => undefined} />)
    const list = screen.getByRole('list', { name: 'Wiederherstellungscodes' })
    expect(within(list).getAllByRole('listitem').map((li) => li.textContent)).toEqual(codes)
  })

  it('only continues after the user confirmed storing the codes', async () => {
    const user = userEvent.setup()
    const onDone = vi.fn()
    render(<RecoveryCodesPanel codes={codes} doneLabel="Weiter" onDone={onDone} />)
    const done = screen.getByRole('button', { name: 'Weiter' })
    expect(done).toBeDisabled()
    await user.click(screen.getByRole('checkbox', { name: 'Ich habe die Codes sicher gespeichert.' }))
    await user.click(done)
    expect(onDone).toHaveBeenCalledTimes(1)
  })

  it('copies the codes to the clipboard', async () => {
    const user = userEvent.setup()
    render(<RecoveryCodesPanel codes={codes} doneLabel="Weiter" onDone={() => undefined} />)
    await user.click(screen.getByRole('button', { name: 'Kopieren' }))
    expect(await navigator.clipboard.readText()).toBe('aaaaaaaaaa\nbbbbbbbbbb')
  })

  it('downloads the codes as a text file', async () => {
    const user = userEvent.setup()
    render(<RecoveryCodesPanel codes={codes} doneLabel="Weiter" onDone={() => undefined} />)
    await user.click(screen.getByRole('button', { name: 'Als Textdatei herunterladen' }))
    expect(URL.createObjectURL).toHaveBeenCalledWith(expect.any(Blob))
    expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledTimes(1)
  })
})
