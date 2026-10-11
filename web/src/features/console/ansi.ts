/** Colours of ANSI SGR 30–37/90–97; Minecraft § colours are mapped onto the same 16 names. */
export type LogColor =
  | 'black'
  | 'red'
  | 'green'
  | 'yellow'
  | 'blue'
  | 'magenta'
  | 'cyan'
  | 'white'
  | 'bright-black'
  | 'bright-red'
  | 'bright-green'
  | 'bright-yellow'
  | 'bright-blue'
  | 'bright-magenta'
  | 'bright-cyan'
  | 'bright-white'

export interface Segment {
  text: string
  color?: LogColor
  bold?: true
  italic?: true
  underline?: true
  strike?: true
  obfuscated?: true
}

type Style = Omit<Segment, 'text'>

const BASE: readonly LogColor[] = ['black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white']
const BRIGHT: readonly LogColor[] = [
  'bright-black',
  'bright-red',
  'bright-green',
  'bright-yellow',
  'bright-blue',
  'bright-magenta',
  'bright-cyan',
  'bright-white',
]

const MC_COLORS: Readonly<Record<string, LogColor>> = {
  '0': 'black',
  '1': 'blue',
  '2': 'green',
  '3': 'cyan',
  '4': 'red',
  '5': 'magenta',
  '6': 'yellow',
  '7': 'white',
  '8': 'bright-black',
  '9': 'bright-blue',
  a: 'bright-green',
  b: 'bright-cyan',
  c: 'bright-red',
  d: 'bright-magenta',
  e: 'bright-yellow',
  f: 'bright-white',
}

function sameStyle(a: Style, b: Style): boolean {
  return (
    a.color === b.color &&
    a.bold === b.bold &&
    a.italic === b.italic &&
    a.underline === b.underline &&
    a.strike === b.strike &&
    a.obfuscated === b.obfuscated
  )
}

function withMinecraftFormat(style: Style, code: string): Style | null {
  switch (code) {
    case 'k':
      return { ...style, obfuscated: true }
    case 'l':
      return { ...style, bold: true }
    case 'm':
      return { ...style, strike: true }
    case 'n':
      return { ...style, underline: true }
    case 'o':
      return { ...style, italic: true }
    default:
      return null
  }
}

/** Applies one SGR parameter list ("1;31"); unsupported parameters (backgrounds, 256/true colour) are skipped. */
function applySgr(style: Style, params: string): Style {
  const codes = params === '' ? [0] : params.split(';').map((p) => (p === '' ? 0 : Number(p)))
  let next: Style = { ...style }
  for (let k = 0; k < codes.length; k++) {
    const c = codes[k]
    if (c === 0) next = {}
    else if (c === 1) next.bold = true
    else if (c === 3) next.italic = true
    else if (c === 4) next.underline = true
    else if (c === 9) next.strike = true
    else if (c === 22) delete next.bold
    else if (c === 23) delete next.italic
    else if (c === 24) delete next.underline
    else if (c === 29) delete next.strike
    else if (c >= 30 && c <= 37) next.color = BASE[c - 30]
    else if (c === 39) delete next.color
    else if (c >= 90 && c <= 97) next.color = BRIGHT[c - 90]
    else if (c === 38 || c === 48) k += codes[k + 1] === 5 ? 2 : codes[k + 1] === 2 ? 4 : 0
  }
  return next
}

/**
 * Splits a log line into styled segments: ANSI SGR sequences and Minecraft § codes set the style, every other
 * escape sequence and control character (except tab) is dropped. The result is rendered as text only.
 */
export function parseFormatted(input: string): Segment[] {
  const out: Segment[] = []
  let style: Style = {}
  let text = ''

  const flush = () => {
    if (!text) return
    const last = out.at(-1)
    if (last && sameStyle(last, style)) last.text += text
    else out.push({ ...style, text })
    text = ''
  }
  const setStyle = (next: Style) => {
    flush()
    style = next
  }

  for (let i = 0; i < input.length; i++) {
    const ch = input[i]
    if (ch === '\u001b') {
      if (input[i + 1] !== '[') continue // lone ESC: drop it
      let j = i + 2
      while (j < input.length) {
        const code = input.charCodeAt(j)
        if (code >= 0x40 && code <= 0x7e) break // final byte of the CSI sequence
        j++
      }
      if (j >= input.length) break // unterminated sequence: drop the rest
      if (input[j] === 'm') setStyle(applySgr(style, input.slice(i + 2, j)))
      i = j
      continue
    }
    if (ch === '§') {
      const code = input[i + 1]?.toLowerCase()
      if (code === undefined) continue
      i += 1
      if (Object.hasOwn(MC_COLORS, code)) setStyle({ color: MC_COLORS[code] })
      else if (code === 'r') setStyle({})
      else {
        const formatted = withMinecraftFormat(style, code)
        if (formatted) setStyle(formatted)
      }
      continue
    }
    const c = input.charCodeAt(i)
    if ((c < 0x20 && ch !== '\t') || c === 0x7f) continue
    text += ch
  }
  flush()
  return out
}

export function plainText(input: string): string {
  return parseFormatted(input)
    .map((s) => s.text)
    .join('')
}
