# UI-Redesign – Welle 0: Fundament (Implementation Plan)

> **For agentic workers:** Dieser Plan wird vom **Lead allein** umgesetzt (Agent-Team-Lead, siehe CLAUDE.md „Implementation with agent teams“). Steps use checkbox (`- [ ]`) syntax for tracking. Erst wenn alle Tasks erledigt und gepusht sind, starten die fünf Teammates von Welle 1 (Pläne `2026-10-14-ui-wave1-t*.md`) auf dem resultierenden Commit.

**Goal:** Design-Tokens (Dark M2 / Light Clean), Schriften, CSS-Utilities, die sechs gemeinsamen Bausteine mit festen Props und eine Screenshot-Infrastruktur bereitstellen, auf denen alle Teammates von Welle 1 parallel aufbauen.

**Architecture:** Alle Farben bleiben shadcn-Variablen in `web/src/index.css` (Hex-Werte, damit ein Vitest-Test den Kontrast rechnerisch prüfen kann) plus neue Tokens für Abschrägung, Slots, Status, Konsole und Minecraft-Farbcodes. Utilities (`bevel*`, `glow`, `pixel-heading`, `console-scope`, `pixel-pattern`) sind Tailwind-v4-`@utility`s. Die Bausteine liegen in `web/src/components/` und sind reine Präsentationskomponenten ohne Daten-Hooks.

**Tech Stack:** React 19, Tailwind v4 (`@tailwindcss/vite`), shadcn/ui (vendored), fontsource, Vitest + Testing Library, Playwright.

**Spec:** `docs/superpowers/specs/2026-10-14-mc-panel-ui-redesign-design.md` (§3, §6.1, §6.3)

## Global Constraints

- Nur `web/`, `scripts/test-vm.sh`, `scripts/vm-test.sh`, `docs/` und `.gitignore` ändern; keine API-, Go- oder OpenAPI-Änderung.
- Keine externen Ressourcen (CSP): Schriften nur über fontsource-Pakete, keine Bilder von außen, keine Mojang-Assets.
- Schriften: `@fontsource-variable/space-grotesk@^5.3.0`, `@fontsource/silkscreen@^5.3.0`, `@fontsource-variable/pixelify-sans@^5.3.0`, `@fontsource-variable/jetbrains-mono@^5.3.0`; `@fontsource-variable/geist` entfernen.
- Silkscreen nie für Fließtext und nie unter 12 px; Pixelify Sans nur für Zahlen/MOTD.
- Kontrast WCAG AA: 4,5 : 1 für Text, 3 : 1 für UI-Ränder/große Schrift.
- `--radius: 0.125rem`.
- Fehlende Werte werden als „–“ angezeigt, nie als 0.
- Mac: nur `make check`. Tests nur über `make test-vm`. Kein `npm install -g`. `npm install <paket>` im Projekt (`web/`) ist erlaubt (ändert nur `package.json`/`package-lock.json`).
- Go-Tests: nicht betroffen. TS-Tests: Vitest + Testing Library, keine neuen Testbibliotheken.
- Commits: Conventional Commits mit den Attribution-Zeilen aus der Session.

## Review Focus

- **Light Mode wird vergessen:** jede neue Farbe braucht einen Wert in `:root` *und* `.dark`; der Kontrast-Test (Task 1) prüft beide Blöcke.
- **Konsole in Light Mode:** `console-scope` muss die dunklen `--mc-*`-Werte lokal setzen, sonst sind §-Farben auf dunklem Grund im Light Mode unlesbar (Test in Task 1).
- **Fehlender Wert vs. 0:** `StatSlot` mit `value={0}` zeigt „0“, mit `null`/`undefined` „–“; `bar` außerhalb 0–1 wird geklemmt, `NaN` blendet den Balken aus (Tests in Task 2).
- **`forced-colors`:** Abschrägungen sind `box-shadow`s und verschwinden in Windows-Kontrastmodus; die Utilities setzen dort einen echten `border` (Task 1, manuell im Screenshot nicht prüfbar – Code-Review).
- **Unbekannte §-Codes / leerer Text in `McText`:** `§z`, `§` am Ende und `''` dürfen nicht crashen und kein HTML erzeugen (Tests in Task 2).

---

## File Structure

| Datei | Verantwortung |
|---|---|
| `web/src/index.css` (ändern) | Fonts, `@theme`-Mapping, Tokens `:root`/`.dark`, Utilities |
| `web/src/styles/tokens.test.ts` (neu) | Kontrast der Token-Paare (liest `index.css`) |
| `web/src/lib/contrast.ts` (+ Test) (neu) | `contrastRatio(hexA, hexB)` |
| `web/src/lib/mc-colors.ts` (neu) | `LogColor` → Tailwind-Klasse `text-mc-*` |
| `web/src/components/page-header.tsx` (ändern) | + `breadcrumb`, Pixel-Überschrift |
| `web/src/components/section.tsx` (neu) | Abschnitts-Container |
| `web/src/components/stat-slot.tsx` (neu) | Kennzahl als Inventar-Slot |
| `web/src/components/ping-bars.tsx` (neu) | Erreichbarkeits-Balken |
| `web/src/components/pixel-icon.tsx` (neu) | Grasblock / Server-Icon |
| `web/src/components/mc-text.tsx` (neu) | `§`-Farbcodes als Spans |
| `web/src/components/foundation.test.tsx` (neu) | Tests der Bausteine |
| `web/playwright.config.ts` (ändern) | Projekt `screenshots` |
| `web/e2e/shots/shoot.ts` (neu) | Helfer `shoot()` + `signInMock()` |
| `web/e2e/shots/foundation.shots.ts` (neu) | Login + Startseite in Dark/Light, Desktop/Mobil |
| `scripts/vm-test.sh`, `scripts/test-vm.sh` (ändern) | Suite `shots`, Rückkopie nach `web/screenshots/` |
| `.gitignore` (ändern) | `web/screenshots/` |
| `docs/dev/README.md` (ändern) | Suite `shots` dokumentieren |

