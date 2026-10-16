// Unicode format characters (category Cf): bidi overrides and isolates, zero-width characters, BOM, soft hyphen.
const FORMAT_CHARS = /\p{Cf}/gu

/**
 * A file name as shown in the UI (SEC-38): format characters become visible as ⟨U+202E⟩, so a name like
 * "evil\u202Egpj.jar" cannot pose as "evilraj.jpg". The name sent to the API stays unchanged. Markup wraps it in
 * <bdi> (components/file-name.tsx); in translated text (toasts, dialog titles, labels) it is interpolated as is.
 */
export function visibleName(name: string): string {
  return name.replace(FORMAT_CHARS, (c) => `⟨U+${c.codePointAt(0)!.toString(16).toUpperCase().padStart(4, '0')}⟩`)
}

