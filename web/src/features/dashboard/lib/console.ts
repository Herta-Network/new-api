/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { ConsoleSummaryPeriod } from '../types'

type ChartTheme = 'light' | 'dark'

/**
 * Series colors of the operations overview charts, in a fixed order. Each set
 * passes the categorical colorblind and contrast checks against the card
 * surface of its theme (#ffffff light, #2a2a2a dark); the light third slot
 * sits below 3:1, so its values are also printed in the summary strip.
 */
const CONSOLE_SERIES_COLORS: Record<ChartTheme, readonly string[]> = {
  light: ['#2a78d6', '#eb6834', '#1baf7a'],
  dark: ['#3987e5', '#d95926', '#199e70'],
}

const CONSOLE_CHART_INK: Record<ChartTheme, { text: string; grid: string }> = {
  light: { text: 'rgba(15, 23, 42, 0.58)', grid: 'rgba(15, 23, 42, 0.1)' },
  dark: {
    text: 'rgba(255, 255, 255, 0.68)',
    grid: 'rgba(255, 255, 255, 0.12)',
  },
}

/**
 * The axis label of a server-zone bucket: "15:00" for an hour, "10-22" for a
 * day and "2026-10" for a month. Labels are not converted to the browser's
 * zone, since the buckets are the server's days.
 */
export function formatConsoleBucketLabel(
  date: string,
  period: ConsoleSummaryPeriod
): string {
  if (period === 'today') return date.slice(11, 16)
  if (period === 'monthly') return date.slice(5, 10)
  return date
}

/** "UTC+08:00" for an offset of 28800 seconds. */
export function formatUtcOffset(seconds: number): string {
  const sign = seconds < 0 ? '-' : '+'
  const minutes = Math.abs(Math.round(seconds / 60))
  const hours = String(Math.floor(minutes / 60)).padStart(2, '0')
  return `UTC${sign}${hours}:${String(minutes % 60).padStart(2, '0')}`
}

export interface ConsoleTrendPoint {
  /** The bucket start: two buckets can share a label on a daylight saving day. */
  start: number
  label: string
  series: string
  value: number
}

/**
 * An area chart of `points` over the buckets, one series per entry of
 * `series` in color order. Stacked series add up to the `totalLabel` row of
 * the tooltip.
 */
export function buildConsoleTrendSpec(args: {
  points: ConsoleTrendPoint[]
  series: string[]
  stacked: boolean
  totalLabel?: string
  theme: ChartTheme
  formatValue: (value: number) => string
  formatTick: (value: number) => string
}) {
  const ink = CONSOLE_CHART_INK[args.theme]
  const bucketCount = args.points.length / Math.max(1, args.series.length)
  const labels = new Map(
    args.points.map((point) => [String(point.start), point.label])
  )
  return {
    type: 'area' as const,
    data: [{ id: 'console-trend', values: args.points }],
    xField: 'start',
    yField: 'value',
    seriesField: 'series',
    stack: args.stacked,
    color: {
      type: 'ordinal',
      domain: args.series,
      range: CONSOLE_SERIES_COLORS[args.theme].slice(0, args.series.length),
    },
    line: {
      style: {
        lineWidth: 2,
        curveType: 'monotone',
        lineCap: 'round',
        lineJoin: 'round',
      },
    },
    area: {
      style: {
        curveType: 'monotone',
        fillOpacity: args.stacked ? 0.16 : 0.08,
      },
    },
    // A lone bucket has no line to draw, so it shows as a dot.
    point: {
      visible: bucketCount <= 1,
      style: { size: 8, lineWidth: 2, stroke: 'transparent' },
    },
    legends: {
      visible: true,
      orient: 'top',
      position: 'start',
      item: { label: { style: { fill: ink.text, fontSize: 12 } } },
    },
    crosshair: {
      xField: {
        visible: true,
        line: { type: 'line', style: { stroke: ink.text, lineWidth: 1 } },
      },
    },
    axes: [
      {
        orient: 'bottom',
        type: 'band',
        label: {
          formatMethod: (value: number | string) =>
            labels.get(String(value)) ?? String(value),
          style: { fill: ink.text, fontSize: 11 },
          autoHide: true,
          autoLimit: true,
        },
        tick: { visible: false },
        domainLine: { style: { stroke: ink.grid } },
      },
      {
        orient: 'left',
        type: 'linear',
        label: {
          formatMethod: (value: number | string) =>
            args.formatTick(Number(value)),
          style: { fill: ink.text, fontSize: 11 },
        },
        grid: { visible: true, style: { stroke: ink.grid, lineDash: [] } },
        domainLine: { visible: false },
      },
    ],
    tooltip: {
      mark: { visible: false },
      dimension: {
        title: {
          value: (datum: Record<string, unknown>) => String(datum?.label ?? ''),
        },
        content: [
          {
            key: (datum: Record<string, unknown>) =>
              String(datum?.series ?? ''),
            value: (datum: Record<string, unknown>) =>
              Number(datum?.value) || 0,
          },
        ],
        updateContent: (
          items: Array<{ key: string; value: string | number }>
        ) => {
          const rows = items.map((item) => ({
            ...item,
            value: args.formatValue(Number(item.value) || 0),
          }))
          if (!args.stacked || !args.totalLabel) return rows
          const total = items.reduce(
            (sum, item) => sum + (Number(item.value) || 0),
            0
          )
          return [
            { key: args.totalLabel, value: args.formatValue(total) },
            ...rows,
          ]
        },
      },
    },
    animationAppear: { duration: 400 },
  }
}

