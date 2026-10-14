# UI-Redesign – Welle 1 / T1: Primitives (Implementation Plan)

> **For agentic workers:** Dieser Plan wird vom **Teammate T1** eines Agent-Teams umgesetzt (CLAUDE.md „Implementation with agent teams“). Arbeite in deinem eigenen Worktree auf Branch `feat/ui-primitives`:
> `git fetch origin && git worktree add ../mc-panel-wt/feat-ui-primitives -b feat/ui-primitives origin/feat/ui-foundation && (cd ../mc-panel-wt/feat-ui-primitives/web && npm ci)`.
> Ändere **nur** die Dateien aus „File Structure“. Brauchst du etwas in `web/src/index.css`, `web/package.json` oder den Welle-0-Bausteinen, melde es dem Lead (SendMessage) und warte auf seinen Push nach `feat/ui-foundation`, dann `git rebase origin/feat/ui-foundation`. Steps use checkbox (`- [ ]`) syntax for tracking. Vor „fertig“: `make check` + `make test-vm SUITE=web` + `make test-vm SUITE=lint` + `make test-vm SUITE=shots`.

**Goal:** Alle shadcn-Basiskomponenten, Diagramme und die kleinen App-weiten Komponenten im M2/Clean-Stil umgestalten, ohne eine einzige Komponenten-API zu ändern.

**Architecture:** Nur Klassen-Strings in `web/src/components/ui/*` und den übrigen `web/src/components/*.tsx` ändern; Farben ausschließlich über die Welle-0-Tokens (`bg-primary`, `bg-destructive-solid`, `bg-surface-2`, `text-muted-foreground`, …) und Utilities (`bevel`, `bevel-primary`, `bevel-danger`, `bevel-pressed`, `bevel-inset`, `glow`, `pixel-heading`). `data-slot`, `data-variant`, `data-size`, Props und Exporte bleiben gleich, damit kein Feature-Code angepasst werden muss. Ein Vitest-Test pinnt die Stil-Verträge (Variante → Utility-Klasse), Playwright-Screenshots zeigen das Ergebnis.

**Tech Stack:** React 19, Tailwind v4, shadcn/ui (vendored, `cn` aus Paket `cn`), radix-ui, class-variance-authority, sonner, Vitest + Testing Library, Playwright.

**Spec:** `docs/superpowers/specs/2026-10-14-mc-panel-ui-redesign-design.md` (§3.3, §3.5, §6.2, §6.3); Grundlage: `docs/superpowers/plans/2026-10-14-ui-wave0-foundation.md`

## Global Constraints

- Nur Frontend, keine API-, Go- oder Mock-Änderung.
- Komponenten-APIs, Exporte, `data-slot`/`data-variant`/`data-size` unverändert; erlaubt sind nur **zusätzliche optionale** Props (hier: `className` an `ThemeToggle`).
- Keine externen Ressourcen (CSP); keine neuen npm-Pakete.
- Silkscreen (`font-heading`/`pixel-heading`) nie für Fließtext und nie unter 12 px (`text-xs` ist die Untergrenze).
- `--radius: 0.125rem`: alle `rounded-lg/xl/md/4xl` der Komponenten werden `rounded-none` oder `rounded-sm`.
- Fokus immer sichtbar: jede interaktive Komponente hat `focus-visible:outline-2 focus-visible:outline-ring` (bzw. bei Tabs/Items den Hintergrund-Fokus).
- `bevel-inset` setzt selbst `background-color`; nie mit einer `bg-*`-Klasse am selben Element kombinieren (Reihenfolge der Utilities ist nicht garantiert).
- Mac: nur `make check`; Tests nur über `make test-vm`. Keine Dateien anderer Teammates anfassen.
- i18n: keine neuen Schlüssel nötig; falls doch, nur unter `ui.*` in `web/src/i18n/{de,en}.ts`.
- Commits: Conventional Commits mit den Attribution-Zeilen der Session (`Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`, `Claude-Session: …`).

## Review Focus

- **Fokus verschwindet:** Durch das Entfernen der `ring-3`-Fokusringe darf kein Element ohne sichtbaren Fokus bleiben – `variants.test.tsx` prüft `focus-visible:outline` an Button, Input, SelectTrigger, NativeSelect, TabsTrigger.
- **Pixel-Schrift zu klein:** Badges werden Silkscreen; die alte `text-xs`-Untergrenze muss bleiben (Test prüft, dass Badge nicht `text-[0.6…]`/`text-[10px]` nutzt).
- **Switch-Zustand nicht erkennbar:** Track darf nicht `bevel-inset` + `bg-primary` kombinieren; der Test prüft, dass der Switch-Track kein `bevel-inset` trägt und `data-checked:bg-primary` hat.
- **Destruktiv im Light Mode:** `destructive` ist jetzt ein **massiver** roter Button (`bg-destructive-solid`, weißer Text) in beiden Modi – Test prüft die Klassen; Screenshot ConfirmDialog „Stoppen“ prüft die Optik.
- **Toaster mit `richColors`:** sonner setzt eigene Radien/Schatten; nach dem Umstyling müssen Erfolg/Fehler-Toasts im Screenshot weiterhin farbig unterscheidbar sein (Code-Review + `primitives-toast-*`-Screenshot).