---

### Task 1: Schriften, Tokens und Utilities

**Files:**
- Modify: `web/package.json`, `web/package-lock.json` (via npm)
- Modify: `web/src/index.css` (komplett ersetzen bis vor `@layer base`)
- Create: `web/src/lib/contrast.ts`, `web/src/lib/contrast.test.ts`, `web/src/styles/tokens.test.ts`, `web/src/lib/mc-colors.ts`

**Interfaces:**
- Produces (CSS, von allen Teammates genutzt):
  - Tailwind-Farben: alle bisherigen (`bg-background`, `text-primary`, …) plus `bg-surface-2`, `text-status-online|offline|starting|error`, `bg-status-*`, `text-mc-0` … `text-mc-f`, `bg-slot`, `bg-console`, `text-console-foreground`, `border-console-border`, `bg-destructive-solid`, `text-destructive-solid-foreground`.
  - Fonts: `font-sans` (Space Grotesk), `font-heading` (Silkscreen), `font-pixel` (Pixelify Sans), `font-mono` (JetBrains Mono).
  - Utilities: `bevel` (neutral erhaben), `bevel-primary`, `bevel-danger`, `bevel-pressed` (invertiert, für `:active`), `bevel-inset` (Slot/Input), `glow` (kombinierbar mit allen `bevel*`), `pixel-heading`, `console-scope`, `pixel-pattern`.
  - `mcColorClass(color: LogColor): string` aus `@/lib/mc-colors`.
  - `contrastRatio(a: string, b: string): number` aus `@/lib/contrast`.

- [ ] **Step 1: Pakete tauschen**

```bash
cd web && npm install @fontsource-variable/space-grotesk@^5.3.0 @fontsource/silkscreen@^5.3.0 @fontsource-variable/pixelify-sans@^5.3.0 @fontsource-variable/jetbrains-mono@^5.3.0 && npm uninstall @fontsource-variable/geist
```

Prüfen, dass Umlaute abgedeckt sind (latin-Subset reicht bis U+00FF):

```bash
grep -h "unicode-range" node_modules/@fontsource/silkscreen/index.css node_modules/@fontsource-variable/pixelify-sans/index.css | head -4
```

Expected: eine Zeile mit `U+0000-00FF` je Paket.

- [ ] **Step 2: Failing test für `contrastRatio`**

`web/src/lib/contrast.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { contrastRatio } from './contrast'

describe('contrastRatio', () => {
  it('is 21 for black on white and symmetric', () => {
    expect(contrastRatio('#000000', '#ffffff')).toBeCloseTo(21, 1)
    expect(contrastRatio('#ffffff', '#000000')).toBeCloseTo(21, 1)
  })
  it('is 1 for identical colours', () => {
    expect(contrastRatio('#5cc14a', '#5cc14a')).toBeCloseTo(1, 5)
  })
  it('accepts short hex', () => {
    expect(contrastRatio('#fff', '#000')).toBeCloseTo(21, 1)
  })
  it('rejects non-hex input', () => {
    expect(() => contrastRatio('red', '#000')).toThrow()
  })
})
```

- [ ] **Step 3: `contrast.ts` implementieren**

```ts
// WCAG 2.x relative luminance and contrast ratio for #rgb / #rrggbb colours.
function channel(v: number): number {
  const s = v / 255
  return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
}

function luminance(hex: string): number {
  const m = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(hex.trim())
  if (!m) throw new Error(`not a hex colour: ${hex}`)
  const h = m[1].length === 3 ? [...m[1]].map((c) => c + c).join('') : m[1]
  const [r, g, b] = [0, 2, 4].map((i) => channel(parseInt(h.slice(i, i + 2), 16)))
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

export function contrastRatio(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}
```

- [ ] **Step 4: `index.css` ersetzen** (alles vor `@layer base { … }`; der `@layer base`-Block am Ende bleibt, bekommt aber `font-feature-settings` nicht – unverändert lassen)

