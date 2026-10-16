import { setNonce } from 'get-nonce'

/**
 * The style nonce of this page load (SEC-31): the panel puts a fresh one into index.html's
 * `<meta property="csp-nonce">` and the CSP header (style-src 'self' 'nonce-…'). Read from the
 * `nonce` property: browsers hide the attribute once the CSP applies.
 */
export function cspNonce(): string | undefined {
  const meta = document.querySelector<HTMLMetaElement>('meta[property="csp-nonce"]')
  return meta?.nonce || meta?.getAttribute('nonce') || undefined
}

/** Hands the nonce to the libraries that insert <style> elements at runtime (Radix via react-style-singleton). */
export function applyCspNonce(): void {
  const nonce = cspNonce()
  if (nonce) setNonce(nonce)
}