---

## File Structure

| Datei | Änderung |
|---|---|
| `web/src/components/ui/button.tsx` | Varianten/Größen M2 |
| `web/src/components/ui/badge.tsx` | Silkscreen, Block-Abschrägung |
| `web/src/components/ui/input.tsx` | Slot (`bevel-inset`) |
| `web/src/components/ui/select.tsx` | Trigger als Slot, Content als Block |
| `web/src/components/ui/switch.tsx` | eckiger Track + Block-Thumb |
| `web/src/components/ui/field.tsx` | Legend als Pixel-Überschrift, eckige Field-Labels |
| `web/src/components/ui/card.tsx` | eckig, 1-px-Rand, Pixel-Titel |
| `web/src/components/ui/dialog.tsx` | Block-Rahmen, Pixel-Titel, Footer-Fläche |
| `web/src/components/ui/sheet.tsx` | Rand statt Schatten, Pixel-Titel |
| `web/src/components/ui/dropdown-menu.tsx` | Block-Rahmen, eckige Items |
| `web/src/components/ui/tabs.tsx` | Unterstrich-Tabs mit grünem Balken |
| `web/src/components/ui/table.tsx` | dichte Zeilen, Silkscreen-Kopf |
| `web/src/components/ui/separator.tsx`, `label.tsx` | unverändert (prüfen, nichts zu tun) |
| `web/src/components/ui/variants.test.tsx` (neu) | Stil-Verträge |
| `web/src/components/charts/line-chart.tsx` | eckige Legende, Tabelle ohne Radius, Pixel-Caption |
| `web/src/components/native-select.tsx` | Slot |
| `web/src/components/data-list.tsx` | dichtere Mobil-Karten |
| `web/src/components/app-toaster.tsx` | eckige Toasts mit Abschrägung |
| `web/src/components/not-found-page.tsx`, `route-error.tsx` | Grasblock + Pixel-Überschrift |
| `web/src/components/theme-toggle.tsx` | optionales `className` |
| `web/src/components/misc.test.tsx` (neu) | NotFound/RouteError/ThemeToggle |
| `web/e2e/shots/primitives.shots.ts` (neu) | Screenshots |

`confirm-dialog.tsx`, `code-form.tsx`, `dialog-lock.ts`, `sparkline.tsx` bleiben inhaltlich unverändert (sie erben den Stil von Button/Dialog/Input bzw. nutzen schon `stroke-primary`).

---

### Task 1: Button und Badge

**Files:**
- Modify: `web/src/components/ui/button.tsx`, `web/src/components/ui/badge.tsx`
- Create: `web/src/components/ui/variants.test.tsx`

**Interfaces:**
- Consumes (Welle 0): Utilities `bevel`, `bevel-primary`, `bevel-danger`, `bevel-pressed`, `glow`; Farben `bg-destructive-solid`, `text-destructive-solid-foreground`, `outline-ring`.
- Produces: `Button`/`buttonVariants` und `Badge`/`badgeVariants` mit unveränderten Varianten- und Größennamen. Neue Höhen: `default` 36 px (`h-9`), `sm` 32 px, `lg` 40 px, `xs` 24 px, `icon` 36 px, `icon-sm` 32 px, `icon-xs` 24 px, `icon-lg` 40 px.

- [ ] **Step 1: Failing test**

`web/src/components/ui/variants.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Badge } from './badge'
import { Button } from './button'

function cls(el: Element): string[] {
  return (el.getAttribute('class') ?? '').split(/\s+/)
}

describe('Button variants', () => {
  it('default is a green raised block with glow and a visible focus outline', () => {
    render(<Button>Start</Button>)
    const b = screen.getByRole('button', { name: 'Start' })
    expect(b).toHaveAttribute('data-variant', 'default')
    expect(cls(b)).toEqual(expect.arrayContaining(['bg-primary', 'text-primary-foreground', 'bevel-primary', 'glow', 'focus-visible:outline-2', 'focus-visible:outline-ring']))
  })
  it('destructive is a solid red block in both modes', () => {
    render(<Button variant="destructive">Löschen</Button>)
    expect(cls(screen.getByRole('button', { name: 'Löschen' }))).toEqual(
      expect.arrayContaining(['bg-destructive-solid', 'text-destructive-solid-foreground', 'bevel-danger']),
    )
  })
  it('outline and secondary are neutral bevels that invert when pressed', () => {
    render(
      <>
        <Button variant="outline">A</Button>
        <Button variant="secondary">B</Button>
      </>,
    )
    for (const name of ['A', 'B']) expect(cls(screen.getByRole('button', { name }))).toEqual(expect.arrayContaining(['bevel', 'active:bevel-pressed']))
  })
  it('ghost and link stay flat', () => {
    render(
      <>
        <Button variant="ghost">G</Button>
        <Button variant="link">L</Button>
      </>,
    )
    for (const name of ['G', 'L']) {
      const c = cls(screen.getByRole('button', { name }))
      expect(c.some((x) => x.startsWith('bevel'))).toBe(false)
    }
  })
  it('keeps data-size and has no rounded-lg left', () => {
    render(<Button size="icon-sm" aria-label="x" />)
    const b = screen.getByRole('button', { name: 'x' })
    expect(b).toHaveAttribute('data-size', 'icon-sm')
    expect(cls(b).some((x) => /rounded-(lg|xl|md)|rounded-\[/.test(x))).toBe(false)
  })
})

describe('Badge', () => {
  it('uses the pixel font at 12 px or more with a bevel', () => {
    render(<Badge>Läuft</Badge>)
    const b = screen.getByText('Läuft')
    expect(cls(b)).toEqual(expect.arrayContaining(['font-heading', 'text-xs', 'uppercase', 'bevel-primary']))
    expect(cls(b).some((x) => /^text-\[(0\.[0-6]|[0-9]px|1[01]px)/.test(x))).toBe(false)
  })
  it('destructive badge is solid red', () => {
    render(<Badge variant="destructive">Fehler</Badge>)
    expect(cls(screen.getByText('Fehler'))).toEqual(expect.arrayContaining(['bg-destructive-solid', 'bevel-danger']))
  })
})
```

