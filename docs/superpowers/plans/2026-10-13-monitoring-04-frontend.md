# mc-panel Teilprojekt 5: Monitoring – Phase B3 Frontend (Aufgaben 11–14)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eigene SVG-Diagramme (`LineChart`, `Sparkline`), Monitoring-Werte im Dashboard (pro Server Spieler/TPS/CPU/RAM, Sparkline, Erreichbarkeit, Alert-Badges; Host-Karte für Admins), Tab „Monitoring“ (Live-Kopf alle 5 s, Bereichswahl 1 h / 24 h / 7 d / 30 d, vier Diagramme, Down-Alert-Schalter für Admins), Einstellungs-Karte „Monitoring & Alerts“ und Playwright gegen den Mock.

**Architecture:** Generische Diagramm-Bausteine unter `web/src/components/charts/` (reine Funktionen für Skalen, Lücken und Ausdünnen, darüber zwei React-Komponenten), Feature-Code unter `web/src/features/monitoring/` (API-Queries, Modell, Komponenten), Einbindung in Dashboard, Server-Detailseite und Einstellungen. Alle Daten kommen über die in Phase A generierten Typen und den MSW-Mock (V6).

**Tech Stack:** React 19, TypeScript, TanStack Query/Router, shadcn/ui (Card, Badge, Button, Switch, Input, Label), react-i18next, MSW 2, Vitest + Testing Library, Playwright. Keine neuen Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-13-mc-panel-monitoring-design.md` (§7), Übersicht `docs/superpowers/plans/2026-10-13-monitoring-00-overview.md` (V3, V6, Global Constraints).

## Global Constraints

Alle Global Constraints der Übersicht gelten, insbesondere:

- Texte `de`/`en` (gleiche Schlüssel, `en: Messages` ist typisiert), keine Inline-Skripte, keine CDNs, kein `dangerouslySetInnerHTML`. Die CSP erlaubt `style-src 'self' 'unsafe-inline'` (`internal/httpx/middleware.go`): `style={{ width: … }}` für Balken ist zulässig, Farben kommen trotzdem nur über Tailwind-Klassen und Theme-Variablen (`--primary`, `--chart-2`, `--muted-foreground`, `--destructive`), damit hell/dunkel automatisch stimmt.
- Rechte: `metrics`, `metrics/live` mit `server.view`; `overview` filtert der Server (Host nur für Admins); Einstellungen und Down-Alert-Schalter nur Admins (`PUT /monitoring/settings` verlangt Sudo – der globale Sudo-Dialog wiederholt die Aktion).
- Takte im Frontend: Live-Kopf alle 5 s (nur solange der Tab offen ist – Radix entfernt inaktive Tabs), Dashboard-Übersicht alle 30 s, Diagramme alle 30 s (1 h) bzw. 5 min (sonst).
- Alert-Einstellungen: `disk_min_percent` 1–50, `disk_min_bytes` 1 GiB – 10 TiB (UI in GiB, höchstens zwei Nachkommastellen), `down_after_min` 1–60, `crash_restarts` 2–20, `crash_window_min` 1–120.
- Diagramme: kein Verbinden über Lücken > 2 Schritte, höchstens ein Punkt pro Pixelspalte (min/max bleiben erhalten), Tooltip per Maus und Tastatur, `role="img"` mit Titel und Zusammenfassung, Umschalter „Als Tabelle anzeigen“.
- Dateieigentum B3: `web/src/components/charts/**` (neu), `web/src/features/monitoring/**` (neu), `web/src/features/dashboard/**`, `web/src/features/servers/**` (nur Tab-Einbindung und die Tab-Tests), `web/src/features/settings/**`, Locale-Dateien (außer `errors.*`), `web/src/i18n/i18n.test.ts` (nur `DYNAMIC_KEYS`), `web/e2e/**`, `web/playwright.config.ts` (nur `testMatch`). **Nicht** ändern: `web/src/api/**`, `web/src/mocks/**` (Phase A); Bedarf → Lead.
- Commits enden mit `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` und `Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R`.

## Ausführung dieser Phase

- Worktree: `git worktree add ../mc-panel-wt/feat/monitoring-frontend -b feat/monitoring-frontend origin/main` (nach dem Merge von Phase A), dort `(cd web && npm ci)`.
- „rot“/„grün“: auf dem Mac nur `make check` (tsc); Vitest/eslint/Playwright per `make test-vm SUITE=web`, `SUITE=lint`, `SUITE=e2e` (lange Läufe auf der VM detached starten und das Log abfragen; einzelne Dateien: `ssh claude@192.168.1.117 'cd ~/mc-panel-test/monitoring-frontend/web && PATH=/usr/local/lib/nodejs/bin:$PATH npx vitest run src/components/charts'`). Neue Dateien vor `make check` mit `git add -N` vormerken.
- Vor dem Push: `make check` und `make test-vm` (alle Suites). Draft-PR, `gh pr checks <n> --watch` bis `CI OK`.

### Was Phase A liefert (benutzt, nicht geändert)

- `web/src/api/schema.d.ts` mit den sieben Operationen aus V3; `web/src/api/types.ts` exportiert Aliase für jedes benannte Schema: `MetricsRange`, `MetricPoint`, `MetricsSeries`, `TpsSource`, `LiveMetrics`, `AlertKind`, `OverviewServer`, `HostSnapshot`, `MonitoringOverview`, `FiringAlert`, `MonitoringSettings`, `UpdateMonitoringSettingsRequest`, `UpdateServerAlertsRequest`. `Server` hat `down_alerts: boolean | null`.
- MSW-Mock `web/src/mocks/handlers/monitoring.ts` (V6): Server 1 `survival` (läuft, Spieler, TPS; live immer mindestens ein Spieler mit Namen; `down_alerts = true` für Admins), 2 `creative` (im Seed gestoppt → alle Werte `null`), 3 `lobby` (ohne Instanz → alle Metrik-Schlüssel mit `[]`), 4 `modded` (im Monitoring-Mock „soll laufen, nicht erreichbar“: `up = false`, Alert `server.down` – bewusste Mock-Vereinfachung, sein Server-Status im übrigen Mock bleibt `stopped`); Host mit `disk_free` knapp über der Schwelle; `overview` filtert nach `server.view`, `host = null` für Nicht-Admins; `PUT /monitoring/settings` validiert und verlangt Sudo; `PUT /servers/{id}/alerts` nur Admin.

Diese Phase überschreibt Mock-Antworten in Tests per `server.use(...)` (wie `features/backups/test-utils.tsx`) und verlässt sich nur in Playwright auf die Seed-Daten.

## Review Focus (in dieser Phase)

1. **Lücken und Extremwerte im Diagramm:** leere Reihe, ein einzelner Punkt, alle Werte gleich (z. B. TPS 20,0), Werte mit Lücke mitten im Zeitraum, 2880 Rohpunkte → keine Linie über die Lücke, keine Division durch 0, höchstens ~580 Punkte im SVG. → Aufgabe 11 (`scale.test.ts`, `thin.test.ts`, `line-chart.test.tsx`).
2. **Fehlende Werte statt Nullen:** `null` in `OverviewServer`/`LiveMetrics` (Server startet, agent weg) → „–“, nie „0“ oder „NaN“. → Aufgaben 12, 13.
3. **Polling-Lebensdauer:** Live-Abfrage stoppt, sobald der Tab verlassen wird; Bereichswechsel lädt nur den neuen Bereich. → Aufgabe 13.
4. **Rechte:** Nicht-Admin sieht keine Host-Karte, keinen Down-Alert-Schalter, keine Einstellungs-Karte (Route ist Admin-only). → Aufgaben 12, 13.
5. **Tastatur und Screenreader:** Diagramm fokussierbar, Pfeiltasten wandern durch die Werte, Werte werden in einer Live-Region angesagt; Tabellen-Alternative. → Aufgabe 11.

## Dateistruktur

```
web/src/components/charts/scale.ts          niceTicks, timeTicks, linear
web/src/components/charts/thin.ts           ChartPoint, splitSegments, thinPoints, prepareSeries
web/src/components/charts/line-chart.tsx    LineChart (Achsen, Band, Grenzlinie, Tooltip, Tabelle)
web/src/components/charts/sparkline.tsx     Sparkline
web/src/features/monitoring/api.ts          Query-Keys, queryOptions, Mutationen, Takte
web/src/features/monitoring/model.ts        Umrechnung, Erreichbarkeit, Alert-Schlüssel, Einstellungs-Validierung
web/src/features/monitoring/meter.tsx       Meter (Balken mit role="meter")
web/src/features/monitoring/server-metrics.tsx  Werte im Dashboard-Kärtchen
web/src/features/monitoring/host-card.tsx   Host-Karte
web/src/features/monitoring/live-header.tsx Live-Kopf
web/src/features/monitoring/range-picker.tsx Bereichswahl
web/src/features/monitoring/metric-charts.tsx die vier Diagramme
web/src/features/monitoring/down-alerts-switch.tsx Schalter (Admin)
web/src/features/monitoring/monitoring-tab.tsx Tab
web/src/features/monitoring/test-utils.tsx  Builder + Mock-Overrides für Tests
web/src/features/settings/monitoring-card.tsx Einstellungs-Karte
web/src/features/dashboard/dashboard-page.tsx, server-card.tsx  Einbindung
web/src/features/servers/server-detail-page.tsx Tab „Monitoring“
web/src/features/settings/settings-page.tsx Abschnitt „Monitoring“
web/src/i18n/de.ts, en.ts, i18n.test.ts     Texte, DYNAMIC_KEYS
web/e2e/monitoring.spec.ts, web/playwright.config.ts
```

---

### Task 11: Diagramm-Bausteine (`components/charts`)

**Files:**
- Create: `web/src/components/charts/scale.ts`, `scale.test.ts`, `thin.ts`, `thin.test.ts`, `line-chart.tsx`, `line-chart.test.tsx`, `sparkline.tsx`, `sparkline.test.tsx`
- Modify: `web/src/i18n/de.ts`, `web/src/i18n/en.ts` (Block `charts`)

**Interfaces:**
- Consumes: `cn` aus `@/lib/utils`, `Button`, `useTranslation`.
- Produces (für Aufgaben 12–13):
  - `scale.ts`: `interface Ticks { min: number; max: number; step: number; values: number[] }`, `niceTicks(lo: number, hi: number, count?: number): Ticks`, `timeTicks(from: number, to: number, count?: number, offsetMs?: number): number[]`
  - `thin.ts`: `interface ChartPoint { t: number; v: number; min: number; max: number }`, `splitSegments(points, stepMs, maxGapSteps?): ChartPoint[][]`, `thinPoints(points, buckets): ChartPoint[]`, `prepareSeries(points, stepMs, maxPoints): ChartPoint[][]`
  - `line-chart.tsx`: `type ChartTone = 'primary' | 'secondary' | 'muted'`, `type ChartAxis = 'left' | 'right'`, `interface ChartSeries { id: string; label: string; points: ChartPoint[]; tone: ChartTone; axis?: ChartAxis }`, `interface ChartLimit { value: number; label: string }`, `LineChart(props: LineChartProps)` mit `{ title, from, to, stepMs, series, formatValue(v, axis), formatTime(t), formatTick?(t), band?, zeroBased?, limit? }`
  - `sparkline.tsx`: `Sparkline({ points: readonly (readonly [number, number])[]; label: string; className?: string })`

- [ ] **Step 1: Skalen-Tests schreiben (rot)**

`web/src/components/charts/scale.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { niceTicks, timeTicks } from './scale'

describe('niceTicks', () => {
  it('uses 1/2/5 × 10^n steps that cover the range', () => {
    expect(niceTicks(0, 20)).toEqual({ min: 0, max: 20, step: 5, values: [0, 5, 10, 15, 20] })
    expect(niceTicks(0, 12)).toEqual({ min: 0, max: 15, step: 5, values: [0, 5, 10, 15] })
    expect(niceTicks(19.2, 20)).toEqual({ min: 19.2, max: 20, step: 0.2, values: [19.2, 19.4, 19.6, 19.8, 20] })
  })

  it('opens up a flat or empty range instead of dividing by zero', () => {
    expect(niceTicks(0, 0)).toEqual({ min: 0, max: 1, step: 0.5, values: [0, 0.5, 1] })
    expect(niceTicks(5, 5)).toEqual({ min: 4.5, max: 5.5, step: 0.5, values: [4.5, 5, 5.5] })
  })

  it('swaps a reversed range and falls back for non-finite input', () => {
    expect(niceTicks(20, 0).values).toEqual([0, 5, 10, 15, 20])
    expect(niceTicks(Number.NaN, 3)).toEqual(niceTicks(0, 1))
  })

  it('handles byte-sized values', () => {
    const gib = 1024 ** 3
    const t = niceTicks(0, 5 * gib)
    expect(t.min).toBe(0)
    expect(t.max).toBeGreaterThanOrEqual(5 * gib)
    expect(t.values.length).toBeLessThanOrEqual(6)
  })
})

describe('timeTicks', () => {
  it('picks a round step with at most count + 1 ticks', () => {
    expect(timeTicks(0, 3_600_000, 5, 0)).toEqual([0, 900_000, 1_800_000, 2_700_000, 3_600_000])
    expect(timeTicks(1_000, 86_401_000, 5, 0)).toEqual([21_600_000, 43_200_000, 64_800_000, 86_400_000])
  })

  it('aligns ticks to local time via the offset', () => {
    // UTC+1: local midnight is 23:00 UTC.
    const ticks = timeTicks(0, 2 * 86_400_000, 2, 3_600_000)
    expect(ticks).toEqual([82_800_000, 169_200_000])
  })
})
```

- [ ] **Step 2: `make check` (rot: Modul fehlt)**

Run: `git add -N web/src/components/charts && make check`
Expected: FAIL `Cannot find module './scale'`.

- [ ] **Step 3: `scale.ts` implementieren**

```ts
// web/src/components/charts/scale.ts
export interface Ticks {
  min: number
  max: number
  step: number
  values: number[]
}

/** Tolerance for float noise (0.8 / 4 = 0.20000000000000018). */
const EPS = 1e-9

function decimals(step: number): number {
  return Math.max(0, -Math.floor(Math.log10(step)))
}