```css
@import "tailwindcss";
@import "tw-animate-css";
@import "./shadcn-tailwind.css";
@import "@fontsource-variable/space-grotesk";
@import "@fontsource/silkscreen/400.css";
@import "@fontsource-variable/pixelify-sans";
@import "@fontsource-variable/jetbrains-mono";

@custom-variant dark (&:is(.dark *));

@theme inline {
    --font-sans: 'Space Grotesk Variable', ui-sans-serif, system-ui, sans-serif;
    --font-heading: 'Silkscreen', 'Space Grotesk Variable', ui-sans-serif, sans-serif;
    --font-pixel: 'Pixelify Sans Variable', 'Space Grotesk Variable', ui-sans-serif, sans-serif;
    --font-mono: 'JetBrains Mono Variable', ui-monospace, monospace;
    --color-sidebar-ring: var(--sidebar-ring);
    --color-sidebar-border: var(--sidebar-border);
    --color-sidebar-accent-foreground: var(--sidebar-accent-foreground);
    --color-sidebar-accent: var(--sidebar-accent);
    --color-sidebar-primary-foreground: var(--sidebar-primary-foreground);
    --color-sidebar-primary: var(--sidebar-primary);
    --color-sidebar-foreground: var(--sidebar-foreground);
    --color-sidebar: var(--sidebar);
    --color-chart-5: var(--chart-5);
    --color-chart-4: var(--chart-4);
    --color-chart-3: var(--chart-3);
    --color-chart-2: var(--chart-2);
    --color-chart-1: var(--chart-1);
    --color-ring: var(--ring);
    --color-input: var(--input);
    --color-border: var(--border);
    --color-destructive: var(--destructive);
    --color-destructive-solid: var(--destructive-solid);
    --color-destructive-solid-foreground: var(--destructive-solid-foreground);
    --color-accent-foreground: var(--accent-foreground);
    --color-accent: var(--accent);
    --color-muted-foreground: var(--muted-foreground);
    --color-muted: var(--muted);
    --color-secondary-foreground: var(--secondary-foreground);
    --color-secondary: var(--secondary);
    --color-primary-foreground: var(--primary-foreground);
    --color-primary: var(--primary);
    --color-popover-foreground: var(--popover-foreground);
    --color-popover: var(--popover);
    --color-card-foreground: var(--card-foreground);
    --color-card: var(--card);
    --color-foreground: var(--foreground);
    --color-background: var(--background);
    --color-surface-2: var(--surface-2);
    --color-slot: var(--slot-bg);
    --color-console: var(--console-bg);
    --color-console-foreground: var(--console-fg);
    --color-console-border: var(--console-border);
    --color-status-online: var(--status-online);
    --color-status-offline: var(--status-offline);
    --color-status-starting: var(--status-starting);
    --color-status-error: var(--status-error);
    --color-mc-0: var(--mc-0);
    --color-mc-1: var(--mc-1);
    --color-mc-2: var(--mc-2);
    --color-mc-3: var(--mc-3);
    --color-mc-4: var(--mc-4);
    --color-mc-5: var(--mc-5);
    --color-mc-6: var(--mc-6);
    --color-mc-7: var(--mc-7);
    --color-mc-8: var(--mc-8);
    --color-mc-9: var(--mc-9);
    --color-mc-a: var(--mc-a);
    --color-mc-b: var(--mc-b);
    --color-mc-c: var(--mc-c);
    --color-mc-d: var(--mc-d);
    --color-mc-e: var(--mc-e);
    --color-mc-f: var(--mc-f);
    --radius-sm: calc(var(--radius) * 0.6);
    --radius-md: calc(var(--radius) * 0.8);
    --radius-lg: var(--radius);
    --radius-xl: calc(var(--radius) * 1.4);
    --radius-2xl: calc(var(--radius) * 1.8);
    --radius-3xl: calc(var(--radius) * 2.2);
    --radius-4xl: calc(var(--radius) * 2.6);
}

/* Light: "Clean" – warm off-white, bevels only hinted. Hex only: tokens.test.ts checks contrast. */
:root {
    --radius: 0.125rem;
    --background: #f6f6f4;
    --foreground: #1f2024;
    --card: #ffffff;
    --card-foreground: #1f2024;
    --popover: #ffffff;
    --popover-foreground: #1f2024;
    --surface-2: #efefec;
    --primary: #358027;
    --primary-foreground: #ffffff;
    --secondary: #e8e8e4;
    --secondary-foreground: #1f2024;
    --muted: #efefec;
    --muted-foreground: #5a5b61;
    --accent: #e3efdd;
    --accent-foreground: #1f2024;
    --destructive: #b3261e;
    --destructive-solid: #b3261e;
    --destructive-solid-foreground: #ffffff;
    --border: #dcdcd8;
    --input: #c9c9c4;
    --ring: #358027;
    --chart-1: #358027;
    --chart-2: #2f6fb3;
    --chart-3: #a86b00;
    --chart-4: #8a3fb0;
    --chart-5: #5a5b61;
    --sidebar: #efefec;
    --sidebar-foreground: #1f2024;
    --sidebar-primary: #358027;
    --sidebar-primary-foreground: #ffffff;
    --sidebar-accent: #ffffff;
    --sidebar-accent-foreground: #1f2024;
    --sidebar-border: #dcdcd8;
    --sidebar-ring: #358027;
    --rail: #ebebe8;

    --bevel-hi: #ffffff;
    --bevel-lo: #c4c4bf;
    --bevel-edge: #bdbdb8;
    --bevel-primary-hi: #5cb24a;
    --bevel-primary-lo: #1f5216;
    --bevel-danger-hi: #d9534a;
    --bevel-danger-lo: #7a1712;
    --slot-bg: #ffffff;
    --slot-hi: #e4e4e0;
    --slot-lo: #ffffff;
    --slot-edge: #e2e2de;
    --glow: #5cc14a33;
    --pixel-shadow: none;

    --status-online: #2f7a22;
    --status-offline: #696a70;
    --status-starting: #8a5a00;
    --status-error: #b3261e;

    --console-bg: #0b0c0e;
    --console-fg: #c4c6cc;
    --console-border: #23252b;

    --mc-0: #000000;
    --mc-1: #0000aa;
    --mc-2: #1d7a1d;
    --mc-3: #00737a;
    --mc-4: #aa0000;
    --mc-5: #8a008a;
    --mc-6: #8a5a00;
    --mc-7: #5a5b61;
    --mc-8: #45464b;
    --mc-9: #3333cc;
    --mc-a: #2f7a22;
    --mc-b: #00737a;
    --mc-c: #c42b2b;
    --mc-d: #a3289e;
    --mc-e: #7a6600;
    --mc-f: #1f2024;
}

/* Dark: "M2" – launcher base, block bevels, inventory slots. */
.dark {
    --background: #101114;
    --foreground: #d4d6dc;
    --card: #16171b;
    --card-foreground: #d4d6dc;
    --popover: #1a1c21;
    --popover-foreground: #d4d6dc;
    --surface-2: #1a1c21;
    --primary: #5cc14a;
    --primary-foreground: #0c1a08;
    --secondary: #2c2f37;
    --secondary-foreground: #e6e7ea;
    --muted: #1a1c21;
    --muted-foreground: #8a8d96;
    --accent: #22252b;
    --accent-foreground: #ffffff;
    --destructive: #ff6b6b;
    --destructive-solid: #b3261e;
    --destructive-solid-foreground: #ffffff;
    --border: #2a2d34;
    --input: #3a3d45;
    --ring: #5cc14a;
    --chart-1: #5cc14a;
    --chart-2: #6ba8ff;
    --chart-3: #f5c451;
    --chart-4: #d08cff;
    --chart-5: #8a8d96;
    --sidebar: #16171b;
    --sidebar-foreground: #d4d6dc;
    --sidebar-primary: #5cc14a;
    --sidebar-primary-foreground: #0c1a08;
    --sidebar-accent: #22252b;
    --sidebar-accent-foreground: #ffffff;
    --sidebar-border: #0b0c0e;
    --sidebar-ring: #5cc14a;
    --rail: #0d0e11;

    --bevel-hi: #575b66;
    --bevel-lo: #24262c;
    --bevel-edge: #000000;
    --bevel-primary-hi: #8ad877;
    --bevel-primary-lo: #2d6b21;
    --bevel-danger-hi: #d9534a;
    --bevel-danger-lo: #7a1712;
    --slot-bg: #1a1c21;
    --slot-hi: #0b0c0e;
    --slot-lo: #2c2f37;
    --slot-edge: transparent;
    --glow: #5cc14a40;
    --pixel-shadow: 2px 2px 0 #000000;

    --status-online: #5cc14a;
    --status-offline: #8a8d96;
    --status-starting: #f5c451;
    --status-error: #ff6b6b;

    --mc-0: #8a8d96;
    --mc-1: #7f8cff;
    --mc-2: #3fc43f;
    --mc-3: #3fc4c4;
    --mc-4: #e05050;
    --mc-5: #d070d0;
    --mc-6: #ffaa00;
    --mc-7: #c4c6cc;
    --mc-8: #8a8d96;
    --mc-9: #7f7fff;
    --mc-a: #55ff55;
    --mc-b: #55ffff;
    --mc-c: #ff5555;
    --mc-d: #ff55ff;
    --mc-e: #ffff55;
    --mc-f: #ffffff;
}

/* Raised block (buttons, badges). Compose with `glow`. */
@utility bevel {
    box-shadow: inset 2px 2px 0 var(--bevel-hi), inset -2px -2px 0 var(--bevel-lo), 0 0 0 1px var(--bevel-edge), var(--tw-glow, 0 0 #0000);
    @media (forced-colors: active) {
        box-shadow: none;
        border: 1px solid ButtonText;
    }
}
@utility bevel-primary {
    box-shadow: inset 2px 2px 0 var(--bevel-primary-hi), inset -2px -2px 0 var(--bevel-primary-lo), 0 0 0 1px var(--bevel-edge), var(--tw-glow, 0 0 #0000);
    @media (forced-colors: active) {
        box-shadow: none;
        border: 1px solid ButtonText;
    }
}
@utility bevel-danger {
    box-shadow: inset 2px 2px 0 var(--bevel-danger-hi), inset -2px -2px 0 var(--bevel-danger-lo), 0 0 0 1px var(--bevel-edge);
    @media (forced-colors: active) {
        box-shadow: none;
        border: 1px solid ButtonText;
    }
}
/* Pressed: swaps light and dark edge of the neutral bevel. */
@utility bevel-pressed {
    box-shadow: inset 2px 2px 0 var(--bevel-lo), inset -2px -2px 0 var(--bevel-hi), 0 0 0 1px var(--bevel-edge);
}
/* Sunken inventory slot (stat slots, inputs). */
@utility bevel-inset {
    background-color: var(--slot-bg);
    box-shadow: inset 2px 2px 0 var(--slot-hi), inset -2px -2px 0 var(--slot-lo), 0 0 0 1px var(--slot-edge);
    @media (forced-colors: active) {
        box-shadow: none;
        border: 1px solid CanvasText;
    }
}
@utility glow {
    --tw-glow: 0 0 16px var(--glow);
    @media (prefers-reduced-motion: reduce) {
        --tw-glow: 0 0 #0000;
    }
}
@utility pixel-heading {
    font-family: var(--font-heading);
    font-weight: 400;
    letter-spacing: 0.02em;
    text-shadow: var(--pixel-shadow);
}
/* Console surface: dark in both modes, with the dark § colour set. */
@utility console-scope {
    background-color: var(--console-bg);
    color: var(--console-fg);
    --mc-0: #8a8d96;
    --mc-1: #7f8cff;
    --mc-2: #3fc43f;
    --mc-3: #3fc4c4;
    --mc-4: #e05050;
    --mc-5: #d070d0;
    --mc-6: #ffaa00;
    --mc-7: #c4c6cc;
    --mc-8: #8a8d96;
    --mc-9: #7f7fff;
    --mc-a: #55ff55;
    --mc-b: #55ffff;
    --mc-c: #ff5555;
    --mc-d: #ff55ff;
    --mc-e: #ffff55;
    --mc-f: #ffffff;
}
/* Subtle pixel checkerboard for the auth screens (pure CSS, no image). */
@utility pixel-pattern {
    background-color: var(--background);
    background-image:
        linear-gradient(45deg, var(--surface-2) 25%, transparent 25%, transparent 75%, var(--surface-2) 75%),
        linear-gradient(45deg, var(--surface-2) 25%, transparent 25%, transparent 75%, var(--surface-2) 75%);
    background-size: 32px 32px;
    background-position: 0 0, 16px 16px;
}
```

