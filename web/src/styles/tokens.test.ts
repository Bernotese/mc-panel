import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { contrastRatio } from '@/lib/contrast'

const css = fs.readFileSync(path.resolve(import.meta.dirname, '../index.css'), 'utf8')

function block(selector: string): Record<string, string> {
  const start = css.indexOf(`${selector} {`)
  if (start < 0) throw new Error(`block ${selector} missing`)
  const body = css.slice(start, css.indexOf('}', start))
  const vars: Record<string, string> = {}
  for (const m of body.matchAll(/--([\w-]+):\s*(#[0-9a-fA-F]{3,8});/g)) vars[m[1]] = m[2].slice(0, 7)
  return vars
}

const light = block(':root')
const dark = { ...light, ...block('.dark') }
const consoleScope = block('@utility console-scope')

// [foreground, background, minimum ratio]
const TEXT_PAIRS: [string, string, number][] = [
  ['foreground', 'background', 4.5],
  ['foreground', 'card', 4.5],
  ['foreground', 'surface-2', 4.5],
  ['muted-foreground', 'background', 4.5],
  ['muted-foreground', 'card', 4.5],
  ['muted-foreground', 'surface-2', 4.5],
  ['primary-foreground', 'primary', 4.5],
  ['primary', 'background', 4.5],
  ['secondary-foreground', 'secondary', 4.5],
  ['accent-foreground', 'accent', 4.5],
  ['destructive', 'background', 4.5],
  ['destructive-solid-foreground', 'destructive-solid', 4.5],
  ['status-online', 'background', 4.5],
  ['status-offline', 'background', 4.5],
  ['status-starting', 'background', 4.5],
  ['status-error', 'background', 4.5],
  ['console-fg', 'console-bg', 4.5],
  ['ring', 'background', 3],
]

describe.each([
  ['light', light],
  ['dark', dark],
])('%s tokens', (_name, vars) => {
  it.each(TEXT_PAIRS)('%s on %s ≥ %d', (fg, bg, min) => {
    expect(vars[fg], fg).toBeDefined()
    expect(vars[bg], bg).toBeDefined()
    expect(contrastRatio(vars[fg], vars[bg])).toBeGreaterThanOrEqual(min)
  })
  it('defines every --mc-* colour', () => {
    for (const c of '0123456789abcdef') expect(vars[`mc-${c}`], `mc-${c}`).toBeDefined()
  })
  it('keeps § colours 1–f readable on the page background', () => {
    for (const c of '123456789abcdef') expect(contrastRatio(vars[`mc-${c}`], vars.background), `mc-${c}`).toBeGreaterThanOrEqual(3)
  })
})

describe('console scope', () => {
  it('keeps § colours readable on the always-dark console in both modes', () => {
    for (const c of '0123456789abcdef') expect(contrastRatio(consoleScope[`mc-${c}`], light['console-bg']), `mc-${c}`).toBeGreaterThanOrEqual(4.5)
  })
})