/** "Nice" y-axis ticks: steps of 1/2/5 × 10^n that cover [lo, hi] in about `count` intervals. */
export function niceTicks(lo: number, hi: number, count = 4): Ticks {
  if (!Number.isFinite(lo) || !Number.isFinite(hi)) return niceTicks(0, 1, count)
  if (hi < lo) [lo, hi] = [hi, lo]
  if (hi === lo) {
    if (hi === 0) hi = 1
    else {
      const pad = Math.abs(hi) * 0.1
      lo -= pad
      hi += pad
    }
  }
  const raw = (hi - lo) / Math.max(1, count)
  const exp = 10 ** Math.floor(Math.log10(raw))
  const f = raw / exp
  const nice = f <= 1 + EPS ? 1 : f <= 2 + EPS ? 2 : f <= 5 + EPS ? 5 : 10
  const step = nice * exp
  const d = decimals(step)
  const round = (v: number) => Number(v.toFixed(d))
  const min = round(Math.floor(lo / step + EPS) * step)
  const max = round(Math.ceil(hi / step - EPS) * step)
  const values: number[] = []
  for (let i = 0; min + i * step <= max + step / 2; i++) values.push(round(min + i * step))
  return { min, max, step: round(step), values }
}

const TIME_STEPS = [
  60_000, 300_000, 600_000, 900_000, 1_800_000, 3_600_000, 7_200_000, 10_800_000, 21_600_000, 43_200_000, 86_400_000, 172_800_000,
  604_800_000,
]

/**
 * Tick times on round local boundaries (minutes, hours, days) with at most `count` intervals in [from, to].
 * `offsetMs` is the local UTC offset (positive east), by default the browser's at `from`.
 */
export function timeTicks(from: number, to: number, count = 5, offsetMs = -new Date(from).getTimezoneOffset() * 60_000): number[] {
  const span = Math.max(1, to - from)
  const step = TIME_STEPS.find((s) => span / s <= count) ?? TIME_STEPS[TIME_STEPS.length - 1]
  const first = Math.ceil((from + offsetMs) / step) * step - offsetMs
  const out: number[] = []
  for (let t = first; t <= to; t += step) out.push(t)
  return out
}
```

- [ ] **Step 4: Tests für Lücken und Ausdünnen schreiben (rot)**

`web/src/components/charts/thin.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { prepareSeries, splitSegments, thinPoints, type ChartPoint } from './thin'

const STEP = 30_000
const p = (i: number, v: number, min = v, max = v): ChartPoint => ({ t: i * STEP, v, min, max })

describe('splitSegments', () => {
  it('keeps neighbours up to two steps apart in one line', () => {
    const segs = splitSegments([p(0, 1), p(1, 2), p(3, 3)], STEP)
    expect(segs).toHaveLength(1)
  })

  it('cuts where more than two steps are missing', () => {
    const segs = splitSegments([p(0, 1), p(1, 2), p(5, 4), p(6, 5)], STEP)
    expect(segs.map((s) => s.map((x) => x.v))).toEqual([
      [1, 2],
      [4, 5],
    ])
  })

  it('returns no segment for no points', () => {
    expect(splitSegments([], STEP)).toEqual([])
  })
})

describe('thinPoints', () => {
  it('leaves short series alone', () => {
    const pts = [p(0, 1), p(1, 2)]
    expect(thinPoints(pts, 5)).toEqual(pts)
  })

  it('averages buckets but keeps their extremes', () => {
    const pts = Array.from({ length: 10 }, (_, i) => p(i, i, i, i === 7 ? 100 : i))
    const out = thinPoints(pts, 5)
    expect(out).toHaveLength(5)
    expect(out[0]).toEqual({ t: 0, v: 0.5, min: 0, max: 1 })
    expect(out[3]).toEqual({ t: 6 * STEP, v: 6.5, min: 6, max: 100 })
  })
})

describe('prepareSeries', () => {
  it('thins 2880 raw points to at most the pixel budget without bridging gaps', () => {
    const pts = [...Array.from({ length: 1440 }, (_, i) => p(i, 20)), ...Array.from({ length: 1440 }, (_, i) => p(i + 1500, 19))]
    const segs = prepareSeries(pts, STEP, 580)
    expect(segs).toHaveLength(2)
    expect(segs.flat().length).toBeLessThanOrEqual(580)
    expect(segs[0].every((x) => x.v === 20)).toBe(true)
  })
})
```

- [ ] **Step 5: `thin.ts` implementieren**

```ts
// web/src/components/charts/thin.ts
/** One plotted value: for raw data min = max = v; for 5-min/1-h averages the API's min/max (spec §6.1). */
export interface ChartPoint {
  t: number
  v: number
  min: number
  max: number
}

/** Splits a series where neighbours are more than `maxGapSteps` steps apart: a line never bridges a gap (spec §7.4). */
export function splitSegments(points: readonly ChartPoint[], stepMs: number, maxGapSteps = 2): ChartPoint[][] {
  const out: ChartPoint[][] = []
  let current: ChartPoint[] = []
  for (const point of points) {
    const prev = current[current.length - 1]
    if (prev && point.t - prev.t > stepMs * maxGapSteps) {
      out.push(current)
      current = []
    }
    current.push(point)
  }
  if (current.length > 0) out.push(current)
  return out
}

/** At most `buckets` points: each bucket keeps the average of v and the extreme min/max, so peaks survive. */
export function thinPoints(points: readonly ChartPoint[], buckets: number): ChartPoint[] {
  if (buckets < 1 || points.length <= buckets) return [...points]
  const out: ChartPoint[] = []
  const size = points.length / buckets
  for (let b = 0; b < buckets; b++) {
    const start = Math.floor(b * size)
    const end = Math.floor((b + 1) * size)
    if (end <= start) continue
    let sum = 0
    let min = Infinity
    let max = -Infinity
    for (let i = start; i < end; i++) {
      const point = points[i]
      sum += point.v
      min = Math.min(min, point.min)
      max = Math.max(max, point.max)
    }
    out.push({ t: points[start].t, v: sum / (end - start), min, max })
  }
  return out
}