Die bisherigen oklch-Blöcke `:root { … }` und `.dark { … }` entfallen vollständig.

- [ ] **Step 5: `mc-colors.ts`**

```ts
import type { LogColor } from '@/features/console/ansi'

/** Tailwind class per Minecraft/ANSI colour name; the values come from the --mc-* tokens (console-scope overrides them). */
const CLASS: Readonly<Record<LogColor, string>> = {
  black: 'text-mc-0',
  blue: 'text-mc-1',
  green: 'text-mc-2',
  cyan: 'text-mc-3',
  red: 'text-mc-4',
  magenta: 'text-mc-5',
  yellow: 'text-mc-6',
  white: 'text-mc-7',
  'bright-black': 'text-mc-8',
  'bright-blue': 'text-mc-9',
  'bright-green': 'text-mc-a',
  'bright-cyan': 'text-mc-b',
  'bright-red': 'text-mc-c',
  'bright-magenta': 'text-mc-d',
  'bright-yellow': 'text-mc-e',
  'bright-white': 'text-mc-f',
}

export function mcColorClass(color: LogColor): string {
  return CLASS[color]
}
```

- [ ] **Step 6: Failing test für Token-Kontraste**

`web/src/styles/tokens.test.ts`:

```ts
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
```

Hinweis: `--glow` und `--slot-edge` sind keine 7-stelligen Paare bzw. `transparent` und werden vom Regex ohnehin auf 7 Zeichen gekürzt oder ignoriert; sie sind nicht in `TEXT_PAIRS`.

