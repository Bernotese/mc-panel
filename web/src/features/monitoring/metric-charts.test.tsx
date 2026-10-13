import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { MetricsSeries } from '@/api/types'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { MetricCharts } from './metric-charts'
import { GIB } from './model'

const STEP = 30_000

function mockSeries(series: Record<string, number[]>) {
  const now = Date.now()
  const body: MetricsSeries = { range: '1h', step_ms: STEP, series: {} }
  for (const [metric, values] of Object.entries(series)) {
    body.series[metric] = values.map((v, i) => [now - (values.length - 1 - i) * STEP, v, v, v])
  }
  server.use(http.get('*/api/servers/:id/metrics', () => HttpResponse.json(body)))
}

async function axisLabels(name: string): Promise<string[]> {
  const chart = await screen.findByRole('img', { name })
  return Array.from(chart.querySelectorAll('[data-axis="left"]')).map((n) => n.textContent ?? '')
}

describe('MetricCharts axes', () => {
  it('steps players by whole numbers, CPU by the decimals its step needs and RAM in GiB', async () => {
    mockSeries({
      players: [0, 1, 0],
      cpu_percent: [0.01, 0.08, 0.03],
      memory_bytes: [4 * GIB, 4 * GIB, 4 * GIB],
      memory_limit_bytes: [4 * GIB, 4 * GIB, 4 * GIB],
    })
    await renderWithProviders(<MetricCharts serverId={1} range="1h" />, { me: loginAs('admin') })
    expect(await axisLabels('Spieler')).toEqual(['0', '1'])
    expect(await axisLabels('CPU-Auslastung')).toEqual(['0 %', '0,02 %', '0,04 %', '0,06 %', '0,08 %'])
    expect(await axisLabels('Arbeitsspeicher')).toEqual(['0 GiB', '1 GiB', '2 GiB', '3 GiB', '4 GiB'])
  })
})