/** Gaps first, then thinning per segment in proportion to its length (at least one point each). */
export function prepareSeries(points: readonly ChartPoint[], stepMs: number, maxPoints: number): ChartPoint[][] {
  const total = Math.max(1, points.length)
  return splitSegments(points, stepMs).map((segment) =>
    thinPoints(segment, Math.max(1, Math.floor((maxPoints * segment.length) / total))),
  )
}
```

- [ ] **Step 6: Texte `charts` ergänzen**

In `web/src/i18n/de.ts` als neuen Block vor `dashboard: {` einfügen:

```ts
  charts: {
    show_table: 'Als Tabelle anzeigen',
    hide_table: 'Tabelle ausblenden',
    time: 'Zeit',
    no_data: 'Keine Daten in diesem Zeitraum.',
    summary: '{{label}}: aktuell {{current}}, Minimum {{min}}, Maximum {{max}}',
    summary_empty: '{{label}}: keine Daten',
    band: '{{value}} ({{min}} – {{max}})',
    keyboard_hint: 'Mit den Pfeiltasten durch die Werte gehen, Esc schließt die Anzeige.',
  },
```

In `web/src/i18n/en.ts` an derselben Stelle:

```ts
  charts: {
    show_table: 'Show as table',
    hide_table: 'Hide table',
    time: 'Time',
    no_data: 'No data in this period.',
    summary: '{{label}}: currently {{current}}, minimum {{min}}, maximum {{max}}',
    summary_empty: '{{label}}: no data',
    band: '{{value}} ({{min}} – {{max}})',
    keyboard_hint: 'Use the arrow keys to step through the values, Esc closes the readout.',
  },
```

- [ ] **Step 7: Diagramm-Tests schreiben (rot)**

`web/src/components/charts/line-chart.test.tsx`:

```tsx
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { LineChart, type ChartSeries } from './line-chart'
import type { ChartPoint } from './thin'

const STEP = 30_000
function pts(values: (number | null)[]): ChartPoint[] {
  return values.flatMap((v, i) => (v === null ? [] : [{ t: i * STEP, v, min: v, max: v }]))
}
const base = {
  title: 'Spieler',
  from: 0,
  to: 9 * STEP,
  stepMs: STEP,
  formatValue: (v: number) => String(Math.round(v * 10) / 10),
  formatTime: (t: number) => `T${t / STEP}`,
}
const players = (values: (number | null)[]): ChartSeries => ({ id: 'players', label: 'Spieler', tone: 'primary', points: pts(values) })

describe('LineChart', () => {
  it('summarises each series for assistive technology', () => {
    render(<LineChart {...base} series={[players([1, 5, 3])]} />)
    const chart = screen.getByRole('img', { name: 'Spieler' })
    expect(chart).toHaveAccessibleDescription(/Spieler: aktuell 3, Minimum 1, Maximum 5/)
  })

  it('shows a hint instead of an empty chart', () => {
    render(<LineChart {...base} series={[players([])]} />)
    expect(screen.getByText('Keine Daten in diesem Zeitraum.')).toBeInTheDocument()
    expect(screen.queryByRole('img')).not.toBeInTheDocument()
  })

  it('draws one line per segment and never bridges a gap', () => {
    const { container } = render(<LineChart {...base} series={[players([1, 2, null, null, null, 4, 5])]} />)
    expect(container.querySelectorAll('[data-testid="chart-line"][data-series="players"]')).toHaveLength(2)
  })

  it('marks a single isolated point with a dot', () => {
    const { container } = render(<LineChart {...base} series={[players([null, null, null, null, 7])]} />)
    expect(container.querySelectorAll('[data-testid="chart-dot"][data-series="players"]')).toHaveLength(1)
  })

  it('walks through the values with the keyboard and announces them', async () => {
    const user = userEvent.setup()
    render(<LineChart {...base} series={[players([1, 5, 3])]} />)
    const chart = screen.getByRole('img', { name: 'Spieler' })
    chart.focus()
    await user.keyboard('{End}')
    expect(screen.getByRole('status')).toHaveTextContent('T2 – Spieler: 3')
    await user.keyboard('{ArrowLeft}')
    expect(screen.getByRole('status')).toHaveTextContent('T1 – Spieler: 5')
    await user.keyboard('{Home}')
    expect(screen.getByRole('status')).toHaveTextContent('T0 – Spieler: 1')
    await user.keyboard('{Escape}')
    expect(screen.getByRole('status')).toHaveTextContent('')
  })

  it('follows the mouse to the nearest value', () => {
    render(<LineChart {...base} series={[players([1, 5, 3, 4, 4, 4, 4, 4, 4, 4])]} />)
    const chart = screen.getByRole('img', { name: 'Spieler' })
    vi.spyOn(chart, 'getBoundingClientRect').mockReturnValue({ left: 0, top: 0, width: 640, height: 200, right: 640, bottom: 200, x: 0, y: 0, toJSON: () => ({}) })
    // Inner width 640 - 48 - 12 = 580; t = 1 step sits at 48 + 580 / 9 ≈ 112.
    fireEvent.mouseMove(chart, { clientX: 112, clientY: 50 })
    expect(screen.getByRole('status')).toHaveTextContent('T1 – Spieler: 5')
    fireEvent.mouseLeave(chart)
    expect(screen.getByRole('status')).toHaveTextContent('')
  })

  it('offers the values as a table', async () => {
    const user = userEvent.setup()
    render(<LineChart {...base} series={[players([1, 5, 3])]} />)
    await user.click(screen.getByRole('button', { name: 'Als Tabelle anzeigen' }))
    const table = screen.getByRole('table', { name: 'Spieler' })
    expect(within(table).getAllByRole('row')).toHaveLength(4)
    expect(within(table).getByRole('columnheader', { name: 'Zeit' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Tabelle ausblenden' }))
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('puts a second series on a right axis', () => {
    const series: ChartSeries[] = [
      { id: 'tps', label: 'TPS', tone: 'primary', points: pts([20, 19.5]) },
      { id: 'mspt', label: 'MSPT', tone: 'secondary', axis: 'right', points: pts([3, 40]) },
    ]
    const { container } = render(<LineChart {...base} title="TPS und MSPT" series={series} />)
    expect(container.querySelectorAll('[data-axis="right"]').length).toBeGreaterThan(0)
    expect(container.querySelectorAll('[data-testid="chart-line"]')).toHaveLength(2)
  })

  it('draws a limit line and includes it in the scale', () => {
    const { container } = render(<LineChart {...base} series={[players([1, 2])]} limit={{ value: 10, label: 'Limit' }} />)
    expect(container.querySelector('[data-testid="chart-limit"]')).not.toBeNull()
    expect([...container.querySelectorAll('[data-axis="left"]')].map((n) => n.textContent)).toContain('10')
  })

  it('fills the min–max band for averaged data', () => {
    const series: ChartSeries[] = [{ id: 'tps', label: 'TPS', tone: 'primary', points: [{ t: 0, v: 19, min: 15, max: 20 }, { t: STEP, v: 19.5, min: 19, max: 20 }] }]
    const { container } = render(<LineChart {...base} series={series} band />)
    expect(container.querySelector('[data-testid="chart-band"]')).not.toBeNull()
  })
})
```

`web/src/components/charts/sparkline.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Sparkline } from './sparkline'

describe('Sparkline', () => {
  it('scales from zero to the maximum', () => {
    render(<Sparkline points={[[0, 0], [1, 10]]} label="Spieler" />)
    const svg = screen.getByRole('img', { name: 'Spieler' })
    expect(svg.querySelector('path')).toHaveAttribute('d', 'M0,23 L100,1')
  })

  it('renders nothing for fewer than two points', () => {
    const { container } = render(<Sparkline points={[[0, 3]]} label="Spieler" />)
    expect(container).toBeEmptyDOMElement()
  })

  it('keeps a flat zero line at the bottom', () => {
    render(<Sparkline points={[[0, 0], [5, 0]]} label="Spieler" />)
    expect(screen.getByRole('img', { name: 'Spieler' }).querySelector('path')).toHaveAttribute('d', 'M0,23 L100,23')
  })
})
```

- [ ] **Step 8: `make check` (rot)**

Run: `make check`
Expected: FAIL `Cannot find module './line-chart'` / `'./sparkline'`.

- [ ] **Step 9: `sparkline.tsx` implementieren**

```tsx
// web/src/components/charts/sparkline.tsx
import { cn } from '@/lib/utils'

const W = 100
const H = 24

const r = (n: number) => Math.round(n * 100) / 100

/** A tiny zero-based line without axes; the caller's label carries the numbers (spec §7.1). */
export function Sparkline({ points, label, className }: { points: readonly (readonly [number, number])[]; label: string; className?: string }) {
  if (points.length < 2) return null
  const t0 = points[0][0]
  const span = points[points.length - 1][0] - t0 || 1
  const max = Math.max(1, ...points.map((p) => p[1]))
  const d = points.map(([t, v], i) => `${i === 0 ? 'M' : 'L'}${r(((t - t0) / span) * W)},${r(H - 1 - (Math.max(0, v) / max) * (H - 2))}`).join(' ')
  return (
    <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label={label} className={cn('h-6 w-full', className)}>
      <path d={d} className="stroke-primary fill-none" strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
    </svg>
  )
}
```

- [ ] **Step 10: `line-chart.tsx` implementieren**

```tsx
// web/src/components/charts/line-chart.tsx
import { useId, useMemo, useState, type KeyboardEvent, type MouseEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { niceTicks, timeTicks, type Ticks } from './scale'
import { prepareSeries, type ChartPoint } from './thin'

export type ChartTone = 'primary' | 'secondary' | 'muted'
export type ChartAxis = 'left' | 'right'

export interface ChartSeries {
  id: string
  label: string
  points: ChartPoint[]
  tone: ChartTone
  axis?: ChartAxis
}

export interface ChartLimit {
  value: number
  label: string
}

export interface LineChartProps {
  title: string
  from: number
  to: number
  /** Distance of two stored points (V3 step_ms); gaps above two steps are not bridged. */
  stepMs: number
  series: ChartSeries[]
  formatValue(value: number, axis: ChartAxis): string
  /** Time in the readout and the table. */
  formatTime(t: number): string
  /** Time on the x axis (default formatTime). */
  formatTick?(t: number): string
  /** Draws the min–max band (5-min and 1-h averages). */
  band?: boolean
  /** The y axis starts at 0 (default true). */
  zeroBased?: boolean
  /** Dashed reference line on the left axis, e.g. the RAM limit. */
  limit?: ChartLimit
}

const W = 640
const H = 200
const M_TOP = 10
const M_BOTTOM = 24
const M_SIDE = 48
const M_RIGHT_EMPTY = 12

const STROKE: Record<ChartTone, string> = { primary: 'stroke-primary', secondary: 'stroke-[var(--chart-2)]', muted: 'stroke-muted-foreground' }
const FILL: Record<ChartTone, string> = { primary: 'fill-primary', secondary: 'fill-[var(--chart-2)]', muted: 'fill-muted-foreground' }
const SWATCH: Record<ChartTone, string> = { primary: 'bg-primary', secondary: 'bg-[var(--chart-2)]', muted: 'bg-muted-foreground' }

function axisTicks(series: ChartSeries[], axis: ChartAxis, band: boolean, zeroBased: boolean, extra?: number): Ticks | null {
  let lo = Infinity
  let hi = -Infinity
  for (const s of series) {
    if ((s.axis ?? 'left') !== axis) continue
    for (const p of s.points) {
      lo = Math.min(lo, band ? p.min : p.v)
      hi = Math.max(hi, band ? p.max : p.v)
    }
  }
  if (lo === Infinity) return null
  if (extra !== undefined) hi = Math.max(hi, extra)
  if (zeroBased) lo = Math.min(0, lo)
  return niceTicks(lo, hi)
}

/** Index of the time closest to t (times ascending). */
function nearestIndex(times: readonly number[], t: number): number {
  let lo = 0
  let hi = times.length - 1
  while (hi - lo > 1) {
    const mid = (lo + hi) >> 1
    if (times[mid] < t) lo = mid
    else hi = mid
  }
  return Math.abs(times[lo] - t) <= Math.abs(times[hi] - t) ? lo : hi
}

function nearestPoint(points: readonly ChartPoint[], t: number, tolerance: number): ChartPoint | null {
  if (points.length === 0) return null
  const p = points[nearestIndex(points.map((x) => x.t), t)]
  return Math.abs(p.t - t) <= tolerance ? p : null
}

function stats(points: readonly ChartPoint[]) {
  let min = Infinity
  let max = -Infinity
  for (const p of points) {
    min = Math.min(min, p.v)
    max = Math.max(max, p.v)
  }
  return { current: points[points.length - 1].v, min, max }
}

/** Line chart for metric series (spec §7.4): own SVG, theme colours, keyboard readout and a table alternative. */
export function LineChart({ title, from, to, stepMs, series, formatValue, formatTime, formatTick, band = false, zeroBased = true, limit }: LineChartProps) {
  const { t } = useTranslation()
  const titleId = useId()
  const descId = useId()
  const hintId = useId()
  const tableId = useId()
  const [active, setActive] = useState<number | null>(null)
  const [showTable, setShowTable] = useState(false)
  const tick = formatTick ?? formatTime

  const hasRight = series.some((s) => s.axis === 'right' && s.points.length > 0)
  const marginRight = hasRight ? M_SIDE : M_RIGHT_EMPTY
  const innerW = W - M_SIDE - marginRight
  const innerH = H - M_TOP - M_BOTTOM
  const span = Math.max(1, to - from)

  const left = useMemo(() => axisTicks(series, 'left', band, zeroBased, limit?.value), [series, band, zeroBased, limit?.value])
  const right = useMemo(() => (hasRight ? axisTicks(series, 'right', band, zeroBased) : null), [series, band, zeroBased, hasRight])
  const prepared = useMemo(
    () => series.map((s) => ({ ...s, segments: prepareSeries(s.points, stepMs, Math.floor(innerW)) })),
    [series, stepMs, innerW],
  )
  // The readout walks over the (thinned) times of the first series with data.
  const anchor = prepared.find((s) => s.points.length > 0)
  const times = useMemo(() => (anchor ? anchor.segments.flat().map((p) => p.t) : []), [anchor])
  const tolerance = Math.max(stepMs, span / innerW) * 2

  const description = series
    .map((s) => {
      if (s.points.length === 0) return t('charts.summary_empty', { label: s.label })
      const st = stats(s.points)
      const axis = s.axis ?? 'left'
      return t('charts.summary', { label: s.label, current: formatValue(st.current, axis), min: formatValue(st.min, axis), max: formatValue(st.max, axis) })
    })
    .join('; ')

  if (!anchor) {
    return (
      <figure className="space-y-2">
        <figcaption className="text-sm font-medium">{title}</figcaption>
        <p className="text-muted-foreground text-sm">{t('charts.no_data')}</p>
      </figure>
    )
  }

  const x = (time: number) => M_SIDE + ((time - from) / span) * innerW
  const y = (v: number, ticks: Ticks) => M_TOP + innerH - ((v - ticks.min) / (ticks.max - ticks.min || 1)) * innerH
  const ticksOf = (axis: ChartAxis | undefined) => ((axis ?? 'left') === 'right' ? right : left)

  const activeTime = active === null ? null : times[active]
  const readout =
    activeTime === null
      ? ''
      : `${formatTime(activeTime)} – ${prepared
          .map((s) => {
            const p = nearestPoint(s.segments.flat(), activeTime, tolerance)
            if (!p) return `${s.label}: –`
            const axis = s.axis ?? 'left'
            const value = formatValue(p.v, axis)
            return `${s.label}: ${band && p.min !== p.max ? t('charts.band', { value, min: formatValue(p.min, axis), max: formatValue(p.max, axis) }) : value}`
          })
          .join(', ')}`

  function onKeyDown(e: KeyboardEvent<SVGSVGElement>) {
    const last = times.length - 1
    let next: number | null = active
    if (e.key === 'ArrowRight') next = active === null ? last : Math.min(last, active + 1)
    else if (e.key === 'ArrowLeft') next = active === null ? last : Math.max(0, active - 1)
    else if (e.key === 'Home') next = 0
    else if (e.key === 'End') next = last
    else if (e.key === 'Escape') next = null
    else return
    e.preventDefault()
    setActive(next)
  }

  function onMouseMove(e: MouseEvent<SVGSVGElement>) {
    const rect = e.currentTarget.getBoundingClientRect()
    if (rect.width <= 0) return
    const px = ((e.clientX - rect.left) / rect.width) * W
    const time = from + ((px - M_SIDE) / innerW) * span
    setActive(nearestIndex(times, time))
  }

  const xTicks = timeTicks(from, to)

  return (
    <figure className="space-y-2">
      <figcaption className="flex flex-wrap items-center justify-between gap-2 text-sm">
        <span className="font-medium">{title}</span>
        <span className="flex flex-wrap gap-3" data-testid="chart-legend">
          {series.map((s) => (
            <span key={s.id} className="text-muted-foreground flex items-center gap-1.5">
              <span aria-hidden="true" className={cn('inline-block h-0.5 w-3 rounded-full', SWATCH[s.tone])} />
              {s.label}
            </span>
          ))}
          {limit ? (
            <span className="text-muted-foreground flex items-center gap-1.5">
              <span aria-hidden="true" className="border-destructive inline-block w-3 border-t border-dashed" />
              {limit.label}
            </span>
          ) : null}
        </span>
      </figcaption>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        role="img"
        tabIndex={0}
        aria-labelledby={titleId}
        aria-describedby={`${descId} ${hintId}`}
        className="focus-visible:ring-ring h-auto w-full touch-pan-y rounded-sm outline-none select-none focus-visible:ring-2"
        onKeyDown={onKeyDown}
        onMouseMove={onMouseMove}
        onMouseLeave={() => setActive(null)}
        onBlur={() => setActive(null)}
      >
        <title id={titleId}>{title}</title>
        <desc id={descId}>{description}</desc>
        {left?.values.map((v) => (
          <g key={`l${v}`}>
            <line x1={M_SIDE} x2={W - marginRight} y1={y(v, left)} y2={y(v, left)} className="stroke-border" strokeWidth={1} />
            <text data-axis="left" x={M_SIDE - 6} y={y(v, left)} dy="0.32em" textAnchor="end" className="fill-muted-foreground text-[10px]">
              {formatValue(v, 'left')}
            </text>
          </g>
        ))}
        {right?.values.map((v) => (
          <text key={`r${v}`} data-axis="right" x={W - marginRight + 6} y={y(v, right)} dy="0.32em" className="fill-muted-foreground text-[10px]">
            {formatValue(v, 'right')}
          </text>
        ))}
        {xTicks.map((time) => (
          <text key={time} x={x(time)} y={H - 6} textAnchor="middle" className="fill-muted-foreground text-[10px]">
            {tick(time)}
          </text>
        ))}
        {limit && left ? (
          <line
            data-testid="chart-limit"
            x1={M_SIDE}
            x2={W - marginRight}
            y1={y(limit.value, left)}
            y2={y(limit.value, left)}
            className="stroke-destructive"
            strokeWidth={1}
            strokeDasharray="4 3"
          />
        ) : null}
        {prepared.map((s) => {
          const ticks = ticksOf(s.axis)
          if (!ticks) return null
          return (
            <g key={s.id}>
              {band
                ? s.segments
                    .filter((seg) => seg.length > 1)
                    .map((seg, i) => (
                      <polygon
                        key={`b${i}`}
                        data-testid="chart-band"
                        data-series={s.id}
                        points={[...seg.map((p) => `${x(p.t)},${y(p.max, ticks)}`), ...[...seg].reverse().map((p) => `${x(p.t)},${y(p.min, ticks)}`)].join(' ')}
                        className={cn(FILL[s.tone], 'opacity-15')}
                      />
                    ))
                : null}
              {s.segments.map((seg, i) =>
                seg.length === 1 ? (
                  <circle key={`d${i}`} data-testid="chart-dot" data-series={s.id} cx={x(seg[0].t)} cy={y(seg[0].v, ticks)} r={2} className={FILL[s.tone]} />
                ) : (
                  <path
                    key={`l${i}`}
                    data-testid="chart-line"
                    data-series={s.id}
                    d={seg.map((p, j) => `${j === 0 ? 'M' : 'L'}${x(p.t).toFixed(1)},${y(p.v, ticks).toFixed(1)}`).join(' ')}
                    className={cn(STROKE[s.tone], 'fill-none')}
                    strokeWidth={1.5}
                    strokeLinejoin="round"
                  />
                ),
              )}
            </g>
          )
        })}
        {activeTime !== null ? (
          <g data-testid="chart-cursor">
            <line x1={x(activeTime)} x2={x(activeTime)} y1={M_TOP} y2={M_TOP + innerH} className="stroke-foreground/40" strokeWidth={1} />
            {prepared.map((s) => {
              const ticks = ticksOf(s.axis)
              const p = ticks ? nearestPoint(s.segments.flat(), activeTime, tolerance) : null
              return p && ticks ? <circle key={s.id} cx={x(p.t)} cy={y(p.v, ticks)} r={3} className={FILL[s.tone]} /> : null
            })}
          </g>
        ) : null}
      </svg>
      <p id={hintId} className="sr-only">
        {t('charts.keyboard_hint')}
      </p>
      <p role="status" aria-live="polite" className="text-muted-foreground min-h-5 text-xs tabular-nums">
        {readout}
      </p>
      <Button type="button" variant="ghost" size="sm" aria-expanded={showTable} aria-controls={tableId} onClick={() => setShowTable((v) => !v)}>
        {showTable ? t('charts.hide_table') : t('charts.show_table')}
      </Button>
      {showTable ? (
        <div id={tableId} className="max-h-64 overflow-auto rounded-md border">
          <table className="w-full text-sm tabular-nums">
            <caption className="sr-only">{title}</caption>
            <thead>
              <tr>
                <th scope="col" className="px-2 py-1 text-left font-medium">
                  {t('charts.time')}
                </th>
                {series.map((s) => (
                  <th key={s.id} scope="col" className="px-2 py-1 text-right font-medium">
                    {s.label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {times.map((time) => (
                <tr key={time} className="border-t">
                  <td className="px-2 py-1">{formatTime(time)}</td>
                  {prepared.map((s) => {
                    const p = nearestPoint(s.segments.flat(), time, tolerance)
                    return (
                      <td key={s.id} className="px-2 py-1 text-right">
                        {p ? formatValue(p.v, s.axis ?? 'left') : '–'}
                      </td>
                    )
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </figure>
  )
}
```

Hinweis: Das `<svg>` hat sowohl `<title>` als auch `aria-labelledby` – Testing Library liest den Namen aus `aria-labelledby` („Spieler“), die Beschreibung aus `aria-describedby` (Zusammenfassung + Tastatur-Hinweis); deshalb prüft der Test die Beschreibung mit einem regulären Ausdruck.

- [ ] **Step 11: Tests auf der VM (grün)**

Run: `make check && make test-vm SUITE=web` (bzw. gezielt `npx vitest run src/components/charts`)
Expected: PASS für `scale.test.ts`, `thin.test.ts`, `line-chart.test.tsx`, `sparkline.test.tsx`; `i18n.test.ts` grün (de/en gleich).

- [ ] **Step 12: Commit**

```bash
git add web/src/components/charts web/src/i18n/de.ts web/src/i18n/en.ts
git commit -m "feat(web): own SVG line chart and sparkline for monitoring

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 12: Monitoring-Modell, API und Dashboard

**Files:**
- Create: `web/src/features/monitoring/api.ts`, `model.ts`, `model.test.ts`, `meter.tsx`, `server-metrics.tsx`, `host-card.tsx`, `test-utils.tsx`
- Modify: `web/src/features/dashboard/dashboard-page.tsx`, `server-card.tsx`, `dashboard-page.test.tsx`, `web/src/i18n/de.ts`, `en.ts`, `i18n.test.ts`

**Interfaces:**
- Consumes: Typen aus Phase A (V3), `Sparkline` (Aufgabe 11), `formatGiB`, `formatCpu` (`features/servers/format`), `useFormat`, `useMe`, `FieldError` (`features/servers/validation`).
- Produces (für Aufgabe 13):
  - `api.ts`: `LIVE_POLL_MS = 5_000`, `OVERVIEW_POLL_MS = 30_000`, `metricsPollMs(range)`, `monitoringOverviewKey`, `monitoringSettingsKey`, `serverMetricsKey(id, range)`, `liveMetricsKey(id)`, `monitoringOverviewQueryOptions`, `serverMetricsQueryOptions(id, range)`, `liveMetricsQueryOptions(id)`, `monitoringSettingsQueryOptions`, `updateMonitoringSettings(body): Promise<MonitoringSettings>`, `updateServerAlerts(id, downAlerts): Promise<void>`
  - `model.ts`: `RANGES`, `RANGE_MS`, `GIB`, `toChartPoints(points)`, `metricPoints(data, metric)`, `lastValue(points)`, `type Reachability`, `reachability(server, metrics)`, `alertKey(kind)`, `tpsSourceKey(source)`, `percent(used, total)`, `formatDecimal(n, locale)`, `SETTINGS_FIELDS`, `type SettingsField`, `type SettingsDraft`, `SETTINGS_LIMITS`, `draftFromSettings(s)`, `validateSettings(d): SettingsValidation`, `sameSettings(req, s)`
  - `meter.tsx`: `Meter({ label, value, max, valueText, danger? })`
  - `test-utils.tsx`: `overviewServer(id, over)`, `hostSnapshot(over)`, `liveMetrics(over)`, `metricsSeries(range, series)`, `monitoringSettings(over)`, `mockOverview(o)`, `mockLive(l)`, `mockMetrics(fn)`, `mockMonitoringSettings(s)`

- [ ] **Step 1: Modell-Tests schreiben (rot)**

`web/src/features/monitoring/model.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import type { MonitoringSettings, OverviewServer, Server } from '@/api/types'
import {
  alertKey,
  draftFromSettings,
  GIB,
  lastValue,
  metricPoints,
  percent,
  reachability,
  sameSettings,
  toChartPoints,
  tpsSourceKey,
  validateSettings,
} from './model'

const settings: MonitoringSettings = {
  disk_min_percent: 10,
  disk_min_bytes: 10 * GIB,
  down_after_min: 3,
  crash_restarts: 3,
  crash_window_min: 10,
  alerts: [],
}

function srv(state: Server['status']['state']): Server {
  return { status: { state } } as Server
}

describe('monitoring model', () => {
  it('turns API points into chart points', () => {
    expect(toChartPoints([[1, 2, 1.5, 3]])).toEqual([{ t: 1, v: 2, min: 1.5, max: 3 }])
    expect(toChartPoints(undefined)).toEqual([])
    expect(metricPoints({ range: '1h', step_ms: 30_000, series: { tps: [[0, 20, 20, 20]] } }, 'tps')).toHaveLength(1)
    expect(metricPoints({ range: '1h', step_ms: 30_000, series: {} }, 'tps')).toEqual([])
    expect(lastValue([])).toBeNull()
    expect(lastValue(toChartPoints([[0, 1, 1, 1], [1, 4, 4, 4]]))).toBe(4)
  })

  it('derives reachability from the state and the ping', () => {
    const up = { up: true } as OverviewServer
    const down = { up: false } as OverviewServer
    expect(reachability(srv('running'), up)).toBe('reachable')
    expect(reachability(srv('running'), down)).toBe('unreachable')
    expect(reachability(srv('running'), { up: null } as OverviewServer)).toBeNull()
    expect(reachability(srv('running'), undefined)).toBeNull()
    expect(reachability(srv('stopped'), down)).toBeNull()
  })

  it('maps alert kinds and TPS sources to translation keys', () => {
    expect(alertKey('server.crash_loop')).toBe('monitoring.alert.server_crash_loop')
    expect(alertKey('host.disk_low')).toBe('monitoring.alert.host_disk_low')
    expect(tpsSourceKey('tick_query')).toBe('monitoring.tps_source.tick_query')
  })

  it('clamps percentages and refuses missing totals', () => {
    expect(percent(5, 10)).toBe(50)
    expect(percent(15, 10)).toBe(100)
    expect(percent(null, 10)).toBeNull()
    expect(percent(5, 0)).toBeNull()
  })

  it('edits the disk threshold in GiB and validates every field', () => {
    const draft = draftFromSettings(settings)
    expect(draft).toEqual({ disk_min_percent: '10', disk_min_gib: '10', down_after_min: '3', crash_restarts: '3', crash_window_min: '10' })
    expect(validateSettings(draft).request).toEqual({ disk_min_percent: 10, disk_min_bytes: 10 * GIB, down_after_min: 3, crash_restarts: 3, crash_window_min: 10 })
    expect(sameSettings(validateSettings(draft).request!, settings)).toBe(true)

    const bad = validateSettings({ ...draft, down_after_min: '0', crash_restarts: '2.5', disk_min_gib: '1,234' })
    expect(bad.request).toBeNull()
    expect(bad.errors.down_after_min).toEqual({ key: 'monitoring.settings.validation.range', params: { min: 1, max: 60 } })
    expect(bad.errors.crash_restarts).toEqual({ key: 'monitoring.settings.validation.integer' })
    expect(bad.errors.disk_min_gib).toEqual({ key: 'monitoring.settings.validation.decimal' })

    const half = validateSettings({ ...draft, disk_min_gib: '1,5' })
    expect(half.request?.disk_min_bytes).toBe(1.5 * GIB)
    expect(validateSettings({ ...draft, disk_min_gib: '10241' }).errors.disk_min_gib?.key).toBe('monitoring.settings.validation.range')
  })

  it('shows odd byte values with two decimals', () => {
    expect(draftFromSettings({ ...settings, disk_min_bytes: 1.25 * GIB }).disk_min_gib).toBe('1.25')
  })
})
```

- [ ] **Step 2: `model.ts` und `api.ts` implementieren**

```ts
// web/src/features/monitoring/model.ts
import type { AlertKind, MetricPoint, MetricsRange, MetricsSeries, MonitoringSettings, OverviewServer, Server, TpsSource, UpdateMonitoringSettingsRequest } from '@/api/types'
import type { ChartPoint } from '@/components/charts/thin'
import type { FieldError } from '@/features/servers/validation'

export const RANGES = ['1h', '24h', '7d', '30d'] as const satisfies readonly MetricsRange[]
export const RANGE_MS: Record<MetricsRange, number> = { '1h': 3_600_000, '24h': 86_400_000, '7d': 7 * 86_400_000, '30d': 30 * 86_400_000 }
export const GIB = 1024 ** 3

export function toChartPoints(points: readonly MetricPoint[] | undefined): ChartPoint[] {
  return (points ?? []).map(([t, v, min, max]) => ({ t, v, min, max }))
}

export function metricPoints(data: MetricsSeries | undefined, metric: string): ChartPoint[] {
  return toChartPoints(data?.series[metric])
}

export function lastValue(points: readonly ChartPoint[]): number | null {
  return points.length > 0 ? points[points.length - 1].v : null
}

/** Only for running containers; "stopped" is already the status badge (spec §7.1). */
export type Reachability = 'reachable' | 'unreachable'

export function reachability(server: Server, metrics: OverviewServer | undefined): Reachability | null {
  if (server.status.state !== 'running' || !metrics) return null
  if (metrics.up === true) return 'reachable'
  if (metrics.up === false) return 'unreachable'
  return null
}

export function alertKey(kind: AlertKind): string {
  return `monitoring.alert.${kind.replace('.', '_')}`
}

export function tpsSourceKey(source: TpsSource): string {
  return `monitoring.tps_source.${source}`
}

export function percent(used: number | null, total: number | null): number | null {
  if (used === null || total === null || total <= 0) return null
  return Math.min(100, Math.max(0, (used / total) * 100))
}

export function formatDecimal(n: number, locale: string, digits = 1): string {
  return new Intl.NumberFormat(locale, { maximumFractionDigits: digits }).format(n)
}

export const SETTINGS_FIELDS = ['disk_min_percent', 'disk_min_gib', 'down_after_min', 'crash_restarts', 'crash_window_min'] as const
export type SettingsField = (typeof SETTINGS_FIELDS)[number]
export type SettingsDraft = Record<SettingsField, string>

/** Spec §4 / Global Constraints; the disk threshold is edited in GiB (1 GiB – 10 TiB). */
export const SETTINGS_LIMITS: Record<SettingsField, readonly [number, number]> = {
  disk_min_percent: [1, 50],
  disk_min_gib: [1, 10_240],
  down_after_min: [1, 60],
  crash_restarts: [2, 20],
  crash_window_min: [1, 120],
}

export function draftFromSettings(s: MonitoringSettings): SettingsDraft {
  const gib = s.disk_min_bytes / GIB
  return {
    disk_min_percent: String(s.disk_min_percent),
    disk_min_gib: Number.isInteger(gib) ? String(gib) : String(Math.round(gib * 100) / 100),
    down_after_min: String(s.down_after_min),
    crash_restarts: String(s.crash_restarts),
    crash_window_min: String(s.crash_window_min),
  }
}

export interface SettingsValidation {
  errors: Partial<Record<SettingsField, FieldError>>
  request: UpdateMonitoringSettingsRequest | null
}

export function validateSettings(d: SettingsDraft): SettingsValidation {
  const errors: Partial<Record<SettingsField, FieldError>> = {}
  const values: Partial<Record<SettingsField, number>> = {}
  for (const field of SETTINGS_FIELDS) {
    const decimal = field === 'disk_min_gib'
    const raw = d[field].trim().replace(',', '.')
    const [min, max] = SETTINGS_LIMITS[field]
    const ok = decimal ? /^\d+(\.\d{1,2})?$/.test(raw) : /^\d+$/.test(raw)
    if (!ok) {
      errors[field] = { key: decimal ? 'monitoring.settings.validation.decimal' : 'monitoring.settings.validation.integer' }
      continue
    }
    const n = Number(raw)
    if (n < min || n > max) errors[field] = { key: 'monitoring.settings.validation.range', params: { min, max } }
    else values[field] = n
  }
  if (Object.keys(errors).length > 0) return { errors, request: null }
  return {
    errors,
    request: {
      disk_min_percent: values.disk_min_percent!,
      disk_min_bytes: Math.round(values.disk_min_gib! * GIB),
      down_after_min: values.down_after_min!,
      crash_restarts: values.crash_restarts!,
      crash_window_min: values.crash_window_min!,
    },
  }
}

export function sameSettings(req: UpdateMonitoringSettingsRequest, s: MonitoringSettings): boolean {
  return (
    req.disk_min_percent === s.disk_min_percent &&
    req.disk_min_bytes === s.disk_min_bytes &&
    req.down_after_min === s.down_after_min &&
    req.crash_restarts === s.crash_restarts &&
    req.crash_window_min === s.crash_window_min
  )
}
```

```ts
// web/src/features/monitoring/api.ts
import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { LiveMetrics, MetricsRange, MetricsSeries, MonitoringOverview, MonitoringSettings, UpdateMonitoringSettingsRequest } from '@/api/types'

/** Live header while the tab is open (spec §6.2). */
export const LIVE_POLL_MS = 5_000
/** Dashboard values follow the 30-s collector pass. */
export const OVERVIEW_POLL_MS = 30_000
/** 1 h shows 30-s values; longer ranges change at most every 5 minutes. */
export const metricsPollMs = (range: MetricsRange) => (range === '1h' ? 30_000 : 300_000)

export const monitoringOverviewKey = ['monitoring', 'overview'] as const
export const monitoringSettingsKey = ['monitoring', 'settings'] as const
/** Below serverKey(id): a deleted or reloaded server takes its metrics along via the servers prefix. */
export const serverMetricsKey = (id: number, range: MetricsRange) => ['servers', id, 'metrics', range] as const
export const liveMetricsKey = (id: number) => ['servers', id, 'metrics', 'live'] as const

export const monitoringOverviewQueryOptions = queryOptions<MonitoringOverview>({
  queryKey: monitoringOverviewKey,
  queryFn: () => unwrap(api.GET('/monitoring/overview')),
})

export function serverMetricsQueryOptions(id: number, range: MetricsRange) {
  return queryOptions<MetricsSeries>({
    queryKey: serverMetricsKey(id, range),
    queryFn: () => unwrap(api.GET('/servers/{id}/metrics', { params: { path: { id }, query: { range } } })),
  })
}

export function liveMetricsQueryOptions(id: number) {
  return queryOptions<LiveMetrics>({
    queryKey: liveMetricsKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/metrics/live', { params: { path: { id } } })),
  })
}

export const monitoringSettingsQueryOptions = queryOptions<MonitoringSettings>({
  queryKey: monitoringSettingsKey,
  queryFn: () => unwrap(api.GET('/monitoring/settings')),
})

export function updateMonitoringSettings(body: UpdateMonitoringSettingsRequest): Promise<MonitoringSettings> {
  return unwrap(api.PUT('/monitoring/settings', { body }))
}

export function updateServerAlerts(id: number, downAlerts: boolean): Promise<void> {
  return unwrapVoid(api.PUT('/servers/{id}/alerts', { params: { path: { id } }, body: { down_alerts: downAlerts } }))
}
```

`GET /monitoring/host` (V3) nutzt diese Phase nicht: Spec §7.1 zeigt nur aktuelle Host-Werte. Die Route bleibt für eine spätere Host-Verlaufsansicht.

- [ ] **Step 3: Test-Hilfen**

```tsx
// web/src/features/monitoring/test-utils.tsx
import { http, HttpResponse } from 'msw'
import type { HostSnapshot, LiveMetrics, MetricsRange, MetricsSeries, MonitoringOverview, MonitoringSettings, OverviewServer } from '@/api/types'
import { server } from '@/mocks/node'
import { GIB } from './model'

export function overviewServer(id: number, over: Partial<OverviewServer> = {}): OverviewServer {
  return {
    id,
    up: true,
    players: 0,
    players_max: 20,
    tps: 20,
    cpu_percent: 5,
    memory_bytes: 2 * GIB,
    memory_limit_bytes: 5 * GIB,
    sparkline: [],
    alerts: [],
    ...over,
  }
}

export function hostSnapshot(over: Partial<HostSnapshot> = {}): HostSnapshot {
  return {
    at: Date.now(),
    cpu_percent: 23.4,
    load1: 0.52,
    memory_used: 6 * GIB,
    memory_total: 16 * GIB,
    disk_free: 80 * GIB,
    disk_total: 200 * GIB,
    alerts: [],
    ...over,
  }
}

export function liveMetrics(over: Partial<LiveMetrics> = {}): LiveMetrics {
  return {
    at: Date.now(),
    up: true,
    players: 2,
    players_max: 20,
    player_names: ['Alex', 'Steve'],
    tps: 19.9,
    mspt: 12.3,
    cpu_percent: 31.5,
    memory_bytes: 3 * GIB,
    memory_limit_bytes: 5 * GIB,
    tps_source: 'tick_query',
    ...over,
  }
}

const STEPS: Record<MetricsRange, number> = { '1h': 30_000, '24h': 300_000, '7d': 3_600_000, '30d': 3_600_000 }

/** Three points per metric ending now; values from `values` (default 1, 2, 3). */
export function metricsSeries(range: MetricsRange, metrics: string[], values: [number, number, number] = [1, 2, 3]): MetricsSeries {
  const step = STEPS[range]
  const now = Date.now()
  const series: MetricsSeries['series'] = {}
  for (const m of metrics) series[m] = values.map((v, i) => [now - (2 - i) * step, v, v, v])
  return { range, step_ms: step, series }
}

export function monitoringSettings(over: Partial<MonitoringSettings> = {}): MonitoringSettings {
  return { disk_min_percent: 10, disk_min_bytes: 10 * GIB, down_after_min: 3, crash_restarts: 3, crash_window_min: 10, alerts: [], ...over }
}

export function mockOverview(o: MonitoringOverview) {
  server.use(http.get('*/api/monitoring/overview', () => HttpResponse.json(o)))
}

export function mockLive(l: LiveMetrics | ((n: number) => LiveMetrics)) {
  const state = { calls: 0 }
  server.use(
    http.get('*/api/servers/:id/metrics/live', () => {
      state.calls += 1
      return HttpResponse.json(typeof l === 'function' ? l(state.calls) : l)
    }),
  )
  return state
}

/** Records the requested ranges and answers with metricsSeries for all server metrics. */
export function mockMetrics() {
  const ranges: string[] = []
  server.use(
    http.get('*/api/servers/:id/metrics', ({ request }) => {
      const range = (new URL(request.url).searchParams.get('range') ?? '1h') as MetricsRange
      ranges.push(range)
      return HttpResponse.json(metricsSeries(range, ['up', 'players', 'players_max', 'tps', 'mspt', 'cpu_percent', 'memory_bytes', 'memory_limit_bytes']))
    }),
  )
  return ranges
}

export function mockMonitoringSettings(s: MonitoringSettings) {
  server.use(http.get('*/api/monitoring/settings', () => HttpResponse.json(s)))
}
```

- [ ] **Step 4: Dashboard-Tests ergänzen (rot)**

In `web/src/features/dashboard/dashboard-page.test.tsx` Importe ergänzen und am Ende des `describe` anfügen:

```tsx
import { GIB } from '@/features/monitoring/model'
import { hostSnapshot, mockOverview, overviewServer } from '@/features/monitoring/test-utils'
```

```tsx
  it('shows players, TPS, CPU, reachability and a sparkline from the monitoring overview', async () => {
    const now = Date.now()
    mockOverview({
      servers: [overviewServer(1, { players: 3, players_max: 20, tps: 19.8, cpu_percent: 12.5, sparkline: [[now - 60_000, 1], [now, 3]] })],
      host: null,
    })
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const card = await screen.findByRole('article', { name: 'survival' })
    expect(await within(card).findByText('3/20')).toBeInTheDocument()
    expect(within(card).getByText('19,8')).toBeInTheDocument()
    expect(within(card).getByText('12,5 %')).toBeInTheDocument()
    expect(within(card).getByText('Erreichbar')).toBeInTheDocument()
    expect(within(card).getByRole('img', { name: 'Spieler in der letzten Stunde: aktuell 3, höchstens 3' })).toBeInTheDocument()
  })

  it('marks an unreachable server and its alerts', async () => {
    mockOverview({ servers: [overviewServer(1, { up: false, players: null, tps: null, alerts: ['server.down'] })], host: null })
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const card = await screen.findByRole('article', { name: 'survival' })
    expect(await within(card).findByText('Nicht erreichbar')).toBeInTheDocument()
    expect(within(card).getByText('Server down')).toBeInTheDocument()
    expect(within(card).getAllByText('–').length).toBeGreaterThanOrEqual(2)
    expect(within(card).queryByText('NaN')).not.toBeInTheDocument()
  })

  it('shows admins a host card with a disk warning', async () => {
    mockOverview({ servers: [], host: hostSnapshot({ disk_free: 5 * GIB, disk_total: 100 * GIB, alerts: ['host.disk_low'] }) })
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    const host = await screen.findByRole('region', { name: 'Host' })
    expect(within(host).getByText('5 GiB frei von 100 GiB')).toBeInTheDocument()
    expect(within(host).getByText('Der freie Platz liegt unter der Alert-Schwelle.')).toBeInTheDocument()
    expect(within(host).getByRole('meter', { name: 'Platte' })).toHaveAttribute('aria-valuenow', String(95 * GIB))
    expect(within(host).getByRole('link', { name: 'Monitoring-Einstellungen' })).toHaveAttribute('href', '/settings')
  })

  it('shows no host card to other users', async () => {
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    await screen.findByRole('article', { name: 'survival' })
    expect(screen.queryByRole('region', { name: 'Host' })).not.toBeInTheDocument()
  })

  it('keeps the cards when the monitoring overview fails', async () => {
    server.use(http.get('*/api/monitoring/overview', () => HttpResponse.json({ error: 'internal', message: 'boom' }, { status: 500 })))
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const card = await screen.findByRole('article', { name: 'survival' })
    expect(within(card).queryByText('Erreichbar')).not.toBeInTheDocument()
  })
```

Dazu oben `import { http, HttpResponse } from 'msw'` und `import { server } from '@/mocks/node'`.

Hinweis zur Platte: Der Balken zeigt die *belegte* Kapazität (`disk_total − disk_free`), der Text den freien Platz.

- [ ] **Step 5: `make check` (rot)**

Run: `git add -N web/src/features/monitoring && make check`
Expected: FAIL (Module `server-metrics`, `host-card` fehlen bzw. Texte fehlen erst im VM-Lauf).

- [ ] **Step 6: Texte `monitoring` (Grundstock) ergänzen**

In `web/src/i18n/de.ts` nach dem Block `backups: { … },` einfügen:

```ts
  monitoring: {
    players: 'Spieler',
    tps: 'TPS',
    mspt: 'MSPT',
    cpu: 'CPU',
    memory: 'RAM',
    missing: '–',
    players_value: '{{players}}/{{max}}',
    percent: '{{value}} %',
    ms: '{{value}} ms',
    gib: '{{value}} GiB',
    sparkline: 'Spieler in der letzten Stunde: aktuell {{current}}, höchstens {{max}}',
    reachability: {
      reachable: 'Erreichbar',
      unreachable: 'Nicht erreichbar',
    },
    alert: {
      server_down: 'Server down',
      server_crash_loop: 'Crash-Loop',
      host_disk_low: 'Platte knapp',
    },
    host: {
      title: 'Host',
      description: 'Zustand des Servers, auf dem mc-panel läuft.',
      cpu: 'CPU',
      memory: 'RAM',
      memory_value: '{{used}} / {{total}} GiB',
      load: 'Load (1 min)',
      disk: 'Platte',
      disk_value: '{{free}} GiB frei von {{total}} GiB',
      disk_low: 'Der freie Platz liegt unter der Alert-Schwelle.',
      settings_link: 'Monitoring-Einstellungen',
    },
  },
```

In `web/src/i18n/en.ts` an derselben Stelle:

```ts
  monitoring: {
    players: 'Players',
    tps: 'TPS',
    mspt: 'MSPT',
    cpu: 'CPU',
    memory: 'RAM',
    missing: '–',
    players_value: '{{players}}/{{max}}',
    percent: '{{value}} %',
    ms: '{{value}} ms',
    gib: '{{value}} GiB',
    sparkline: 'Players in the last hour: currently {{current}}, at most {{max}}',
    reachability: {
      reachable: 'Reachable',
      unreachable: 'Unreachable',
    },
    alert: {
      server_down: 'Server down',
      server_crash_loop: 'Crash loop',
      host_disk_low: 'Disk low',
    },
    host: {
      title: 'Host',
      description: 'State of the machine mc-panel runs on.',
      cpu: 'CPU',
      memory: 'RAM',
      memory_value: '{{used}} / {{total}} GiB',
      load: 'Load (1 min)',
      disk: 'Disk',
      disk_value: '{{free}} GiB free of {{total}} GiB',
      disk_low: 'Free space is below the alert threshold.',
      settings_link: 'Monitoring settings',
    },
  },
```

In `web/src/i18n/i18n.test.ts` in `DYNAMIC_KEYS` ergänzen:

```ts
  ...['server_down', 'server_crash_loop', 'host_disk_low'].map((k) => `monitoring.alert.${k}`),
  ...['reachable', 'unreachable'].map((k) => `monitoring.reachability.${k}`),
```

- [ ] **Step 7: `meter.tsx`, `server-metrics.tsx`, `host-card.tsx` implementieren**

```tsx
// web/src/features/monitoring/meter.tsx
import { cn } from '@/lib/utils'

/** Horizontal bar with role="meter"; the width is the only inline style (allowed by the CSP's style-src). */
export function Meter({ label, value, max, valueText, danger = false }: { label: string; value: number; max: number; valueText: string; danger?: boolean }) {
  const pct = max > 0 ? Math.min(100, Math.max(0, (value / max) * 100)) : 0
  return (
    <div
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={max}
      aria-valuenow={value}
      aria-valuetext={valueText}
      className="bg-muted h-2 w-full overflow-hidden rounded-full"
    >
      <div className={cn('h-full rounded-full', danger ? 'bg-destructive' : 'bg-primary')} style={{ width: `${pct}%` }} />
    </div>
  )
}
```

```tsx
// web/src/features/monitoring/server-metrics.tsx
import { useTranslation } from 'react-i18next'
import type { OverviewServer, Server } from '@/api/types'
import { Sparkline } from '@/components/charts/sparkline'
import { Badge } from '@/components/ui/badge'
import { formatCpu, formatGiB } from '@/features/servers/format'
import { useFormat } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Meter } from './meter'
import { alertKey, formatDecimal, reachability } from './model'

/** Monitoring values on a dashboard card (spec §7.1); every missing value is "–", never 0. */
export function ServerMetrics({ server, metrics }: { server: Server; metrics: OverviewServer | undefined }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  if (!metrics) return null
  const missing = t('monitoring.missing')
  const reach = reachability(server, metrics)
  const players = metrics.players === null ? missing : t('monitoring.players_value', { players: metrics.players, max: metrics.players_max ?? missing })
  const tps = metrics.tps === null ? missing : formatDecimal(metrics.tps, fmt.locale)
  const cpu = metrics.cpu_percent === null ? missing : t('monitoring.percent', { value: formatCpu(metrics.cpu_percent, fmt.locale) })
  const spark = metrics.sparkline
  const sparkMax = spark.reduce((m, [, v]) => Math.max(m, v), 0)

  return (
    <div className="space-y-2 text-sm" data-testid="server-metrics">
      {reach ? (
        <p className={cn('flex items-center gap-2', reach === 'unreachable' ? 'text-destructive' : 'text-muted-foreground')}>
          <span aria-hidden="true" className={cn('inline-block size-2 rounded-full', reach === 'unreachable' ? 'bg-destructive' : 'bg-primary')} />
          {t(`monitoring.reachability.${reach}`)}
        </p>
      ) : null}
      <dl className="grid grid-cols-3 gap-2 tabular-nums">
        <div>
          <dt className="text-muted-foreground text-xs">{t('monitoring.players')}</dt>
          <dd>{players}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground text-xs">{t('monitoring.tps')}</dt>
          <dd>{tps}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground text-xs">{t('monitoring.cpu')}</dt>
          <dd>{cpu}</dd>
        </div>
      </dl>
      {metrics.memory_bytes !== null && metrics.memory_limit_bytes !== null && metrics.memory_limit_bytes > 0 ? (
        <Meter
          label={t('monitoring.memory')}
          value={metrics.memory_bytes}
          max={metrics.memory_limit_bytes}
          valueText={t('monitoring.host.memory_value', { used: formatGiB(metrics.memory_bytes, fmt.locale), total: formatGiB(metrics.memory_limit_bytes, fmt.locale) })}
        />
      ) : null}
      {spark.length >= 2 ? (
        <Sparkline points={spark} label={t('monitoring.sparkline', { current: spark[spark.length - 1][1], max: sparkMax })} />
      ) : null}
      {metrics.alerts.length > 0 ? (
        <div className="flex flex-wrap gap-1">
          {metrics.alerts.map((kind) => (
            <Badge key={kind} variant="destructive">
              {t(alertKey(kind))}
            </Badge>
          ))}
        </div>
      ) : null}
    </div>
  )
}
```

```tsx
// web/src/features/monitoring/host-card.tsx
import { Link } from '@tanstack/react-router'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import type { HostSnapshot } from '@/api/types'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'
import { formatCpu, formatGiB } from '@/features/servers/format'
import { useFormat } from '@/lib/format'
import { Meter } from './meter'
import { formatDecimal } from './model'

/** Admin-only host card on the dashboard (spec §7.1). */
export function HostCard({ host }: { host: HostSnapshot }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const missing = t('monitoring.missing')
  const low = host.alerts.includes('host.disk_low')
  const gib = (n: number) => formatGiB(n, fmt.locale)
  return (
    <section aria-labelledby={id} className="mb-6">
      <Card>
        <CardHeader>
          <h2 id={id} className="leading-none font-semibold">
            {t('monitoring.host.title')}
          </h2>
          <CardDescription>{t('monitoring.host.description')}</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-4">
          <div className="space-y-1">
            <p className="text-muted-foreground text-xs">{t('monitoring.host.cpu')}</p>
            <p className="tabular-nums">{host.cpu_percent === null ? missing : t('monitoring.percent', { value: formatCpu(host.cpu_percent, fmt.locale) })}</p>
          </div>
          <div className="space-y-1">
            <p className="text-muted-foreground text-xs">{t('monitoring.host.load')}</p>
            <p className="tabular-nums">{host.load1 === null ? missing : formatDecimal(host.load1, fmt.locale, 2)}</p>
          </div>
          <div className="space-y-1">
            <p className="text-muted-foreground text-xs">{t('monitoring.host.memory')}</p>
            {host.memory_used !== null && host.memory_total !== null ? (
              <>
                <p className="tabular-nums">{t('monitoring.host.memory_value', { used: gib(host.memory_used), total: gib(host.memory_total) })}</p>
                <Meter
                  label={t('monitoring.host.memory')}
                  value={host.memory_used}
                  max={host.memory_total}
                  valueText={t('monitoring.host.memory_value', { used: gib(host.memory_used), total: gib(host.memory_total) })}
                />
              </>
            ) : (
              <p>{missing}</p>
            )}
          </div>
          <div className="space-y-1">
            <p className="text-muted-foreground text-xs">{t('monitoring.host.disk')}</p>
            {host.disk_free !== null && host.disk_total !== null ? (
              <>
                <p className={low ? 'text-destructive tabular-nums' : 'tabular-nums'}>{t('monitoring.host.disk_value', { free: gib(host.disk_free), total: gib(host.disk_total) })}</p>
                <Meter
                  label={t('monitoring.host.disk')}
                  value={host.disk_total - host.disk_free}
                  max={host.disk_total}
                  danger={low}
                  valueText={t('monitoring.host.disk_value', { free: gib(host.disk_free), total: gib(host.disk_total) })}
                />
              </>
            ) : (
              <p>{missing}</p>
            )}
          </div>
          {low ? (
            <p role="status" className="text-destructive sm:col-span-2 lg:col-span-4">
              {t('monitoring.host.disk_low')}
            </p>
          ) : null}
          <Link to="/settings" className="text-primary underline underline-offset-4 sm:col-span-2 lg:col-span-4">
            {t('monitoring.host.settings_link')}
          </Link>
        </CardContent>
      </Card>
    </section>
  )
}
```

- [ ] **Step 8: Dashboard einbinden**

`web/src/features/dashboard/server-card.tsx`: Signatur und Inhalt erweitern.

```tsx
import type { OverviewServer, Server } from '@/api/types'
import { ServerMetrics } from '@/features/monitoring/server-metrics'
```

```tsx
export function ServerCard({ server, metrics }: { server: Server; metrics?: OverviewServer }) {
```

Direkt nach `{instance ? <AddressLine address={server.address} /> : null}`:

```tsx
          {instance ? <ServerMetrics server={server} metrics={metrics} /> : null}
```

`web/src/features/dashboard/dashboard-page.tsx`:

```tsx
import { HostCard } from '@/features/monitoring/host-card'
import { monitoringOverviewQueryOptions, OVERVIEW_POLL_MS } from '@/features/monitoring/api'
```

Im Körper nach `const settings = …`:

```tsx
  // Monitoring values are an extra: a failed overview leaves the cards as they are (spec §7.1).
  const monitoring = useQuery({ ...monitoringOverviewQueryOptions, refetchInterval: OVERVIEW_POLL_MS })
  const metricsById = new Map((monitoring.data?.servers ?? []).map((m) => [m.id, m]))
  const host = isAdmin ? (monitoring.data?.host ?? null) : null
```

In der Liste: `<ServerCard server={server} metrics={metricsById.get(server.id)} />`. Im `return` nach dem Hinweis zur Basisdomain und vor `{content}`: `{host ? <HostCard host={host} /> : null}`.

- [ ] **Step 9: Tests auf der VM (grün)**

Run: `make check && make test-vm SUITE=web`
Expected: PASS `model.test.ts`, `dashboard-page.test.tsx` (neue und alte Fälle), `i18n.test.ts`.

- [ ] **Step 10: Commit**

```bash
git add web/src/features/monitoring web/src/features/dashboard web/src/i18n
git commit -m "feat(web): monitoring values, sparkline and host card on the dashboard

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 13: Tab „Monitoring“ und Einstellungs-Karte

**Files:**
- Create: `web/src/features/monitoring/live-header.tsx`, `range-picker.tsx`, `metric-charts.tsx`, `down-alerts-switch.tsx`, `monitoring-tab.tsx`, `monitoring-tab.test.tsx`, `web/src/features/settings/monitoring-card.tsx`, `monitoring-card.test.tsx`
- Modify: `web/src/features/servers/server-detail-page.tsx`, `server-detail-page.test.tsx`, `web/src/features/settings/settings-page.tsx`, `web/src/i18n/de.ts`, `en.ts`, `i18n.test.ts`

**Interfaces:**
- Consumes: Aufgaben 11–12; `serverKey` (`features/servers/api`), `notifiersKey` (`features/security/security-key`), `SettingsCard`, `FieldRow`, `Switch`, `Label`, `Input`, `useErrorToast`, `errorMessage`, `isApiError`.
- Produces: `MonitoringTab({ server })`, `MonitoringCard()`; Tab-Wert `'monitoring'` in `DetailTab`.

- [ ] **Step 1: Texte ergänzen**

In `de.ts` im Block `monitoring` (nach `host: { … },`) ergänzen:

```ts
    live: {
      title: 'Jetzt',
      unreachable: 'Der Server antwortet nicht auf Anfragen (startet er noch?).',
      agent_unavailable: 'Der Agent ist gerade nicht erreichbar; es gibt keine aktuellen Werte.',
      no_players: 'Niemand online.',
      player_names: 'Online-Spieler',
    },
    tps_source: {
      tick_query: 'Quelle: tick query',
      paper: 'Quelle: Paper',
      none: 'TPS für diese Version nicht verfügbar',
      unknown: 'TPS-Quelle wird noch ermittelt …',
    },
    range: {
      label: 'Zeitraum',
      '1h': '1 Stunde',
      '24h': '24 Stunden',
      '7d': '7 Tage',
      '30d': '30 Tage',
    },
    charts: {
      players: 'Spieler',
      players_max: 'Plätze',
      tps_mspt: 'TPS und MSPT',
      cpu: 'CPU-Auslastung',
      memory: 'Arbeitsspeicher',
      memory_limit: 'Limit',
    },
    down_alerts: {
      label: 'Down-Alerts für diesen Server',
      hint: 'Meldet über die Benachrichtigungen, wenn der Server laufen soll, aber nicht erreichbar ist oder wiederholt abstürzt.',
      enabled: 'Down-Alerts eingeschaltet.',
      disabled: 'Down-Alerts ausgeschaltet.',
    },
    settings: {
      title: 'Monitoring & Alerts',
      description: 'Schwellen für Meldungen bei Ausfällen und knappem Plattenplatz.',
      disk_min_percent: 'Platte knapp unter (%)',
      disk_min_gib: 'oder unter (GiB)',
      disk_hint: 'Gemeldet wird, sobald eine der beiden Schwellen unterschritten ist.',
      down_after_min: 'Server down nach (Minuten)',
      down_hint: 'Nach einem Start gelten 5 Minuten Schonfrist.',
      crash_restarts: 'Crash-Loop ab Neustarts',
      crash_window_min: 'innerhalb von (Minuten)',
      save: 'Einstellungen speichern',
      saved: 'Monitoring-Einstellungen gespeichert.',
      firing: 'Aktive Alerts',
      no_alerts: 'Keine aktiven Alerts.',
      since: 'seit {{time}}',
      channels_hint: 'Meldungen gehen an die eingerichteten Benachrichtigungen (Sicherheit → Benachrichtigungen).',
      notifiers_link: 'Benachrichtigungen öffnen',
      no_notifiers: 'Es ist keine aktive Benachrichtigung eingerichtet – Alerts werden erkannt, aber nicht verschickt.',
      validation: {
        integer: 'Bitte eine ganze Zahl eingeben.',
        decimal: 'Bitte eine Zahl mit höchstens zwei Nachkommastellen eingeben.',
        range: 'Erlaubt sind {{min}} bis {{max}}.',
      },
    },
```

Außerdem `servers.detail.tabs.monitoring: 'Monitoring'` (im vorhandenen Block `servers.detail.tabs`) und im Block `settings` ein neues `monitoring_section: { title: 'Monitoring' },`.

In `en.ts` dieselben Schlüssel:

```ts
    live: {
      title: 'Now',
      unreachable: 'The server does not answer (is it still starting?).',
      agent_unavailable: 'The agent is unreachable right now; there are no current values.',
      no_players: 'Nobody online.',
      player_names: 'Online players',
    },
    tps_source: {
      tick_query: 'Source: tick query',
      paper: 'Source: Paper',
      none: 'TPS not available for this version',
      unknown: 'Detecting the TPS source …',
    },
    range: {
      label: 'Period',
      '1h': '1 hour',
      '24h': '24 hours',
      '7d': '7 days',
      '30d': '30 days',
    },
    charts: {
      players: 'Players',
      players_max: 'Slots',
      tps_mspt: 'TPS and MSPT',
      cpu: 'CPU usage',
      memory: 'Memory',
      memory_limit: 'Limit',
    },
    down_alerts: {
      label: 'Down alerts for this server',
      hint: 'Notifies through the configured channels when the server should run but is unreachable or keeps crashing.',
      enabled: 'Down alerts turned on.',
      disabled: 'Down alerts turned off.',
    },
    settings: {
      title: 'Monitoring & alerts',
      description: 'Thresholds for outage and low disk notifications.',
      disk_min_percent: 'Disk low below (%)',
      disk_min_gib: 'or below (GiB)',
      disk_hint: 'An alert fires as soon as either threshold is crossed.',
      down_after_min: 'Server down after (minutes)',
      down_hint: 'After a start there is a 5-minute grace period.',
      crash_restarts: 'Crash loop from restarts',
      crash_window_min: 'within (minutes)',
      save: 'Save settings',
      saved: 'Monitoring settings saved.',
      firing: 'Active alerts',
      no_alerts: 'No active alerts.',
      since: 'since {{time}}',
      channels_hint: 'Notifications go to the configured channels (Security → Notifications).',
      notifiers_link: 'Open notifications',
      no_notifiers: 'No active notification channel is set up – alerts are detected but not sent.',
      validation: {
        integer: 'Enter a whole number.',
        decimal: 'Enter a number with at most two decimals.',
        range: 'Allowed are {{min}} to {{max}}.',
      },
    },
```

plus `servers.detail.tabs.monitoring: 'Monitoring'` und `settings.monitoring_section: { title: 'Monitoring' }`.

`DYNAMIC_KEYS` in `i18n.test.ts`:

```ts
  ...['tick_query', 'paper', 'none', 'unknown'].map((s) => `monitoring.tps_source.${s}`),
  ...['1h', '24h', '7d', '30d'].map((r) => `monitoring.range.${r}`),
  ...['disk_min_percent', 'disk_min_gib', 'down_after_min', 'crash_restarts', 'crash_window_min'].map((f) => `monitoring.settings.${f}`),
```

- [ ] **Step 2: Tab-Tests schreiben (rot)**

`web/src/features/monitoring/monitoring-tab.test.tsx`:

```tsx
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ServerDetailPage } from '@/features/servers/server-detail-page'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { LIVE_POLL_MS } from './api'
import { liveMetrics, mockLive, mockMetrics } from './test-utils'

async function openMonitoring(serverId = 1, username = 'admin') {
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  const rendered = await renderWithProviders(<ServerDetailPage serverId={serverId} />, { me: loginAs(username, { sudo: true }) })
  await user.click(await screen.findByRole('tab', { name: 'Monitoring' }))
  return { user, ...rendered }
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
})
afterEach(() => {
  vi.useRealTimers()
})

describe('MonitoringTab', () => {
  it('shows the live values with player names and the TPS source', async () => {
    mockLive(liveMetrics())
    mockMetrics()
    await openMonitoring()
    const now = await screen.findByRole('region', { name: 'Jetzt' })
    expect(await within(now).findByText('2/20')).toBeInTheDocument()
    expect(within(now).getByText('19,9')).toBeInTheDocument()
    expect(within(now).getByText('12,3 ms')).toBeInTheDocument()
    expect(within(now).getByText('31,5 %')).toBeInTheDocument()
    expect(within(now).getByText('3 / 5 GiB')).toBeInTheDocument()
    expect(within(now).getByRole('list', { name: 'Online-Spieler' })).toHaveTextContent('AlexSteve')
    expect(within(now).getByText('Quelle: tick query')).toBeInTheDocument()
  })

  it('says when TPS is not available and when nobody is online', async () => {
    mockLive(liveMetrics({ tps: null, mspt: null, tps_source: 'none', players: 0, player_names: [] }))
    mockMetrics()
    await openMonitoring()
    const now = await screen.findByRole('region', { name: 'Jetzt' })
    expect(await within(now).findByText('TPS für diese Version nicht verfügbar')).toBeInTheDocument()
    expect(within(now).getByText('Niemand online.')).toBeInTheDocument()
  })

  it('reports an unreachable server and a missing agent', async () => {
    mockLive(liveMetrics({ up: false, players: null, tps: null }))
    mockMetrics()
    await openMonitoring()
    expect(await screen.findByText('Der Server antwortet nicht auf Anfragen (startet er noch?).')).toBeInTheDocument()

    server.use(http.get('*/api/servers/:id/metrics/live', () => HttpResponse.json({ error: 'agent_unavailable', message: 'no agent' }, { status: 503 })))
    await act(() => vi.advanceTimersByTimeAsync(LIVE_POLL_MS + 100))
    expect(await screen.findByText('Der Agent ist gerade nicht erreichbar; es gibt keine aktuellen Werte.')).toBeInTheDocument()
  })

  it('polls the live values every 5 s only while the tab is open', async () => {
    const live = mockLive((n) => liveMetrics({ players: n }))
    mockMetrics()
    const { user } = await openMonitoring()
    await screen.findByText('1/20')
    await act(() => vi.advanceTimersByTimeAsync(LIVE_POLL_MS + 100))
    expect(await screen.findByText('2/20')).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: 'Übersicht' }))
    const calls = live.calls
    await act(() => vi.advanceTimersByTimeAsync(LIVE_POLL_MS * 3))
    expect(live.calls).toBe(calls)
  })

  it('draws four charts and switches the period', async () => {
    mockLive(liveMetrics())
    const ranges = mockMetrics()
    const { user } = await openMonitoring()
    for (const name of ['Spieler', 'TPS und MSPT', 'CPU-Auslastung', 'Arbeitsspeicher']) {
      expect(await screen.findByRole('img', { name })).toBeInTheDocument()
    }
    expect(screen.getByRole('button', { name: '1 Stunde' })).toHaveAttribute('aria-pressed', 'true')
    await user.click(screen.getByRole('button', { name: '7 Tage' }))
    expect(screen.getByRole('button', { name: '7 Tage' })).toHaveAttribute('aria-pressed', 'true')
    await waitFor(() => expect(ranges).toContain('7d'))
    expect(ranges.at(-1)).toBe('7d')
  })

  it('lets admins switch the down alerts of the server', async () => {
    mockLive(liveMetrics())
    mockMetrics()
    let body: unknown = null
    server.use(
      http.put('*/api/servers/:id/alerts', async ({ request }) => {
        body = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = await openMonitoring()
    const toggle = await screen.findByRole('switch', { name: 'Down-Alerts für diesen Server' })
    expect(toggle).toBeChecked()
    await user.click(toggle)
    expect(await screen.findByText('Down-Alerts ausgeschaltet.')).toBeInTheDocument()
    expect(body).toEqual({ down_alerts: false })
  })

  it('shows other users the tab without the alert switch', async () => {
    mockLive(liveMetrics())
    mockMetrics()
    await openMonitoring(1, 'alice')
    await screen.findByRole('region', { name: 'Jetzt' })
    expect(screen.queryByRole('switch', { name: 'Down-Alerts für diesen Server' })).not.toBeInTheDocument()
  })
})
```

Detailseiten-Tests anpassen (`web/src/features/servers/server-detail-page.test.tsx`): In der Tab-Liste nach `'Übersicht'` den Eintrag `'Monitoring'` einfügen, `toHaveLength(8)` → `toHaveLength(9)`, im Fall „shows a restricted user only the overview“ die Erwartung auf `['Übersicht', 'Monitoring']` ändern (und den Testnamen zu „shows a restricted user only the overview and monitoring“).

- [ ] **Step 3: Einstellungs-Tests schreiben (rot)**

`web/src/features/settings/monitoring-card.test.tsx`:

```tsx
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { GIB } from '@/features/monitoring/model'
import { mockMonitoringSettings, monitoringSettings } from '@/features/monitoring/test-utils'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { SettingsPage } from './settings-page'

async function openCard() {
  const user = userEvent.setup()
  await renderWithProviders(<SettingsPage />, { me: loginAs('admin', { sudo: true }) })
  const card = await screen.findByRole('region', { name: 'Monitoring & Alerts' })
  return { user, card }
}

describe('MonitoringCard', () => {
  it('shows the thresholds and the firing alerts', async () => {
    mockMonitoringSettings(
      monitoringSettings({ alerts: [{ kind: 'server.down', server_id: 4, server_name: 'modded', since: Date.now() - 5 * 60_000, detail: 'Container läuft nicht' }] }),
    )
    const { card } = await openCard()
    expect(await within(card).findByLabelText('Server down nach (Minuten)')).toHaveValue('3')
    expect(within(card).getByLabelText('oder unter (GiB)')).toHaveValue('10')
    const firing = within(card).getByRole('list', { name: 'Aktive Alerts' })
    expect(firing).toHaveTextContent('Server down – modded')
    expect(firing).toHaveTextContent('Container läuft nicht')
  })

  it('saves changed thresholds in bytes', async () => {
    mockMonitoringSettings(monitoringSettings())
    let body: unknown = null
    server.use(
      http.put('*/api/monitoring/settings', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(monitoringSettings({ down_after_min: 5, disk_min_bytes: 20 * GIB }))
      }),
    )
    const { user, card } = await openCard()
    const save = await within(card).findByRole('button', { name: 'Einstellungen speichern' })
    expect(save).toBeDisabled()
    await user.clear(within(card).getByLabelText('Server down nach (Minuten)'))
    await user.type(within(card).getByLabelText('Server down nach (Minuten)'), '5')
    await user.clear(within(card).getByLabelText('oder unter (GiB)'))
    await user.type(within(card).getByLabelText('oder unter (GiB)'), '20')
    await user.click(save)
    expect(await screen.findByText('Monitoring-Einstellungen gespeichert.')).toBeInTheDocument()
    expect(body).toEqual({ disk_min_percent: 10, disk_min_bytes: 20 * GIB, down_after_min: 5, crash_restarts: 3, crash_window_min: 10 })
  })

  it('refuses values outside the limits without a request', async () => {
    mockMonitoringSettings(monitoringSettings())
    let calls = 0
    server.use(
      http.put('*/api/monitoring/settings', () => {
        calls += 1
        return HttpResponse.json(monitoringSettings())
      }),
    )
    const { user, card } = await openCard()
    const field = await within(card).findByLabelText('Crash-Loop ab Neustarts')
    await user.clear(field)
    await user.type(field, '1')
    expect(within(card).getByText('Erlaubt sind 2 bis 20.')).toBeInTheDocument()
    expect(field).toHaveAttribute('aria-invalid', 'true')
    expect(within(card).getByRole('button', { name: 'Einstellungen speichern' })).toBeDisabled()
    expect(calls).toBe(0)
  })

  it('warns when no notification channel is active', async () => {
    mockMonitoringSettings(monitoringSettings())
    server.use(http.get('*/api/security/notifiers', () => HttpResponse.json([])))
    const { card } = await openCard()
    expect(await within(card).findByText('Es ist keine aktive Benachrichtigung eingerichtet – Alerts werden erkannt, aber nicht verschickt.')).toBeInTheDocument()
    expect(within(card).getByRole('link', { name: 'Benachrichtigungen öffnen' })).toHaveAttribute('href', '/security')
  })
})
```

- [ ] **Step 4: `make check` (rot)**

Run: `git add -N web/src/features && make check`
Expected: FAIL (fehlende Module `monitoring-tab`, `monitoring-card`).

- [ ] **Step 5: Live-Kopf, Bereichswahl, Diagramme, Schalter, Tab implementieren**

```tsx
// web/src/features/monitoring/live-header.tsx
import { useQuery } from '@tanstack/react-query'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { isApiError } from '@/api/errors'
import { formatCpu, formatGiB } from '@/features/servers/format'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { LIVE_POLL_MS, liveMetricsQueryOptions } from './api'
import { formatDecimal, tpsSourceKey } from './model'

/** Polled every 5 s while mounted; Radix unmounts the inactive tab, which ends the polling (spec §6.2). */
export function LiveHeader({ serverId }: { serverId: number }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const live = useQuery({ ...liveMetricsQueryOptions(serverId), refetchInterval: LIVE_POLL_MS })
  const missing = t('monitoring.missing')

  let body
  if (live.isPending) body = <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
  else if (live.isError)
    body = (
      <p role="alert" className="text-destructive text-sm">
        {isApiError(live.error, 'agent_unavailable') ? t('monitoring.live.agent_unavailable') : errorMessage(live.error, t)}
      </p>
    )
  else {
    const m = live.data
    const tps = m.tps_source === 'none' ? t('monitoring.tps_source.none') : m.tps === null ? missing : formatDecimal(m.tps, fmt.locale)
    const items: [string, string][] = [
      [t('monitoring.players'), m.players === null ? missing : t('monitoring.players_value', { players: m.players, max: m.players_max ?? missing })],
      [t('monitoring.tps'), tps],
      [t('monitoring.mspt'), m.mspt === null ? missing : t('monitoring.ms', { value: formatDecimal(m.mspt, fmt.locale) })],
      [t('monitoring.cpu'), m.cpu_percent === null ? missing : t('monitoring.percent', { value: formatCpu(m.cpu_percent, fmt.locale) })],
      [
        t('monitoring.memory'),
        m.memory_bytes === null || m.memory_limit_bytes === null
          ? missing
          : t('monitoring.host.memory_value', { used: formatGiB(m.memory_bytes, fmt.locale), total: formatGiB(m.memory_limit_bytes, fmt.locale) }),
      ],
    ]
    body = (
      <div className="space-y-3">
        {m.up === false ? (
          <p role="status" className="text-destructive text-sm">
            {t('monitoring.live.unreachable')}
          </p>
        ) : null}
        <dl className="grid grid-cols-2 gap-3 text-sm tabular-nums sm:grid-cols-5">
          {items.map(([label, value]) => (
            <div key={label}>
              <dt className="text-muted-foreground text-xs">{label}</dt>
              <dd className="text-base font-medium">{value}</dd>
            </div>
          ))}
        </dl>
        {m.tps_source !== 'none' ? <p className="text-muted-foreground text-xs">{t(tpsSourceKey(m.tps_source))}</p> : null}
        {m.player_names.length > 0 ? (
          <ul aria-label={t('monitoring.live.player_names')} className="flex flex-wrap gap-2 text-sm">
            {m.player_names.map((name) => (
              <li key={name} className="bg-muted rounded px-2 py-0.5">
                {name}
              </li>
            ))}
          </ul>
        ) : m.players === 0 ? (
          <p className="text-muted-foreground text-sm">{t('monitoring.live.no_players')}</p>
        ) : null}
      </div>
    )
  }

  return (
    <section aria-labelledby={id} className="space-y-3 rounded-md border p-4">
      <h3 id={id} className="font-semibold">
        {t('monitoring.live.title')}
      </h3>
      {body}
    </section>
  )
}
```

```tsx
// web/src/features/monitoring/range-picker.tsx
import { useTranslation } from 'react-i18next'
import type { MetricsRange } from '@/api/types'
import { Button } from '@/components/ui/button'
import { RANGES } from './model'

export function RangePicker({ value, onChange }: { value: MetricsRange; onChange(range: MetricsRange): void }) {
  const { t } = useTranslation()
  return (
    <div role="group" aria-label={t('monitoring.range.label')} className="flex flex-wrap gap-2">
      {RANGES.map((r) => (
        <Button key={r} type="button" size="sm" variant={r === value ? 'default' : 'outline'} aria-pressed={r === value} onClick={() => onChange(r)}>
          {t(`monitoring.range.${r}`)}
        </Button>
      ))}
    </div>
  )
}
```

```tsx
// web/src/features/monitoring/metric-charts.tsx
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import type { MetricsRange } from '@/api/types'
import { LineChart, type ChartAxis } from '@/components/charts/line-chart'
import { formatCpu, formatGiB } from '@/features/servers/format'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { metricsPollMs, serverMetricsQueryOptions } from './api'
import { formatDecimal, lastValue, metricPoints, RANGE_MS } from './model'

/** The four charts of the tab (spec §7.2); 24 h and more show the average with a min–max band. */
export function MetricCharts({ serverId, range }: { serverId: number; range: MetricsRange }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const query = useQuery({ ...serverMetricsQueryOptions(serverId, range), refetchInterval: metricsPollMs(range), placeholderData: keepPreviousData })
  if (query.isPending) return <p className="text-muted-foreground">{t('common.loading')}</p>
  if (query.isError)
    return (
      <p role="alert" className="text-destructive">
        {errorMessage(query.error, t)}
      </p>
    )
  const data = query.data
  const to = query.dataUpdatedAt || Date.now()
  const from = to - RANGE_MS[range]
  const band = range !== '1h'
  const long = range === '7d' || range === '30d'
  const common = { from, to, stepMs: data.step_ms, band, formatTime: long ? fmt.dateTime : fmt.time, formatTick: long ? fmt.date : fmt.time }
  const memLimit = lastValue(metricPoints(data, 'memory_limit_bytes'))

  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <LineChart
        {...common}
        title={t('monitoring.charts.players')}
        series={[
          { id: 'players', label: t('monitoring.charts.players'), tone: 'primary', points: metricPoints(data, 'players') },
          { id: 'players_max', label: t('monitoring.charts.players_max'), tone: 'muted', points: metricPoints(data, 'players_max') },
        ]}
        formatValue={(v) => fmt.number(Math.round(v))}
      />
      <LineChart
        {...common}
        title={t('monitoring.charts.tps_mspt')}
        series={[
          { id: 'tps', label: t('monitoring.tps'), tone: 'primary', points: metricPoints(data, 'tps') },
          { id: 'mspt', label: t('monitoring.mspt'), tone: 'secondary', axis: 'right', points: metricPoints(data, 'mspt') },
        ]}
        formatValue={(v: number, axis: ChartAxis) => (axis === 'right' ? t('monitoring.ms', { value: formatDecimal(v, fmt.locale) }) : formatDecimal(v, fmt.locale))}
      />
      <LineChart
        {...common}
        title={t('monitoring.charts.cpu')}
        series={[{ id: 'cpu', label: t('monitoring.cpu'), tone: 'primary', points: metricPoints(data, 'cpu_percent') }]}
        formatValue={(v) => t('monitoring.percent', { value: formatCpu(v, fmt.locale) })}
      />
      <LineChart
        {...common}
        title={t('monitoring.charts.memory')}
        series={[{ id: 'memory', label: t('monitoring.memory'), tone: 'primary', points: metricPoints(data, 'memory_bytes') }]}
        limit={memLimit === null ? undefined : { value: memLimit, label: t('monitoring.charts.memory_limit') }}
        formatValue={(v) => t('monitoring.gib', { value: formatGiB(v, fmt.locale) })}
      />
    </div>
  )
}
```

```tsx
// web/src/features/monitoring/down-alerts-switch.tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Server } from '@/api/types'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { serverKey } from '@/features/servers/api'
import { useErrorToast } from '@/lib/error-message'
import { updateServerAlerts } from './api'

/** Admin switch (spec §7.2); a missing value means on (V2: no row = true). */
export function DownAlertsSwitch({ server }: { server: Server }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const checked = server.down_alerts ?? true
  const save = useMutation({
    mutationFn: (value: boolean) => updateServerAlerts(server.id, value),
    onSuccess: (_data, value) => {
      queryClient.setQueryData<Server>(serverKey(server.id), (prev) => (prev ? { ...prev, down_alerts: value } : prev))
      toast.success(t(value ? 'monitoring.down_alerts.enabled' : 'monitoring.down_alerts.disabled'))
    },
    onError: notifyError,
  })
  return (
    <div className="flex items-start gap-3 rounded-md border p-4">
      <Switch id={id} checked={checked} disabled={save.isPending} aria-describedby={`${id}-hint`} onCheckedChange={(value) => save.mutate(value)} />
      <div className="space-y-1">
        <Label htmlFor={id}>{t('monitoring.down_alerts.label')}</Label>
        <p id={`${id}-hint`} className="text-muted-foreground text-sm">
          {t('monitoring.down_alerts.hint')}
        </p>
      </div>
    </div>
  )
}
```

```tsx
// web/src/features/monitoring/monitoring-tab.tsx
import { useState } from 'react'
import type { MetricsRange, Server } from '@/api/types'
import { useMe } from '@/features/auth/me'
import { DownAlertsSwitch } from './down-alerts-switch'
import { LiveHeader } from './live-header'
import { MetricCharts } from './metric-charts'
import { RangePicker } from './range-picker'

export function MonitoringTab({ server }: { server: Server }) {
  const me = useMe()
  const [range, setRange] = useState<MetricsRange>('1h')
  return (
    <div className="space-y-6">
      <LiveHeader serverId={server.id} />
      <RangePicker value={range} onChange={setRange} />
      <MetricCharts serverId={server.id} range={range} />
      {me?.role === 'admin' ? <DownAlertsSwitch server={server} /> : null}
    </div>
  )
}
```

- [ ] **Step 6: Tab in die Detailseite einbinden**

`web/src/features/servers/server-detail-page.tsx`:

- Import `import { MonitoringTab } from '@/features/monitoring/monitoring-tab'`.
- `type DetailTab = 'overview' | 'monitoring' | 'console' | …`.
- `const available: DetailTab[] = ['overview', 'monitoring']` (jeder mit `server.view` und Instanz sieht den Tab, Spec §7.2).
- Trigger direkt nach „Übersicht“: `<TabsTrigger value="monitoring">{t('servers.detail.tabs.monitoring')}</TabsTrigger>`.
- Inhalt nach dem Übersichts-Inhalt: `<TabsContent value="monitoring" className="pt-4"><MonitoringTab server={server} /></TabsContent>`.

Den Kommentar „Up to eight tabs …“ auf „Up to nine tabs …“ ändern.

- [ ] **Step 7: Einstellungs-Karte implementieren und einbinden**

```tsx
// web/src/features/settings/monitoring-card.tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { MonitoringSettings } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { monitoringSettingsKey, monitoringSettingsQueryOptions, updateMonitoringSettings } from '@/features/monitoring/api'
import { alertKey, draftFromSettings, sameSettings, SETTINGS_FIELDS, validateSettings, type SettingsDraft, type SettingsField } from '@/features/monitoring/model'
import { notifiersKey } from '@/features/security/security-key'
import { FieldRow } from '@/features/servers/field-row'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { SettingsCard } from './settings-card'

const HINTS: Partial<Record<SettingsField, string>> = { disk_min_gib: 'monitoring.settings.disk_hint', down_after_min: 'monitoring.settings.down_hint' }

export function MonitoringCard() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const firingId = useId()
  const settings = useQuery(monitoringSettingsQueryOptions)
  const notifiers = useQuery({ queryKey: notifiersKey, queryFn: () => unwrap(api.GET('/security/notifiers')) })
  const noChannel = notifiers.data !== undefined && !notifiers.data.some((n) => n.enabled)

  let body
  if (settings.isPending) body = <p className="text-muted-foreground">{t('common.loading')}</p>
  else if (settings.isError)
    body = (
      <p role="alert" className="text-destructive">
        {errorMessage(settings.error, t)}
      </p>
    )
  else
    body = (
      <>
        {/* Keyed by the stored values: after a save the form starts from the new state. */}
        <MonitoringForm key={JSON.stringify(draftFromSettings(settings.data))} settings={settings.data} />
        <div className="space-y-2">
          <h4 id={firingId} className="font-medium">
            {t('monitoring.settings.firing')}
          </h4>
          {settings.data.alerts.length === 0 ? (
            <p className="text-muted-foreground">{t('monitoring.settings.no_alerts')}</p>
          ) : (
            <ul aria-labelledby={firingId} className="space-y-1">
              {settings.data.alerts.map((a) => (
                <li key={`${a.kind}:${a.server_id ?? 'host'}`} className="text-destructive">
                  {t(alertKey(a.kind))}
                  {a.server_name ? ` – ${a.server_name}` : ''}, {t('monitoring.settings.since', { time: fmt.relative(a.since) })}
                  {a.detail ? <span className="text-muted-foreground block">{a.detail}</span> : null}
                </li>
              ))}
            </ul>
          )}
        </div>
      </>
    )

  return (
    <SettingsCard title={t('monitoring.settings.title')} description={t('monitoring.settings.description')}>
      {body}
      <p className="text-muted-foreground">{t('monitoring.settings.channels_hint')}</p>
      {noChannel ? (
        <p role="status" className="text-destructive">
          {t('monitoring.settings.no_notifiers')}
        </p>
      ) : null}
      <Link to="/security" className="text-primary underline underline-offset-4">
        {t('monitoring.settings.notifiers_link')}
      </Link>
    </SettingsCard>
  )
}

function MonitoringForm({ settings }: { settings: MonitoringSettings }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const initial = draftFromSettings(settings)
  const [draft, setDraft] = useState<SettingsDraft>(initial)
  const v = validateSettings(draft)
  const changed = v.request !== null && !sameSettings(v.request, settings)
  const save = useMutation({
    mutationFn: updateMonitoringSettings,
    onSuccess: (result) => {
      queryClient.setQueryData(monitoringSettingsKey, result)
      toast.success(t('monitoring.settings.saved'))
    },
    onError: notifyError,
  })

  return (
    <form
      className="grid gap-4 sm:grid-cols-2"
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        if (v.request && changed && !save.isPending) save.mutate(v.request)
      }}
    >
      {SETTINGS_FIELDS.map((field) => {
        const hint = HINTS[field]
        // Errors show for edited fields only; untouched fields hold the stored, valid value.
        const error = draft[field] !== initial[field] ? v.errors[field] : undefined
        return (
          <FieldRow key={field} id={`${id}-${field}`} label={t(`monitoring.settings.${field}`)} hint={hint ? t(hint) : undefined} error={error}>
            {(c) => <Input {...c} inputMode={field === 'disk_min_gib' ? 'decimal' : 'numeric'} value={draft[field]} onChange={(e) => setDraft({ ...draft, [field]: e.target.value })} />}
          </FieldRow>
        )
      })}
      <div className="sm:col-span-2">
        <Button type="submit" disabled={!changed || save.isPending}>
          {t('monitoring.settings.save')}
        </Button>
      </div>
    </form>
  )
}
```

`web/src/features/settings/settings-page.tsx`: Import `MonitoringCard`, eine zweite `useId` (`monitoringHeadingId`) und nach der vorhandenen `<section>` (im Fragment) anfügen:

```tsx
      <section aria-labelledby={monitoringHeadingId} className="mt-8 space-y-4">
        <h2 id={monitoringHeadingId} className="text-lg font-semibold">
          {t('settings.monitoring_section.title')}
        </h2>
        <div className="grid gap-4 lg:grid-cols-2">
          <MonitoringCard />
        </div>
      </section>
```

Die Karte lädt unabhängig von den Server-Einstellungen; ein Fehler dort blendet sie nicht aus.

- [ ] **Step 8: Tests auf der VM (grün)**

Run: `make check && make test-vm SUITE=web`
Expected: PASS `monitoring-tab.test.tsx`, `monitoring-card.test.tsx`, `server-detail-page.test.tsx` (angepasste Tab-Listen), `settings-page.test.tsx` (unverändert grün – falls ein Test dort alle `h2` zählt, um den neuen Abschnitt ergänzen), `i18n.test.ts`.

- [ ] **Step 9: Commit**

```bash
git add web/src/features web/src/i18n
git commit -m "feat(web): monitoring tab with live values and charts, alert settings card

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 14: Playwright gegen den Mock

**Files:**
- Create: `web/e2e/monitoring.spec.ts`
- Modify: `web/playwright.config.ts` (`testMatch`), ggf. vorhandene e2e-Tests, die die Tab-Leiste der Detailseite zählen (`grep -rn "getByRole('tab')" web/e2e`)

**Interfaces:**
- Consumes: `signIn`, `confirmSudo` aus `web/e2e/helpers.ts`; Seed-Daten des Mocks (V6): `modded` hat Alert `server.down`, `survival` liefert Live-Werte mit Namen, Host-Werte für Admins.

- [ ] **Step 1: Test schreiben**

```ts
// web/e2e/monitoring.spec.ts
import { expect, test } from '@playwright/test'
import { confirmSudo, signIn } from './helpers'

test('admin sees host and server values on the dashboard and explores the monitoring tab', async ({ page }) => {
  await signIn(page, 'admin', true)
  const host = page.getByRole('region', { name: 'Host' })
  await expect(host.getByText(/GiB frei von/)).toBeVisible()
  await expect(page.getByRole('article', { name: 'modded' }).getByText('Server down')).toBeVisible()

  const survival = page.getByRole('article', { name: 'survival' })
  await expect(survival.getByTestId('server-metrics')).toBeVisible()
  await survival.getByRole('link', { name: 'survival', exact: true }).click()
  await page.getByRole('tab', { name: 'Monitoring', exact: true }).click()

  const now = page.getByRole('region', { name: 'Jetzt' })
  await expect(now.getByRole('list', { name: 'Online-Spieler' })).toBeVisible()
  await expect(page.getByRole('img', { name: 'Spieler' })).toBeVisible()
  await expect(page.getByRole('img', { name: 'TPS und MSPT' })).toBeVisible()

  await page.getByRole('button', { name: '7 Tage' }).click()
  await expect(page.getByRole('button', { name: '7 Tage' })).toHaveAttribute('aria-pressed', 'true')
  await page.getByRole('button', { name: 'Als Tabelle anzeigen' }).first().click()
  await expect(page.getByRole('table', { name: 'Spieler' })).toBeVisible()

  // Keyboard readout on the players chart.
  await page.getByRole('img', { name: 'Spieler' }).focus()
  await page.keyboard.press('End')
  await expect(page.getByRole('status').filter({ hasText: 'Spieler:' }).first()).toBeVisible()

  await expect(page.getByRole('switch', { name: 'Down-Alerts für diesen Server' })).toBeChecked()
})

test('admin changes an alert threshold with sudo', async ({ page }) => {
  await signIn(page, 'admin', true)
  await page.getByRole('link', { name: 'Einstellungen' }).click()
  const card = page.getByRole('region', { name: 'Monitoring & Alerts' })
  const field = card.getByLabel('Server down nach (Minuten)')
  await field.fill('5')
  await card.getByRole('button', { name: 'Einstellungen speichern' }).click()
  await confirmSudo(page)
  await expect(page.getByText('Monitoring-Einstellungen gespeichert.')).toBeVisible()
  await expect(field).toHaveValue('5')
})

test('a user without the admin role sees server values but no host card', async ({ page }) => {
  await signIn(page, 'alice', false)
  await expect(page.getByRole('article', { name: 'survival' }).getByTestId('server-metrics')).toBeVisible()
  await expect(page.getByRole('region', { name: 'Host' })).toHaveCount(0)
})
```

- [ ] **Step 2: `testMatch` erweitern**

In `web/playwright.config.ts` im Projekt `mock`: `testMatch: /(?<![\w-])(totp|smoke|servers|plugins|backups|monitoring)\.spec\.ts$/`.

Prüfen, ob ein vorhandener e2e-Test die Tabs der Detailseite zählt oder per Index anklickt (`grep -rn "getAllByRole('tab')\|getByRole('tab').nth" web/e2e`); falls ja, die erwartete Liste um „Monitoring“ (an zweiter Stelle) ergänzen.

- [ ] **Step 3: Playwright auf der VM**

Run: `make test-vm SUITE=e2e` (detached starten, Log abfragen)
Expected: PASS `monitoring.spec.ts` (3 Tests) und alle bisherigen Mock-Tests.

- [ ] **Step 4: Gesamtlauf und Commit**

Run: `make check && make test-vm` (alle Suites)
Expected: grün.

```bash
git add web/e2e/monitoring.spec.ts web/playwright.config.ts
git commit -m "test(web): end-to-end monitoring against the mock

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

Danach Push, Draft-PR „feat(web): monitoring dashboard, tab and alert settings (sub-project 5)“ (Body: Zusammenfassung, Abweichungen vom Plan, Testergebnisse; Ende `🤖 Generated with [Claude Code](https://claude.com/claude-code)` + Leerzeile + `https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R`), `gh pr checks <n> --watch` bis `CI OK`, Meldung an den Lead.

---

## Self-Review

- **Spec-Abdeckung §7:** Dashboard-Werte, Sparkline, Statuspunkt (Erreichbarkeit; „gestoppt“ zeigt das vorhandene Status-Badge), Alert-Badge, Host-Karte mit Warnfarbe und Link (Aufgabe 12); Tab mit Live-Kopf (5 s, nur bei offenem Tab), „TPS nicht verfügbar“, Bereichswahl, vier Diagramme mit Band ab 24 h und RAM-Limitlinie, Admin-Schalter (Aufgabe 13); Einstellungs-Karte mit fünf Werten, Validierung, feuernden Alerts, Hinweis/Link zu den Benachrichtigungen und Warnung ohne aktiven Kanal (Aufgabe 13); Diagramme mit Achsen, Gitter, Lücken, Tooltip per Maus/Tastatur, Theme-Farben, `role="img"` + Zusammenfassung, Tabelle, Ausdünnen (Aufgabe 11); i18n, Mock-Nutzung, Playwright (Aufgaben 11–14).
- **Bewusst nicht im UI:** `GET /monitoring/host` (Host-Verlauf) – Spec §7 sieht nur aktuelle Host-Werte vor.
- **Typen:** Alle Typnamen aus V3 (`MetricsRange`, `MetricPoint`, `MetricsSeries`, `LiveMetrics`, `OverviewServer`, `HostSnapshot`, `MonitoringOverview`, `FiringAlert`, `MonitoringSettings`, `UpdateMonitoringSettingsRequest`, `TpsSource`, `AlertKind`); `Server.down_alerts` (siehe Hinweis an den Lead).