- [ ] **Step 7: Lokal prüfen und auf der VM testen**

```bash
cd /Users/jannes/Workspace/mc-panel && make check
make test-vm SUITE=web
```

Expected: `make check` grün; Vitest grün. Schlägt ein Kontrast-Paar fehl, den Token-Wert im jeweiligen Block abdunkeln/aufhellen (Richtung: Light dunkler, Dark heller), **nicht** den Mindestwert senken, und erneut testen.

- [ ] **Step 8: Commit**

```bash
git add web/package.json web/package-lock.json web/src/index.css web/src/lib/contrast.ts web/src/lib/contrast.test.ts web/src/lib/mc-colors.ts web/src/styles/tokens.test.ts
git commit -m "feat(web): add M2/Clean design tokens, fonts and block utilities"
```

---

### Task 2: Gemeinsame Bausteine

**Files:**
- Modify: `web/src/components/page-header.tsx`
- Create: `web/src/components/section.tsx`, `stat-slot.tsx`, `ping-bars.tsx`, `pixel-icon.tsx`, `mc-text.tsx`, `foundation.test.tsx`

**Interfaces (fest – Welle 1 baut darauf):**

```ts
// page-header.tsx
export function PageHeader(props: { title: string; description?: string; actions?: ReactNode; breadcrumb?: ReactNode }): JSX.Element
// <h1> bleibt die Überschrift (E2E sucht heading level 1 "Server").

// section.tsx
export function Section(props: {
  title?: string
  description?: ReactNode
  actions?: ReactNode
  tone?: 'default' | 'danger'
  headingLevel?: 2 | 3            // Standard 2
  className?: string
  children: ReactNode
}): JSX.Element
// Mit title: <section aria-labelledby=…> und Überschrift; ohne title: <section> ohne Label.

// stat-slot.tsx
export function StatSlot(props: {
  label: string
  value: ReactNode | null | undefined   // null/undefined → "–"
  hint?: ReactNode
  bar?: number | null                   // 0..1, geklemmt; null/NaN → kein Balken
  tone?: 'default' | 'warn' | 'danger'
  className?: string
}): JSX.Element
// Balken: role="meter" aria-label={label} aria-valuemin=0 aria-valuemax=100 aria-valuenow={round(bar*100)}

// ping-bars.tsx
export function PingBars(props: { level: 0 | 1 | 2 | 3 | 4 | 5 | null; label: string; className?: string }): JSX.Element
// role="img" aria-label={label}; null = grau (unbekannt)

// pixel-icon.tsx
export type PixelIconKind = 'grass' | 'server'
export function PixelIcon(props: { kind: PixelIconKind; state?: 'online' | 'offline' | 'unknown'; size?: number; className?: string }): JSX.Element
// immer aria-hidden; size in px (Standard 20)

// mc-text.tsx
export function McText(props: { text: string; className?: string }): JSX.Element
// <span> mit Kind-<span>s je Segment aus parseFormatted(); Farbe über mcColorClass(); nie dangerouslySetInnerHTML
```

- [ ] **Step 1: Failing tests**