- [ ] **Step 2: Test laufen lassen (soll fehlschlagen)**

Run: `make test-vm SUITE=web`
Expected: FAIL in `variants.test.tsx` (z. B. `bevel-primary` fehlt).

- [ ] **Step 3: `button.tsx` – `buttonVariants` ersetzen** (Funktion `Button` unverändert)

```tsx
const buttonVariants = cva(
  "group/button inline-flex shrink-0 items-center justify-center rounded-none border border-transparent bg-clip-padding text-sm font-semibold whitespace-nowrap transition-[color,background-color,box-shadow,filter] outline-none select-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50 aria-invalid:outline-2 aria-invalid:outline-destructive [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default:
          "bg-primary text-primary-foreground bevel-primary glow hover:brightness-110 active:not-aria-[haspopup]:translate-y-px",
        outline:
          "bg-card text-foreground bevel hover:bg-accent hover:text-accent-foreground active:bevel-pressed aria-expanded:bevel-pressed",
        secondary:
          "bg-secondary text-secondary-foreground bevel hover:brightness-110 active:bevel-pressed aria-expanded:bevel-pressed",
        ghost:
          "hover:bg-accent hover:text-accent-foreground aria-expanded:bg-accent aria-expanded:text-accent-foreground",
        destructive:
          "bg-destructive-solid text-destructive-solid-foreground bevel-danger hover:brightness-110 focus-visible:outline-destructive active:not-aria-[haspopup]:translate-y-px",
        link: "text-primary underline-offset-4 hover:underline",
      },
      size: {
        default:
          "h-9 gap-1.5 px-3 has-data-[icon=inline-end]:pr-2.5 has-data-[icon=inline-start]:pl-2.5",
        xs: "h-6 gap-1 px-2 text-xs has-data-[icon=inline-end]:pr-1.5 has-data-[icon=inline-start]:pl-1.5 [&_svg:not([class*='size-'])]:size-3",
        sm: "h-8 gap-1 px-2.5 text-[0.8rem] has-data-[icon=inline-end]:pr-2 has-data-[icon=inline-start]:pl-2 [&_svg:not([class*='size-'])]:size-3.5",
        lg: "h-10 gap-2 px-4 has-data-[icon=inline-end]:pr-3 has-data-[icon=inline-start]:pl-3",
        icon: "size-9",
        "icon-xs": "size-6 [&_svg:not([class*='size-'])]:size-3",
        "icon-sm": "size-8",
        "icon-lg": "size-10",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  }
)
```

- [ ] **Step 4: `badge.tsx` – `badgeVariants` ersetzen** (Funktion `Badge` unverändert)

```tsx
const badgeVariants = cva(
  "group/badge inline-flex h-6 w-fit shrink-0 items-center justify-center gap-1 overflow-hidden rounded-none border border-transparent px-2 font-heading text-xs leading-none uppercase whitespace-nowrap transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring has-data-[icon=inline-end]:pr-1.5 has-data-[icon=inline-start]:pl-1.5 aria-invalid:outline-2 aria-invalid:outline-destructive [&>svg]:pointer-events-none [&>svg]:size-3!",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground bevel-primary [a]:hover:brightness-110",
        secondary: "bg-secondary text-secondary-foreground bevel [a]:hover:brightness-110",
        destructive: "bg-destructive-solid text-destructive-solid-foreground bevel-danger [a]:hover:brightness-110",
        outline: "border-border text-foreground [a]:hover:bg-accent",
        ghost: "hover:bg-accent hover:text-accent-foreground",
        link: "text-primary underline-offset-4 hover:underline",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)
```

- [ ] **Step 5: Tests grün**

Run: `make check && make test-vm SUITE=web`
Expected: PASS. Bestehende Tests, die Badge-Texte mit `getByText('Läuft')` suchen, bleiben grün (`uppercase` ist nur CSS).

