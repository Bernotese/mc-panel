import { useLayoutEffect, useRef, useState } from 'react'

/** Height from `top` (document coordinates) to the window's bottom edge minus `bottomGap`, at least `min`. */
export function fillHeight(viewport: number, top: number, bottomGap: number, min: number): number {
  return Math.max(min, Math.floor(viewport - top - bottomGap))
}

/** Lets an element reach the bottom of the window; recomputed on resize and when the page above it changes height. */
export function useFillHeight<T extends HTMLElement>(min = 320, bottomGap = 24) {
  const ref = useRef<T>(null)
  const [height, setHeight] = useState<number | null>(null)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const update = () => setHeight(fillHeight(window.innerHeight, el.getBoundingClientRect().top + window.scrollY, bottomGap, min))
    update()
    window.addEventListener('resize', update)
    const observer = new ResizeObserver(update)
    observer.observe(document.body)
    return () => {
      window.removeEventListener('resize', update)
      observer.disconnect()
    }
  }, [min, bottomGap])
  return { ref, height }
}
