import { afterEach, describe, expect, it, vi } from 'vitest'
import { downloadText } from './download'

describe('downloadText', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('revokes the object URL only after the browser had time to start the download', () => {
    vi.useFakeTimers()
    const create = vi.fn(() => 'blob:codes')
    const revoke = vi.fn()
    Object.assign(URL, { createObjectURL: create, revokeObjectURL: revoke })
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)

    downloadText('codes.txt', 'abc')

    expect(create).toHaveBeenCalledTimes(1)
    expect(revoke).not.toHaveBeenCalled()
    vi.runAllTimers()
    expect(revoke).toHaveBeenCalledWith('blob:codes')
    expect(document.querySelector('a[download]')).toBeNull()
  })
})
