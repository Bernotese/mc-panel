import { getNonce } from 'get-nonce'
import { afterEach, describe, expect, it } from 'vitest'
import { applyCspNonce, cspNonce } from './csp-nonce'

function addMeta(nonce: string): HTMLMetaElement {
  const meta = document.createElement('meta')
  meta.setAttribute('property', 'csp-nonce')
  meta.setAttribute('nonce', nonce)
  document.head.append(meta)
  return meta
}

describe('cspNonce', () => {
  afterEach(() => document.head.querySelectorAll('meta[property="csp-nonce"]').forEach((m) => m.remove()))

  it('is undefined without the meta tag', () => {
    expect(cspNonce()).toBeUndefined()
  })

  it('reads the nonce of the page and hands it to get-nonce', () => {
    addMeta('bm9uY2Utb25l')
    expect(cspNonce()).toBe('bm9uY2Utb25l')
    applyCspNonce()
    expect(getNonce()).toBe('bm9uY2Utb25l')
  })
})