- [ ] **Step 6: Commit**

```bash
git add web/src/components/ui/button.tsx web/src/components/ui/badge.tsx web/src/components/ui/variants.test.tsx
git commit -m "feat(web): restyle button and badge as M2 blocks"
```

---

### Task 2: Eingabefelder – Input, Select, Switch, Field, NativeSelect

**Files:**
- Modify: `web/src/components/ui/input.tsx`, `ui/select.tsx`, `ui/switch.tsx`, `ui/field.tsx`, `web/src/components/native-select.tsx`
- Test: `web/src/components/ui/variants.test.tsx` (erweitern)

**Interfaces:**
- Consumes: `bevel-inset`, `bevel`, `bg-slot`, `bg-primary`, `outline-ring`.
- Produces: unveränderte Exporte `Input`, `Select*`, `Switch`, `Field*`, `NativeSelect`. Höhe von Input/SelectTrigger(default)/NativeSelect einheitlich `h-9` (= Button default).

- [ ] **Step 1: Failing tests anhängen** (in `variants.test.tsx`, Imports ergänzen)

```tsx
import { NativeSelect } from '../native-select'
import { Input } from './input'
import { Select, SelectTrigger, SelectValue } from './select'
import { Switch } from './switch'

describe('form controls', () => {
  it('input and native select are sunken slots with a focus outline', () => {
    render(
      <>
        <Input aria-label="Name" />
        <NativeSelect aria-label="Typ">
          <option>a</option>
        </NativeSelect>
      </>,
    )
    for (const el of [screen.getByRole('textbox', { name: 'Name' }), screen.getByRole('combobox', { name: 'Typ' })]) {
      expect(cls(el)).toEqual(expect.arrayContaining(['bevel-inset', 'h-9', 'focus-visible:outline-2', 'focus-visible:outline-ring']))
      expect(cls(el).some((x) => /^(dark:)?bg-/.test(x))).toBe(false)
    }
  })
  it('select trigger is a slot', () => {
    render(
      <Select>
        <SelectTrigger aria-label="Version">
          <SelectValue placeholder="–" />
        </SelectTrigger>
      </Select>,
    )
    const trigger = screen.getByRole('combobox', { name: 'Version' })
    expect(trigger).toHaveAttribute('data-slot', 'select-trigger')
    expect(cls(trigger)).toEqual(expect.arrayContaining(['bevel-inset', 'focus-visible:outline-ring']))
  })
  it('switch track shows the checked state in primary and never combines bevel-inset with bg', () => {
    render(<Switch aria-label="An" defaultChecked />)
    const sw = screen.getByRole('switch', { name: 'An' })
    expect(cls(sw)).toEqual(expect.arrayContaining(['data-checked:bg-primary', 'data-unchecked:bg-slot']))
    expect(cls(sw)).not.toContain('bevel-inset')
  })
})
```

- [ ] **Step 2: Fehlschlag prüfen**

Run: `make test-vm SUITE=web` – Expected: FAIL (`bevel-inset` fehlt).

- [ ] **Step 3: `input.tsx`** – den `className`-String ersetzen:

```tsx
"h-9 w-full min-w-0 rounded-none border-0 bevel-inset px-3 py-1 text-base text-foreground transition-colors outline-none file:inline-flex file:h-6 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:outline-2 aria-invalid:outline-destructive md:text-sm",
```

- [ ] **Step 4: `select.tsx`**

`SelectTrigger` className:

```tsx
"flex w-fit items-center justify-between gap-1.5 rounded-none border-0 bevel-inset py-2 pr-2 pl-3 text-sm whitespace-nowrap text-foreground transition-colors outline-none select-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:outline-2 aria-invalid:outline-destructive data-placeholder:text-muted-foreground data-[size=default]:h-9 data-[size=sm]:h-8 *:data-[slot=select-value]:line-clamp-1 *:data-[slot=select-value]:flex *:data-[slot=select-value]:items-center *:data-[slot=select-value]:gap-1.5 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
```

`SelectContent`: im ersten `cn(...)`-String `rounded-lg bg-popover text-popover-foreground shadow-md ring-1 ring-foreground/10` ersetzen durch `rounded-none bg-popover text-popover-foreground bevel` (Rest des Strings gleich).

`SelectItem`: im String `rounded-md` → `rounded-none`.

- [ ] **Step 5: `switch.tsx`**

Root-className:

```tsx
"peer group/switch relative inline-flex shrink-0 items-center rounded-none border border-input p-px transition-colors outline-none group-has-[:focus-visible]/field-label:outline-0 after:absolute after:-inset-x-3 after:-inset-y-2 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring aria-invalid:outline-2 aria-invalid:outline-destructive data-[size=default]:h-5 data-[size=default]:w-9 data-[size=sm]:h-4 data-[size=sm]:w-7 data-checked:bg-primary data-unchecked:bg-slot shadow-[inset_2px_2px_0_var(--slot-hi)] data-disabled:cursor-not-allowed data-disabled:opacity-50",
```

Thumb-className:

