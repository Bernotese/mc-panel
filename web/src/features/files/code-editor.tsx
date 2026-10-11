import { json } from '@codemirror/lang-json'
import { yaml } from '@codemirror/lang-yaml'
import { StreamLanguage } from '@codemirror/language'
import { properties } from '@codemirror/legacy-modes/mode/properties'
import { toml } from '@codemirror/legacy-modes/mode/toml'
import { EditorState, Prec, type Extension } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import { basicSetup } from 'codemirror'
import { useEffect, useRef } from 'react'
import { isFileDrag, lineSeparator, type EditorLanguage } from './format'

export interface CodeEditorProps {
  value: string
  language: EditorLanguage
  label: string
  onChange(value: string): void
  onSave(): void
}

function languageExtension(language: EditorLanguage): Extension {
  switch (language) {
    case 'yaml':
      return yaml()
    case 'json':
      return json()
    case 'toml':
      return StreamLanguage.define(toml)
    case 'properties':
      return StreamLanguage.define(properties)
    case 'text':
      return []
  }
}

/** Colours from the app's CSS variables, so light and dark mode follow the theme toggle. */
const theme = EditorView.theme({
  '&': { height: '60vh', backgroundColor: 'var(--background)', color: 'var(--foreground)' },
  '&.cm-focused': { outline: '2px solid var(--ring)' },
  '.cm-scroller': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace' },
  '.cm-gutters': { backgroundColor: 'var(--muted)', color: 'var(--muted-foreground)', border: 'none' },
  '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: 'color-mix(in oklch, var(--muted), transparent 40%)' },
})

/**
 * Uncontrolled: the document is set once per mount. FileEditor remounts it (key) after "reload".
 * Default export for React.lazy – CodeMirror ends up in its own chunk.
 */
export default function CodeEditor({ value, language, label, onChange, onSave }: CodeEditorProps) {
  const host = useRef<HTMLDivElement>(null)
  const callbacks = useRef({ onChange, onSave })
  useEffect(() => {
    callbacks.current = { onChange, onSave }
  })

  useEffect(() => {
    if (!host.current) return
    const separator = lineSeparator(value)
    const view = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: value,
        extensions: [
          basicSetup,
          languageExtension(language),
          separator ? EditorState.lineSeparator.of(separator) : [],
          // A file dropped onto the editor would be inserted as text; uploads go through the file list.
          EditorView.domEventHandlers({
            drop: (event) => {
              if (!isFileDrag(event)) return false
              event.preventDefault()
              return true
            },
          }),
          theme,
          EditorView.contentAttributes.of({ 'aria-label': label }),
          Prec.highest(
            keymap.of([
              {
                key: 'Mod-s',
                preventDefault: true,
                run: () => {
                  callbacks.current.onSave()
                  return true
                },
              },
            ]),
          ),
          EditorView.updateListener.of((update) => {
            // sliceDoc joins lines with the document's own separator (CRLF stays CRLF).
            if (update.docChanged) callbacks.current.onChange(update.state.sliceDoc())
          }),
        ],
      }),
    })
    return () => view.destroy()
    // value/language/label belong to the mount; changing them means remounting (key in FileEditor).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return <div ref={host} className="overflow-hidden rounded-md border text-sm" />
}