`web/src/components/foundation.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { McText } from './mc-text'
import { PageHeader } from './page-header'
import { PingBars } from './ping-bars'
import { PixelIcon } from './pixel-icon'
import { Section } from './section'
import { StatSlot } from './stat-slot'

describe('PageHeader', () => {
  it('renders an h1 with breadcrumb and actions', () => {
    render(<PageHeader title="Server" description="Alle Server" breadcrumb={<span>Panel</span>} actions={<button type="button">Neu</button>} />)
    expect(screen.getByRole('heading', { level: 1, name: 'Server' })).toBeInTheDocument()
    expect(screen.getByText('Alle Server')).toBeInTheDocument()
    expect(screen.getByText('Panel')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Neu' })).toBeInTheDocument()
  })
})

describe('Section', () => {
  it('labels the region with its title', () => {
    render(<Section title="Zeitplan">x</Section>)
    expect(screen.getByRole('region', { name: 'Zeitplan' })).toHaveTextContent('x')
    expect(screen.getByRole('heading', { level: 2, name: 'Zeitplan' })).toBeInTheDocument()
  })
  it('supports h3 and danger tone', () => {
    render(<Section title="Gefahrenzone" tone="danger" headingLevel={3}>y</Section>)
    expect(screen.getByRole('heading', { level: 3, name: 'Gefahrenzone' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Gefahrenzone' })).toHaveAttribute('data-tone', 'danger')
  })
  it('renders without title', () => {
    const { container } = render(<Section>z</Section>)
    expect(container.querySelector('section')).toHaveTextContent('z')
  })
})

describe('StatSlot', () => {
  it('shows a dash for missing values but keeps 0', () => {
    const { rerender } = render(<StatSlot label="TPS" value={null} />)
    expect(screen.getByText('–')).toBeInTheDocument()
    rerender(<StatSlot label="TPS" value={undefined} />)
    expect(screen.getByText('–')).toBeInTheDocument()
    rerender(<StatSlot label="Spieler" value={0} />)
    expect(screen.getByText('0')).toBeInTheDocument()
    expect(screen.queryByText('–')).not.toBeInTheDocument()
  })
  it('clamps the bar and hides it for NaN/null', () => {
    const { rerender } = render(<StatSlot label="RAM" value="5 GiB" bar={1.7} />)
    expect(screen.getByRole('meter', { name: 'RAM' })).toHaveAttribute('aria-valuenow', '100')
    rerender(<StatSlot label="RAM" value="5 GiB" bar={-1} />)
    expect(screen.getByRole('meter', { name: 'RAM' })).toHaveAttribute('aria-valuenow', '0')
    rerender(<StatSlot label="RAM" value="5 GiB" bar={Number.NaN} />)
    expect(screen.queryByRole('meter')).not.toBeInTheDocument()
    rerender(<StatSlot label="RAM" value="5 GiB" bar={null} />)
    expect(screen.queryByRole('meter')).not.toBeInTheDocument()
  })
  it('marks the tone', () => {
    const { container } = render(<StatSlot label="Disk" value="5 GiB" tone="danger" />)
    expect(container.firstElementChild).toHaveAttribute('data-tone', 'danger')
  })
})

describe('PingBars', () => {
  it('is an image with a label and lights `level` bars', () => {
    const { container, rerender } = render(<PingBars level={3} label="Erreichbar" />)
    expect(screen.getByRole('img', { name: 'Erreichbar' })).toBeInTheDocument()
    expect(container.querySelectorAll('[data-on="true"]')).toHaveLength(3)
    rerender(<PingBars level={null} label="Unbekannt" />)
    expect(container.querySelectorAll('[data-on="true"]')).toHaveLength(0)
  })
})

describe('PixelIcon', () => {
  it('is hidden from assistive tech', () => {
    const { container } = render(<PixelIcon kind="server" state="offline" size={32} />)
    const el = container.firstElementChild
    expect(el).toHaveAttribute('aria-hidden', 'true')
    expect(el).toHaveAttribute('data-state', 'offline')
    expect(el).toHaveStyle({ width: '32px', height: '32px' })
  })
})

describe('McText', () => {
  it('colours § segments without producing HTML', () => {
    const { container } = render(<McText text={'§aGrün §c<b>rot</b>'} />)
    expect(container).toHaveTextContent('Grün <b>rot</b>')
    expect(container.querySelector('b')).toBeNull()
    expect(container.querySelector('.text-mc-a')).toHaveTextContent('Grün')
    expect(container.querySelector('.text-mc-c')).toHaveTextContent('<b>rot</b>')
  })
  it('survives unknown codes, a trailing § and empty text', () => {
    expect(() => render(<McText text={'§zA§'} />)).not.toThrow()
    expect(() => render(<McText text="" />)).not.toThrow()
  })
})
```

- [ ] **Step 2: Test laufen lassen (soll fehlschlagen)**

Run: `make test-vm SUITE=web`
Expected: FAIL – Module `./section`, `./stat-slot`, … nicht gefunden.

- [ ] **Step 3: Implementieren**

`page-header.tsx`:

```tsx
import type { ReactNode } from 'react'

export function PageHeader({
  title,
  description,
  actions,
  breadcrumb,
}: {
  title: string
  description?: string
  actions?: ReactNode
  breadcrumb?: ReactNode
}) {
  return (
    <div className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0 space-y-1">
        {breadcrumb ? <div className="text-muted-foreground text-xs">{breadcrumb}</div> : null}
        <h1 className="pixel-heading text-foreground text-xl break-words md:text-2xl">{title}</h1>
        {description ? <p className="text-muted-foreground text-sm">{description}</p> : null}
      </div>
      {actions ? <div className="flex flex-wrap gap-2">{actions}</div> : null}
    </div>
  )
}
```

`section.tsx`:

```tsx
import { useId, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

export function Section({
  title,
  description,
  actions,
  tone = 'default',
  headingLevel = 2,
  className,
  children,
}: {
  title?: string
  description?: ReactNode
  actions?: ReactNode
  tone?: 'default' | 'danger'
  headingLevel?: 2 | 3
  className?: string
  children: ReactNode
}) {
  const id = useId()
  const Heading = headingLevel === 3 ? 'h3' : 'h2'
  return (
    <section
      aria-labelledby={title ? id : undefined}
      data-tone={tone}
      className={cn(
        'bg-card text-card-foreground min-w-0 border p-4 md:p-5',
        tone === 'danger' && 'border-destructive/60',
        className,
      )}
    >
      {title || actions ? (
        <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 space-y-1">
            {title ? (
              <Heading id={id} className={cn('pixel-heading text-sm uppercase', tone === 'danger' && 'text-destructive')}>
                {title}
              </Heading>
            ) : null}
            {description ? <div className="text-muted-foreground text-sm">{description}</div> : null}
          </div>
          {actions ? <div className="flex flex-wrap gap-2">{actions}</div> : null}
        </div>
      ) : null}
      {children}
    </section>
  )
}
```

`stat-slot.tsx`:

```tsx
import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

function clamp01(v: number): number {
  return Math.min(1, Math.max(0, v))
}

export function StatSlot({
  label,
  value,
  hint,
  bar,
  tone = 'default',
  className,
}: {
  label: string
  value: ReactNode | null | undefined
  hint?: ReactNode
  bar?: number | null
  tone?: 'default' | 'warn' | 'danger'
  className?: string
}) {
  const showBar = typeof bar === 'number' && !Number.isNaN(bar)
  const pct = showBar ? Math.round(clamp01(bar) * 100) : 0
  return (
    <div data-tone={tone} className={cn('bevel-inset min-w-0 px-3 py-2', className)}>
      <div className="text-muted-foreground truncate text-xs">{label}</div>
      <div
        className={cn(
          'font-pixel text-foreground text-lg leading-tight font-semibold tabular-nums',
          tone === 'warn' && 'text-status-starting',
          tone === 'danger' && 'text-status-error',
        )}
      >
        {value === null || value === undefined ? '–' : value}
      </div>
      {hint ? <div className="text-muted-foreground truncate text-xs">{hint}</div> : null}
      {showBar ? (
        <div role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} className="bg-background mt-1.5 h-1.5">
          <div
            className={cn('h-full', tone === 'danger' ? 'bg-status-error' : tone === 'warn' ? 'bg-status-starting' : 'bg-primary')}
            style={{ width: `${pct}%` }}
          />
        </div>
      ) : null}
    </div>
  )
}
```