```tsx
"pointer-events-none block rounded-none bg-card bevel transition-transform group-data-[size=default]/switch:size-4 group-data-[size=sm]/switch:size-3 group-data-[size=default]/switch:data-checked:translate-x-[calc(100%-2px)] group-data-[size=sm]/switch:data-checked:translate-x-[calc(100%-2px)] group-data-[size=default]/switch:data-unchecked:translate-x-0 group-data-[size=sm]/switch:data-unchecked:translate-x-0"
```

- [ ] **Step 6: `field.tsx`**

- `FieldLegend`: `"mb-1.5 font-medium data-[variant=label]:text-sm data-[variant=legend]:text-base"` → `"mb-1.5 data-[variant=label]:text-sm data-[variant=label]:font-medium data-[variant=legend]:pixel-heading data-[variant=legend]:text-sm data-[variant=legend]:uppercase"`.
- `FieldGroup`: `gap-5` → `gap-3` (Spacing-Skala §3.4: Formularfelder 12 px), `*:data-[slot=field-group]:gap-4` → `*:data-[slot=field-group]:gap-3`.
- `FieldLabel`: im ersten String `has-[>[data-slot=field]]:rounded-lg` → `has-[>[data-slot=field]]:rounded-none`, `has-[>[data-slot=field]]:has-[:focus-visible]:ring-3 has-[>[data-slot=field]]:has-[:focus-visible]:ring-ring/50` → `has-[>[data-slot=field]]:has-[:focus-visible]:outline-2 has-[>[data-slot=field]]:has-[:focus-visible]:outline-ring`.

- [ ] **Step 7: `native-select.tsx`** className:

```tsx
'bevel-inset text-foreground h-9 rounded-none border-0 px-3 py-1 text-sm outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-50',
```

- [ ] **Step 8: Tests grün + Commit**

Run: `make check && make test-vm SUITE=web` – Expected: PASS.

```bash
git add web/src/components/ui/{input,select,switch,field,variants.test}.tsx web/src/components/native-select.tsx
git commit -m "feat(web): restyle form controls as inventory slots"
```

---

### Task 3: Flächen – Card, Dialog, Sheet, Dropdown, Tabs, Table, DataList

**Files:**
- Modify: `web/src/components/ui/{card,dialog,sheet,dropdown-menu,tabs,table}.tsx`, `web/src/components/data-list.tsx`
- Test: `web/src/components/ui/variants.test.tsx` (erweitern), `web/src/components/ui/close-labels.test.tsx` (muss unverändert grün bleiben)

**Interfaces:**
- Consumes: `bevel`, `pixel-heading`, `bg-surface-2`, `bg-primary`.
- Produces: unveränderte Exporte. `TabsList` sieht in **beiden** Varianten (`default`, `line`) als Unterstrich-Leiste aus; aktive Tabs haben einen 3-px-Balken `bg-primary`.

- [ ] **Step 1: Failing tests anhängen**

```tsx
import { Dialog, DialogContent, DialogDescription, DialogTitle } from './dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './table'
import { Tabs, TabsList, TabsTrigger } from './tabs'

describe('surfaces', () => {
  it('dialog title is a pixel heading inside a bevelled block', () => {
    render(
      <Dialog open>
        <DialogContent>
          <DialogTitle>Server stoppen?</DialogTitle>
          <DialogDescription>d</DialogDescription>
        </DialogContent>
      </Dialog>,
    )
    const dialog = screen.getByRole('dialog', { name: 'Server stoppen?' })
    expect(cls(dialog)).toContain('bevel')
    expect(cls(screen.getByText('Server stoppen?'))).toContain('pixel-heading')
  })
  it('active tab gets the green underline and keyboard focus stays visible', () => {
    render(
      <Tabs defaultValue="a">
        <TabsList>
          <TabsTrigger value="a">Jails</TabsTrigger>
          <TabsTrigger value="b">Login-Versuche</TabsTrigger>
        </TabsList>
      </Tabs>,
    )
    const active = screen.getByRole('tab', { name: 'Jails' })
    expect(active).toHaveAttribute('data-state', 'active')
    expect(cls(active)).toEqual(expect.arrayContaining(['after:bg-primary', 'data-active:after:opacity-100', 'focus-visible:outline-ring']))
  })
  it('table head uses the pixel label font and rows are dense', () => {
    render(
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Benutzer</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow>
            <TableCell>alice</TableCell>
          </TableRow>
        </TableBody>
      </Table>,
    )
    expect(cls(screen.getByRole('columnheader', { name: 'Benutzer' }))).toEqual(expect.arrayContaining(['font-heading', 'text-xs', 'uppercase']))
    expect(cls(screen.getByRole('cell', { name: 'alice' }))).toContain('py-1.5')
  })
})
```

- [ ] **Step 2: Fehlschlag prüfen** – `make test-vm SUITE=web`, Expected: FAIL.

- [ ] **Step 3: `card.tsx`**

