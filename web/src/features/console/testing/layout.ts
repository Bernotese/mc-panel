import { vi } from 'vitest'

/** jsdom has no layout: give the log viewport (data-console-log) a size so the virtualizer renders rows. */
export function stubLogViewport(): void {
  const original = HTMLElement.prototype.getBoundingClientRect
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    if (this.hasAttribute('data-console-log')) {
      return { x: 0, y: 0, top: 0, left: 0, right: 800, bottom: 600, width: 800, height: 600, toJSON: () => ({}) } as DOMRect
    }
    return original.call(this)
  })
  // @tanstack/virtual-core measures the scroll element via clientWidth/clientHeight (offsetWidth/offsetHeight up to
  // virtual-core 3.19.4); stub both so the rows render with either.
  for (const [prop, size] of [
    ['clientWidth', 800],
    ['clientHeight', 600],
    ['offsetWidth', 800],
    ['offsetHeight', 600],
  ] as const) {
    const getter = Object.getOwnPropertyDescriptor(HTMLElement.prototype, prop)?.get
    vi.spyOn(HTMLElement.prototype, prop, 'get').mockImplementation(function (this: HTMLElement) {
      return this.hasAttribute('data-console-log') ? size : (getter?.call(this) ?? 0)
    })
  }
  HTMLElement.prototype.scrollTo ??= function scrollTo() {}
}