`ping-bars.tsx`:

```tsx
import { cn } from '@/lib/utils'

const HEIGHTS = [4, 6, 8, 10, 12]

export function PingBars({ level, label, className }: { level: 0 | 1 | 2 | 3 | 4 | 5 | null; label: string; className?: string }) {
  const lit = level ?? 0
  return (
    <span role="img" aria-label={label} className={cn('inline-flex h-3 items-end gap-px', className)}>
      {HEIGHTS.map((h, i) => {
        const on = i < lit
        return (
          <span
            key={h}
            data-on={on}
            className={cn('block w-[3px]', on ? (lit <= 1 ? 'bg-status-error' : lit <= 2 ? 'bg-status-starting' : 'bg-status-online') : 'bg-status-offline/40')}
            style={{ height: `${h}px` }}
          />
        )
      })}
    </span>
  )
}
```

`pixel-icon.tsx`:

```tsx
import { cn } from '@/lib/utils'

export type PixelIconKind = 'grass' | 'server'

/** Own pixel art (no Mojang assets): a grass block and a "server" block with a status lamp. */
export function PixelIcon({
  kind,
  state,
  size = 20,
  className,
}: {
  kind: PixelIconKind
  state?: 'online' | 'offline' | 'unknown'
  size?: number
  className?: string
}) {
  return (
    <svg
      aria-hidden="true"
      data-kind={kind}
      data-state={state}
      viewBox="0 0 16 16"
      width={size}
      height={size}
      shapeRendering="crispEdges"
      style={{ width: `${size}px`, height: `${size}px` }}
      className={cn('shrink-0', state === 'offline' && 'opacity-50 grayscale', className)}
    >
      {kind === 'grass' ? (
        <>
          <rect width="16" height="16" fill="#866043" />
          <rect width="16" height="6" fill="#5cc14a" />
          <rect y="6" width="16" height="2" fill="#3f8f2f" />
          <rect x="2" y="6" width="2" height="3" fill="#5cc14a" />
          <rect x="9" y="6" width="2" height="4" fill="#5cc14a" />
          <rect x="5" y="11" width="2" height="2" fill="#6b4c33" />
          <rect x="12" y="12" width="2" height="2" fill="#6b4c33" />
          <rect width="16" height="1" fill="#ffffff" opacity="0.2" />
          <rect y="15" width="16" height="1" fill="#000000" opacity="0.25" />
        </>
      ) : (
        <>
          <rect width="16" height="16" fill="#3a3d45" />
          <rect x="1" y="1" width="14" height="6" fill="#575b66" />
          <rect x="1" y="9" width="14" height="6" fill="#575b66" />
          <rect x="3" y="3" width="6" height="2" fill="#24262c" />
          <rect x="3" y="11" width="6" height="2" fill="#24262c" />
          <rect x="11" y="3" width="2" height="2" fill={state === 'online' ? '#5cc14a' : state === 'offline' ? '#8a8d96' : '#f5c451'} />
          <rect x="11" y="11" width="2" height="2" fill={state === 'online' ? '#5cc14a' : state === 'offline' ? '#8a8d96' : '#f5c451'} />
        </>
      )}
    </svg>
  )
}
```

`mc-text.tsx`:

```tsx
import { parseFormatted } from '@/features/console/ansi'
import { mcColorClass } from '@/lib/mc-colors'
import { cn } from '@/lib/utils'

export function McText({ text, className }: { text: string; className?: string }) {
  const segments = parseFormatted(text)
  return (
    <span className={cn('font-pixel', className)}>
      {segments.map((s, i) => (
        // Segments are positional and never reordered, so the index is a stable key.
        <span
          key={i}
          className={cn(s.color && mcColorClass(s.color), s.bold && 'font-bold', s.italic && 'italic', s.underline && 'underline', s.strike && 'line-through')}
        >
          {s.text}
        </span>
      ))}
    </span>
  )
}
```

- [ ] **Step 4: Tests grün**

Run: `make check && make test-vm SUITE=web`
Expected: PASS (inkl. bestehender Tests: `PageHeader` hat dieselbe API plus `breadcrumb`).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/{page-header,section,stat-slot,ping-bars,pixel-icon,mc-text}.tsx web/src/components/foundation.test.tsx
git commit -m "feat(web): add shared redesign building blocks (Section, StatSlot, PingBars, PixelIcon, McText)"
```

---

### Task 3: Screenshot-Infrastruktur

**Files:**
- Modify: `web/playwright.config.ts`, `scripts/vm-test.sh`, `scripts/test-vm.sh`, `.gitignore`, `docs/dev/README.md`
- Create: `web/e2e/shots/shoot.ts`, `web/e2e/shots/foundation.shots.ts`

**Interfaces:**
- Produces:
  - `shoot(page: Page, name: string): Promise<void>` – schreibt `web/screenshots/<name>-<theme>-<viewport>.png` für `theme ∈ {dark, light}` und `viewport ∈ {desktop (1440×900), mobile (390×844)}`; setzt das Theme über `localStorage['mcpanel.theme']` + Reload-freies `document.documentElement.classList.toggle('dark')`.
  - `signInMock(page: Page, user?: 'admin' | 'alice'): Promise<void>` – Login gegen die MSW-Mocks (Admin mit TOTP `123456`, alice ohne).
  - Playwright-Projekt `screenshots`, `testMatch: /\.shots\.ts$/`; Teammates legen je eine Datei `web/e2e/shots/<bereich>.shots.ts` an (T1 `primitives`, T2 `shell`, T3 `live`, T4 `manage`, T5 `admin`).
  - `make test-vm SUITE=shots` – führt nur dieses Projekt aus und kopiert `web/screenshots/` zurück in den lokalen Checkout (gitignored).

- [ ] **Step 1: `shoot.ts`**

```ts
import { expect, type Page } from '@playwright/test'