- `Card`: `"group/card flex flex-col gap-(--card-spacing) overflow-hidden rounded-none border bg-card py-(--card-spacing) text-sm text-card-foreground [--card-spacing:--spacing(4)] has-data-[slot=card-footer]:pb-0 has-[>img:first-child]:pt-0 data-[size=sm]:[--card-spacing:--spacing(3)] data-[size=sm]:has-data-[slot=card-footer]:pb-0"` (die `ring-1 ring-foreground/10` und alle `rounded-*xl`-Bildregeln entfallen).
- `CardHeader`: `rounded-t-xl` entfernen.
- `CardTitle`: `"pixel-heading text-sm leading-snug uppercase group-data-[size=sm]/card:text-xs"`.
- `CardFooter`: `"flex items-center border-t bg-surface-2 p-(--card-spacing)"`.

- [ ] **Step 4: `dialog.tsx`**

- `DialogOverlay`: `bg-black/10` → `bg-black/50`.
- `DialogContent` className: `"fixed top-1/2 left-1/2 z-50 grid w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 gap-4 rounded-none bg-popover p-5 text-sm text-popover-foreground bevel duration-100 outline-none sm:max-w-sm data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95"`; Close-Button `className="absolute top-3 right-3"`.
- `DialogFooter`: `"-mx-5 -mb-5 flex flex-col-reverse gap-2 border-t bg-surface-2 p-4 sm:flex-row sm:justify-end"`.
- `DialogTitle`: `"pixel-heading pr-8 text-base leading-tight"`.

- [ ] **Step 5: `sheet.tsx`**

- `SheetOverlay`: `bg-black/10` → `bg-black/50`.
- `SheetContent`: im String `bg-popover bg-clip-padding text-sm text-popover-foreground shadow-lg` → `bg-popover bg-clip-padding text-sm text-popover-foreground shadow-[0_0_0_1px_var(--bevel-edge)]` (Rest gleich).
- `SheetTitle`: `"pixel-heading text-sm text-foreground uppercase"`.

- [ ] **Step 6: `dropdown-menu.tsx`**

- `DropdownMenuContent` und `DropdownMenuSubContent`: jeweils `rounded-lg` → `rounded-none` und `shadow-md ring-1 ring-foreground/10` bzw. `shadow-lg ring-1 ring-foreground/10` → `bevel`.
- In allen Item-Strings (`DropdownMenuItem`, `CheckboxItem`, `RadioItem`, `SubTrigger`) `rounded-md` → `rounded-none`. Vorher `grep -n "rounded" web/src/components/ui/dropdown-menu.tsx` und jede Fundstelle anpassen; danach darf `grep -c "rounded-\(md\|lg\)"` 0 ergeben.

- [ ] **Step 7: `tabs.tsx`**

`tabsListVariants`:

```tsx
const tabsListVariants = cva(
  "group/tabs-list inline-flex w-fit items-center justify-start gap-1 rounded-none border-b-2 border-border p-0 text-muted-foreground group-data-horizontal/tabs:h-9 group-data-vertical/tabs:h-fit group-data-vertical/tabs:flex-col group-data-vertical/tabs:border-r-2 group-data-vertical/tabs:border-b-0",
  {
    variants: {
      variant: {
        default: "bg-transparent",
        line: "bg-transparent",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)
```

`TabsTrigger` className (ersetzt alle vier Strings):

```tsx
"relative inline-flex h-full flex-none items-center justify-center gap-1.5 rounded-none border-0 px-3 text-sm font-medium whitespace-nowrap text-muted-foreground transition-colors outline-none group-data-vertical/tabs:w-full group-data-vertical/tabs:justify-start hover:text-foreground focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50 has-data-[icon=inline-end]:pr-2 has-data-[icon=inline-start]:pl-2 data-active:text-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
"after:absolute after:bg-primary after:opacity-0 after:transition-opacity group-data-horizontal/tabs:after:inset-x-0 group-data-horizontal/tabs:after:-bottom-0.5 group-data-horizontal/tabs:after:h-[3px] group-data-vertical/tabs:after:inset-y-0 group-data-vertical/tabs:after:-right-0.5 group-data-vertical/tabs:after:w-[3px] data-active:after:opacity-100",
```

Hinweis: `group-data-horizontal/tabs:after:-bottom-0.5` legt den Balken genau auf den 2-px-Rand der Liste.

- [ ] **Step 8: `table.tsx`**

- `TableHeader`: `"[&_tr]:border-b-2"`.
- `TableRow`: `"border-b transition-colors hover:bg-accent/60 has-aria-expanded:bg-accent/60 data-[state=selected]:bg-accent"`.
- `TableHead`: `"h-8 px-2 text-left align-middle font-heading text-xs font-normal tracking-wide whitespace-nowrap text-muted-foreground uppercase [&:has([role=checkbox])]:pr-0"`.
- `TableCell`: `"px-2 py-1.5 align-middle whitespace-nowrap [&:has([role=checkbox])]:pr-0"`.
- `TableFooter`: `bg-muted/50` → `bg-surface-2`.

- [ ] **Step 9: `data-list.tsx`** – Mobil-Liste dichter: `className="space-y-3"` → `"space-y-2"`, `<Card className="py-3">` → `<Card size="sm">`, `<CardContent className="px-4">` → `<CardContent>`.