export interface ConsoleRankingBar {
  name: string
  quota: number
  requests: number
  tokens: number
}

/**
 * Horizontal bars of the consumed quota, largest first, with every value
 * printed at its bar end.
 */
export function buildConsoleRankingSpec(args: {
  bars: ConsoleRankingBar[]
  theme: ChartTheme
  formatQuota: (value: number) => string
  formatCount: (value: number) => string
  labels: { quota: string; requests: string; tokens: string }
}) {
  const ink = CONSOLE_CHART_INK[args.theme]
  return {
    type: 'bar' as const,
    direction: 'horizontal' as const,
    data: [{ id: 'console-ranking', values: args.bars }],
    xField: 'quota',
    yField: 'name',
    color: [CONSOLE_SERIES_COLORS[args.theme][0]],
    barMaxWidth: 20,
    bar: {
      style: { cornerRadius: [0, 4, 4, 0] },
      state: { hover: { fillOpacity: 0.8 } },
    },
    label: {
      visible: true,
      position: 'outside',
      formatMethod: (_text: unknown, datum: Record<string, unknown>) =>
        args.formatQuota(Number(datum?.quota) || 0),
      style: { fill: ink.text, fontSize: 11 },
    },
    axes: [
      {
        orient: 'left',
        type: 'band',
        label: {
          style: { fill: ink.text, fontSize: 11 },
          autoLimit: true,
        },
        maxWidth: '35%',
        tick: { visible: false },
        domainLine: { style: { stroke: ink.grid } },
      },
      {
        orient: 'bottom',
        type: 'linear',
        visible: false,
        // Room at the bar ends for the value labels.
        max: Math.max(1, ...args.bars.map((bar) => bar.quota)) * 1.25,
      },
    ],
    tooltip: {
      dimension: { visible: false },
      mark: {
        title: {
          value: (datum: Record<string, unknown>) => String(datum?.name ?? ''),
        },
        content: [
          {
            key: args.labels.quota,
            value: (datum: Record<string, unknown>) =>
              args.formatQuota(Number(datum?.quota) || 0),
          },
          {
            key: args.labels.requests,
            value: (datum: Record<string, unknown>) =>
              args.formatCount(Number(datum?.requests) || 0),
          },
          {
            key: args.labels.tokens,
            value: (datum: Record<string, unknown>) =>
              args.formatCount(Number(datum?.tokens) || 0),
          },
        ],
      },
    },
    animationAppear: { duration: 400 },
  }
}