const VIEWPORTS = { desktop: { width: 1440, height: 900 }, mobile: { width: 390, height: 844 } } as const

async function setTheme(page: Page, theme: 'dark' | 'light') {
  await page.evaluate((t) => {
    localStorage.setItem('mcpanel.theme', t)
    document.documentElement.classList.toggle('dark', t === 'dark')
    document.documentElement.style.colorScheme = t
  }, theme)
}

/** Screenshots of the current page in both themes and both viewports (web/screenshots/, not committed). */
export async function shoot(page: Page, name: string) {
  for (const [vp, size] of Object.entries(VIEWPORTS)) {
    await page.setViewportSize(size)
    for (const theme of ['dark', 'light'] as const) {
      await setTheme(page, theme)
      await page.waitForTimeout(150)
      await page.screenshot({ path: `screenshots/${name}-${theme}-${vp}.png`, fullPage: true })
    }
  }
  await page.setViewportSize(VIEWPORTS.desktop)
}

/** Signs in against the MSW mocks; the mock state lives in the page, so callers navigate by clicking, not page.goto. */
export async function signInMock(page: Page, user: 'admin' | 'alice' = 'admin') {
  await page.goto('/')
  await page.getByLabel('Benutzername').fill(user)
  await page.getByLabel('Passwort').fill('correct-horse-battery')
  await page.getByRole('button', { name: 'Anmelden' }).click()
  if (user === 'admin') {
    await page.getByLabel('Code aus der Authenticator-App').fill('123456')
    await page.getByRole('button', { name: 'Bestätigen' }).click()
  }
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
}
```

Vor dem Schreiben prüfen, ob `alice` in `web/src/mocks/db.ts` ohne TOTP angelegt ist (`grep -n "alice" web/src/mocks/db.ts`); falls alice TOTP hat, den `if` auf `true` für beide setzen.

- [ ] **Step 2: `foundation.shots.ts`**

```ts
import { test } from '@playwright/test'
import { shoot, signInMock } from './shoot'

test('foundation: login and dashboard', async ({ page }) => {
  await page.goto('/')
  await shoot(page, 'foundation-login')
  await signInMock(page)
  await shoot(page, 'foundation-dashboard')
})
```

- [ ] **Step 3: Playwright-Projekt**

In `web/playwright.config.ts` im `projects`-Array nach `mock` einfügen:

```ts
    {
      // Visual review only (make test-vm SUITE=shots); never part of CI or `npm run e2e`.
      name: 'screenshots',
      testMatch: /\.shots\.ts$/,
      use: { ...devices['Desktop Chrome'], baseURL: `http://localhost:${MOCK_PORT}`, locale: 'de-DE' },
    },
```

`npm run e2e` nutzt `--project=mock`, CI damit unberührt. In `package.json` ein Script ergänzen: `"shots": "playwright test --project=screenshots"`.

- [ ] **Step 4: VM-Suite `shots`**

`scripts/vm-test.sh`: `suite_shots` neben `suite_e2e` (Muster übernehmen):

```bash
# Screenshots of the UI against the mock backend for visual review (web/screenshots/, copied back by test-vm.sh).
suite_shots() {
  need_node_modules
  need_playwright
  rm -rf web/screenshots
  log "Playwright screenshots (mock backend)"
  (cd web && npm run shots)
}
```

Im `case` am Ende `shots) suite_shots ;;` ergänzen, die Usage-Zeilen um `shots` erweitern; `all` ruft `shots` **nicht** auf.

`scripts/test-vm.sh`: `shots` in die erlaubten Suites aufnehmen und nach dem `ssh … vm-test.sh` ergänzen:

```bash
if [ "${suite}" = shots ]; then
  rm -rf "${root}/web/screenshots"
  rsync -az "${vm}:${dest}/web/screenshots/" "${root}/web/screenshots/"
  echo "==> screenshots in ${root}/web/screenshots/"
fi
```

und `--exclude=/web/screenshots` zur rsync-Excludeliste hinzufügen. `.gitignore`: `web/screenshots/`.

- [ ] **Step 5: Docs**

`docs/dev/README.md`, Abschnitt „Tests auf der Test-VM“: eine Zeile zu `make test-vm SUITE=shots` (Screenshots aller `*.shots.ts` gegen den Mock in Dark/Light, Desktop/Mobil, landen lokal in `web/screenshots/`; nicht Teil von `all` und CI).

- [ ] **Step 6: Ausführen**

```bash
make check && make test-vm SUITE=shots && ls web/screenshots
```

Expected: 8 PNGs `foundation-{login,dashboard}-{dark,light}-{desktop,mobile}.png`. Mit dem Read-Tool zwei davon ansehen (Dark/Light Login) und prüfen: neue Schrift, Off-White bzw. #101114 als Hintergrund.

- [ ] **Step 7: Commit**

```bash
git add web/playwright.config.ts web/package.json web/e2e/shots scripts/vm-test.sh scripts/test-vm.sh .gitignore docs/dev/README.md
git commit -m "chore(web): add screenshot suite for visual review on the test VM"
```

---

### Task 4: Welle 0 abschließen

- [ ] **Step 1:** `make check && make test-vm SUITE=lint && make test-vm SUITE=web` – alles grün.
- [ ] **Step 2:** `git push -u origin feat/ui-foundation` (Branch enthält Spec, Pläne, Welle 0). Noch **kein** PR – PR 1 wird nach T1 geöffnet.
- [ ] **Step 3:** Commit-SHA notieren; das ist der Startpunkt für alle fünf Teammates (`git worktree add ../mc-panel-wt/<branch> -b <branch> origin/feat/ui-foundation`).
- [ ] **Step 4:** Screenshots aus Task 3 dem Nutzer zeigen (SendUserFile) – erster Eindruck von Tokens und Schrift, bevor Welle 1 startet.