- [ ] **Step 10: Tests grün + Commit**

Run: `make check && make test-vm SUITE=web`
Expected: PASS, inkl. `close-labels.test.tsx` und `data-list.test.tsx`.

```bash
git add web/src/components/ui/{card,dialog,sheet,dropdown-menu,tabs,table,variants.test}.tsx web/src/components/data-list.tsx
git commit -m "feat(web): restyle cards, dialogs, menus, tabs and tables"
```

---

### Task 4: Diagramme, Toaster, Fehlerseiten, ThemeToggle

**Files:**
- Modify: `web/src/components/charts/line-chart.tsx`, `web/src/components/app-toaster.tsx`, `web/src/components/not-found-page.tsx`, `web/src/components/route-error.tsx`, `web/src/components/theme-toggle.tsx`
- Create: `web/src/components/misc.test.tsx`

**Interfaces:**
- Consumes: `PixelIcon` aus `@/components/pixel-icon` (Welle 0, `kind`, `size`), `pixel-heading`, `bevel`.
- Produces: `ThemeToggle({ className?: string })` – zusätzliches optionales Prop, wird an den `Button` durchgereicht (T2 nutzt es in der Icon-Leiste).

- [ ] **Step 1: Failing test** – `web/src/components/misc.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderWithProviders } from '@/test/render'
import { NotFoundPage } from './not-found-page'
import { ThemeToggle } from './theme-toggle'

describe('ThemeToggle', () => {
  it('passes className to the button', () => {
    render(<ThemeToggle className="size-10" />)
    expect(screen.getByRole('button')).toHaveClass('size-10')
  })
})

describe('NotFoundPage', () => {
  it('shows a pixel heading and the grass block', async () => {
    const { container } = await renderWithProviders(<NotFoundPage />)
    const h1 = await screen.findByRole('heading', { level: 1 })
    expect(h1).toHaveClass('pixel-heading')
    expect(container.querySelector('svg[data-kind="grass"]')).not.toBeNull()
  })
})
```

(`renderWithProviders` gibt das Testing-Library-Ergebnis inkl. `container` plus `router`/`queryClient` zurück.)

- [ ] **Step 2: Fehlschlag prüfen** – `make test-vm SUITE=web`, Expected: FAIL.

- [ ] **Step 3: `theme-toggle.tsx`**

```tsx
import { Monitor, Moon, Sun } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { nextTheme, setTheme, useThemePreference } from '@/lib/theme'

export function ThemeToggle({ className }: { className?: string }) {
  const { t } = useTranslation()
  const pref = useThemePreference()
  const Icon = pref === 'dark' ? Moon : pref === 'light' ? Sun : Monitor
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      className={className}
      aria-label={t('theme.toggle', { current: t(`theme.${pref}`) })}
      title={t('theme.toggle', { current: t(`theme.${pref}`) })}
      onClick={() => setTheme(nextTheme(pref))}
    >
      <Icon className="size-4" aria-hidden="true" />
    </Button>
  )
}
```

- [ ] **Step 4: `not-found-page.tsx`**

```tsx
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { PixelIcon } from '@/components/pixel-icon'

export function NotFoundPage() {
  const { t } = useTranslation()
  return (
    <main className="flex min-h-[50svh] flex-col items-center justify-center gap-4 p-6 text-center">
      <PixelIcon kind="grass" size={48} />
      <h1 className="pixel-heading text-2xl">{t('not_found.title')}</h1>
      <p className="text-muted-foreground">{t('not_found.description')}</p>
      <Link to="/" className="text-primary underline underline-offset-4">
        {t('not_found.home')}
      </Link>
    </main>
  )
}
```

- [ ] **Step 5: `route-error.tsx`** – vor dem `<h1>` `<PixelIcon kind="grass" size={48} />` einfügen (Import `import { PixelIcon } from '@/components/pixel-icon'`), `<h1 className="text-xl font-semibold">` → `<h1 className="pixel-heading text-xl">`, `main` von `gap-4` bleibt.

- [ ] **Step 6: `app-toaster.tsx`**

```tsx
import type { CSSProperties } from 'react'
import { Toaster } from 'sonner'
import { useThemePreference } from '@/lib/theme'

// Square toasts with the block bevel; richColors keeps success/error distinguishable.
const STYLE = { '--border-radius': 'var(--radius)' } as CSSProperties

export function AppToaster() {
  const pref = useThemePreference()
  return (
    <Toaster
      theme={pref}
      richColors
      closeButton
      position="top-right"
      style={STYLE}
      toastOptions={{ className: 'bevel! font-sans', classNames: { title: 'font-semibold' } }}
    />
  )
}
```

- [ ] **Step 7: `line-chart.tsx`** (nur Optik, Logik unverändert)

