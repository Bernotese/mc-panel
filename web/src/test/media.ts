// web/src/test/media.ts
type Viewport = 'desktop' | 'mobile'

const WIDTH: Record<Viewport, number> = { desktop: 1280, mobile: 360 }
let viewport: Viewport = 'desktop'
let prefersDark = false

interface Listener {
  query: string
  callback: (event: MediaQueryListEvent) => void
}
const listeners = new Set<Listener>()

function evaluate(query: string): boolean {
  if (query.includes('prefers-color-scheme: dark')) return prefersDark
  const min = /min-width:\s*(\d+)px/.exec(query)
  if (min) return WIDTH[viewport] >= Number(min[1])
  const max = /max-width:\s*(\d+)px/.exec(query)
  if (max) return WIDTH[viewport] <= Number(max[1])
  return false
}

function notify(): void {
  for (const l of [...listeners]) l.callback({ matches: evaluate(l.query), media: l.query } as MediaQueryListEvent)
}

/** jsdom has no matchMedia; this one is controllable per test (default: 1280px desktop, light). */
export function installMatchMedia(): void {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: (query: string): MediaQueryList => {
      const add = (_type: string, callback: (event: MediaQueryListEvent) => void) => {
        listeners.add({ query, callback })
      }
      const remove = (_type: string, callback: (event: MediaQueryListEvent) => void) => {
        for (const l of listeners) if (l.callback === callback && l.query === query) listeners.delete(l)
      }
      return {
        get matches() {
          return evaluate(query)
        },
        media: query,
        onchange: null,
        addEventListener: add,
        removeEventListener: remove,
        addListener: (callback: (event: MediaQueryListEvent) => void) => add('change', callback),
        removeListener: (callback: (event: MediaQueryListEvent) => void) => remove('change', callback),
        dispatchEvent: () => false,
      } as unknown as MediaQueryList
    },
  })
}

export function setViewport(next: Viewport): void {
  viewport = next
  notify()
}

export function setPrefersDark(next: boolean): void {
  prefersDark = next
  notify()
}