- Zeile mit `SWATCH`-Element: `'inline-block h-0.5 w-3 rounded-full'` → `'inline-block h-1 w-3'`.
- Datentabelle-Wrapper: `className="max-h-64 overflow-auto rounded-md border"` → `className="max-h-64 overflow-auto border"`; beide `<th … className="px-2 py-1 … font-medium">` → `font-heading text-xs font-normal uppercase` statt `font-medium`.
- Diagramm-SVG: `rounded-sm` aus `"focus-visible:ring-ring h-auto w-full touch-pan-y rounded-sm outline-none select-none focus-visible:ring-2"` entfernen.
- Beide `<figcaption>`-Titel: `<span className="font-medium">{title}</span>` → `<span className="pixel-heading text-xs uppercase">{title}</span>`; im leeren Fall `<figcaption className="text-sm font-medium">` → `<figcaption className="pixel-heading text-xs uppercase">`.

Farben (`stroke-primary`, `stroke-chart-2`, `stroke-muted-foreground`, `stroke-destructive`) bleiben – sie zeigen über die Welle-0-Tokens jetzt Grasgrün/Blau.

- [ ] **Step 8: Tests grün + Commit**

Run: `make check && make test-vm SUITE=web`
Expected: PASS inkl. `line-chart.test.tsx`, `sparkline.test.tsx` (prüfen nur `data-testid`/Rollen).

```bash
git add web/src/components/{theme-toggle,not-found-page,route-error,app-toaster,misc.test}.tsx web/src/components/charts/line-chart.tsx
git commit -m "feat(web): restyle charts, toasts and error pages"
```

---

### Task 5: Screenshots und Abschluss

**Files:**
- Create: `web/e2e/shots/primitives.shots.ts`

**Interfaces:**
- Consumes (Welle 0): `shoot(page, name)` und `signInMock(page, user?)` aus `./shoot`.

- [ ] **Step 1: Shots-Datei**

```ts
import { expect, test } from '@playwright/test'
import { shoot, signInMock } from './shoot'

// Navigation only by clicking: the MSW state lives in the page (no page.goto after sign-in).
test('primitives: buttons, badges, dialogs, tables, tabs, toasts', async ({ page }) => {
  await signInMock(page)

  // Server list: buttons, badges; stop confirmation = destructive button in a dialog.
  await shoot(page, 'primitives-dashboard')
  const survival = page.getByRole('article', { name: 'survival' })
  await survival.getByRole('button', { name: 'Stoppen' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await shoot(page, 'primitives-confirm-dialog')
  await page.getByRole('dialog').getByRole('button', { name: 'Stoppen' }).click()
  await expect(page.getByText('Stopp für „survival“ angefordert.')).toBeVisible()
  await shoot(page, 'primitives-toast')

  // Users: dense table and a form dialog with inputs.
  await page.getByRole('link', { name: 'Benutzer', exact: true }).click()
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
  await shoot(page, 'primitives-table')
  await page.getByRole('button', { name: 'Benutzer anlegen' }).click()
  await expect(page.getByRole('dialog', { name: 'Benutzer anlegen' })).toBeVisible()
  await shoot(page, 'primitives-form-dialog')
  await page.keyboard.press('Escape')

  // Security center: underline tabs, switches, selects.
  await page.getByRole('link', { name: 'Security Center' }).click()
  await shoot(page, 'primitives-tabs')
  await page.getByRole('tab', { name: 'Einstellungen' }).click()
  await shoot(page, 'primitives-switches')
})
```

Hinweis: Wenn T2 die Navigation bereits umgebaut hat, bleiben die Link-Namen gleich (Spec §4.1: `aria-label` der Icons = bisherige Labels). Schlägt ein Selektor wegen fehlender Mock-Daten fehl (z. B. kein Tab „Einstellungen“), den Schritt mit dem tatsächlich vorhandenen Tab-Namen ersetzen (`grep -n "tabs\." web/src/features/security/*.tsx`).

- [ ] **Step 2: Screenshots erzeugen und ansehen**

```bash
make test-vm SUITE=shots && ls web/screenshots | grep primitives
```

Expected: je Name 4 PNGs. Mit dem Read-Tool mindestens ansehen: `primitives-confirm-dialog-{dark,light}-desktop.png`, `primitives-table-light-desktop.png`, `primitives-tabs-dark-mobile.png`, `primitives-toast-light-desktop.png`. Prüfen: eckige Formen, grüne Abschrägung am Primärbutton, roter massiver Stopp-Button, Silkscreen-Tabellenkopf, grüner Tab-Balken, lesbarer Text in beiden Modi, Toast farbig. Auffälligkeiten in der passenden Datei korrigieren, Task-Test erneut laufen lassen.

- [ ] **Step 3: Komplette Prüfung**

```bash
make check && make test-vm SUITE=web && make test-vm SUITE=lint
```

Expected: alles grün.

- [ ] **Step 4: Commit + Push + Meldung an den Lead**

```bash
git add web/e2e/shots/primitives.shots.ts
git commit -m "chore(web): add primitives screenshots"
git push -u origin feat/ui-primitives
```

Dem Lead melden: Branch `feat/ui-primitives` fertig (Commit-SHA), Liste der Screenshot-Dateien, etwaige Abweichungen vom Plan. **Kein PR öffnen** – der Lead führt T1 in `feat/ui-foundation` zusammen und öffnet PR 1.
