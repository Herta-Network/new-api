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
import { VChart } from '@visactor/react-vchart'
import { ChartArea } from 'lucide-react'
import { useMemo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toIntlLocale } from '@/i18n/languages'
import { formatMinorAmount } from '@/lib/currency'
import { formatCompactNumber, formatNumber } from '@/lib/format'
import { useChartTheme } from '@/lib/use-chart-theme'
import { cn } from '@/lib/utils'
import { VCHART_OPTION } from '@/lib/vchart'

import { CONSOLE_SUMMARY_PERIODS } from '../../constants'
import {
  buildConsoleTrendSpec,
  formatConsoleBucketLabel,
  type ConsoleTrendPoint,
} from '../../lib/console'
import type {
  ConsoleRevenue,
  ConsoleSummaryPeriod,
  ConsoleUsers,
} from '../../types'
import { PanelWrapper } from '../ui/panel-wrapper'
import { SummaryStrip, type SummaryStripItem } from '../ui/summary-strip'

export function ConsolePeriodTabs<T extends string>(props: {
  value: T
  options: readonly { value: T; labelKey: string }[]
  onValueChange: (value: T) => void
  label: string
}) {
  const { t } = useTranslation()
  return (
    <Tabs
      value={props.value}
      onValueChange={(value) => props.onValueChange(value as T)}
      className='shrink-0'
    >
      <TabsList aria-label={props.label}>
        {props.options.map((option) => (
          <TabsTrigger
            key={option.value}
            value={option.value}
            className='px-2.5 text-xs'
          >
            {t(option.labelKey)}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  )
}

/**
 * The chart area of an overview card: a skeleton until the first load, the
 * empty state for a period without data, then the chart, dimmed while it
 * refetches.
 */
export function ConsoleChartFrame(props: {
  label: string
  loading: boolean
  refreshing: boolean
  empty: boolean
  emptyTitle: string
  chartKey: string
  spec: Record<string, unknown> | null
}) {
  const { resolvedTheme, themeReady } = useChartTheme()
  let content: ReactNode = null
  if (props.loading) {
    content = <Skeleton className='h-full w-full' />
  } else if (props.empty) {
    content = (
      <EmptyState
        icon={ChartArea}
        title={props.emptyTitle}
        className='h-full min-h-0 p-0'
      />
    )
  } else if (themeReady && props.spec) {
    content = (
      <VChart
        key={`${props.chartKey}-${resolvedTheme}`}
        spec={{
          ...props.spec,
          theme: resolvedTheme === 'dark' ? 'dark' : 'light',
          background: 'transparent',
        }}
        option={VCHART_OPTION}
      />
    )
  }
  return (
    <div
      role='img'
      aria-label={props.label}
      className={cn(
        'h-72 px-2 pt-3 transition-opacity sm:px-3',
        props.refreshing && 'opacity-60'
      )}
    >
      {content}
    </div>
  )
}

function ConsoleTrendCard(props: {
  title: string
  description: string
  period: ConsoleSummaryPeriod
  onPeriodChange: (period: ConsoleSummaryPeriod) => void
  chart: ReactNode
  summary: SummaryStripItem[]
  loading: boolean
  footnote?: ReactNode
}) {
  return (
    <PanelWrapper
      title={props.title}
      description={props.description}
      contentClassName='p-0'
      headerActions={
        <ConsolePeriodTabs
          value={props.period}
          options={CONSOLE_SUMMARY_PERIODS}
          onValueChange={props.onPeriodChange}
          label={props.title}
        />
      }
    >
      {props.chart}
      <SummaryStrip
        className='mt-2'
        items={props.summary}
        loading={props.loading}
      />
      {props.footnote}
    </PanelWrapper>
  )
}

function periodLabelKey(period: ConsoleSummaryPeriod) {
  return (
    CONSOLE_SUMMARY_PERIODS.find((option) => option.value === period)
      ?.labelKey ?? period
  )
}

export function RevenueTrendCard(props: {
  revenue: ConsoleRevenue | undefined
  loading: boolean
  refreshing: boolean
  period: ConsoleSummaryPeriod
  onPeriodChange: (period: ConsoleSummaryPeriod) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const { resolvedTheme } = useChartTheme()
  const revenue = props.revenue
  const currency = revenue?.currency ?? ''
  const data = revenue?.[props.period]
  const money = (minor: number) => formatMinorAmount(minor, currency, locale)
  const topupLabel = t('Top-up')
  const subscriptionLabel = t('Subscription')
  const totalLabel = t('Total revenue')

  const spec = useMemo(() => {
    if (!data) return null
    const points: ConsoleTrendPoint[] = data.list.flatMap((bucket) => {
      const label = formatConsoleBucketLabel(bucket.date, props.period)
      return [
        { start: bucket.start, label, series: topupLabel, value: bucket.topup },
        {
          start: bucket.start,
          label,
          series: subscriptionLabel,
          value: bucket.subscription,
        },
      ]
    })
    return buildConsoleTrendSpec({
      points,
      series: [topupLabel, subscriptionLabel],
      stacked: true,
      totalLabel,
      theme: resolvedTheme === 'dark' ? 'dark' : 'light',
      // Values stay in hundredths; only the axis ticks are scaled.
      formatValue: (value) => formatMinorAmount(value, currency, locale),
      formatTick: (value) => formatCompactNumber(value / 100, locale),
    })
  }, [
    currency,
    data,
    locale,
    props.period,
    resolvedTheme,
    subscriptionLabel,
    topupLabel,
    totalLabel,
  ])

  const notes: string[] = []
  if (data && data.manual > 0) {
    notes.push(
      t('Includes {{amount}} completed by hand', {
        amount: money(data.manual),
      })
    )
  }
  if (data && data.balance_spending > 0) {
    notes.push(
      t('Wallet balance paid {{amount}}, which is not revenue', {
        amount: money(data.balance_spending),
      })
    )
  }
  const others = (data?.other_currencies ?? [])
    .map((other) => {
      const parts = []
      if (other.revenue > 0) {
        parts.push(formatMinorAmount(other.revenue, other.currency, locale))
      }
      if (other.balance_spending > 0) {
        parts.push(
          t('{{amount}} from wallet balance', {
            amount: formatMinorAmount(
              other.balance_spending,
              other.currency,
              locale
            ),
          })
        )
      }
      return parts.join(', ')
    })
    .filter(Boolean)
  if (others.length > 0) {
    notes.push(
      t('Other currencies, not added in: {{amounts}}', {
        amounts: others.join('; '),
      })
    )
  }
  const empty =
    !data ||
    (data.orders === 0 &&
      data.balance_orders === 0 &&
      data.other_currencies.length === 0)

  return (
    <ConsoleTrendCard
      title={t('Revenue')}
      description={t('Top-up and subscription revenue in {{currency}}', {
        currency,
      })}
      period={props.period}
      onPeriodChange={props.onPeriodChange}
      loading={props.loading}
      chart={
        <ConsoleChartFrame
          label={t('Revenue chart, {{period}}', {
            period: t(periodLabelKey(props.period)),
          })}
          loading={props.loading}
          refreshing={props.refreshing}
          empty={empty}
          emptyTitle={t('No revenue in this period')}
          chartKey={`revenue-${props.period}`}
          spec={spec}
        />
      }
      summary={[
        { label: totalLabel, value: money(data?.total ?? 0) },
        { label: topupLabel, value: money(data?.topup ?? 0) },
        { label: subscriptionLabel, value: money(data?.subscription ?? 0) },
      ]}
      footnote={
        notes.length > 0 ? (
          <ul className='text-muted-foreground space-y-0.5 border-t px-4 py-2.5 text-xs sm:px-5'>
            {notes.map((note) => (
              <li key={note}>{note}</li>
            ))}
          </ul>
        ) : null
      }
    />
  )
}

export function UserTrendCard(props: {
  users: ConsoleUsers | undefined
  loading: boolean
  refreshing: boolean
  period: ConsoleSummaryPeriod
  onPeriodChange: (period: ConsoleSummaryPeriod) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const { resolvedTheme } = useChartTheme()
  const data = props.users?.[props.period]
  const registrationsLabel = t('Registrations')
  const firstTimeLabel = t('First-time payers')
  const returningLabel = t('Returning payers')

  const spec = useMemo(() => {
    if (!data) return null
    const points: ConsoleTrendPoint[] = data.list.flatMap((bucket) => {
      const label = formatConsoleBucketLabel(bucket.date, props.period)
      return [
        {
          start: bucket.start,
          label,
          series: registrationsLabel,
          value: bucket.registrations,
        },
        {
          start: bucket.start,
          label,
          series: firstTimeLabel,
          value: bucket.first_time_payers,
        },
        {
          start: bucket.start,
          label,
          series: returningLabel,
          value: bucket.returning_payers,
        },
      ]
    })
    return buildConsoleTrendSpec({
      points,
      series: [registrationsLabel, firstTimeLabel, returningLabel],
      stacked: false,
      theme: resolvedTheme === 'dark' ? 'dark' : 'light',
      formatValue: (value) => formatNumber(value, locale),
      formatTick: (value) => formatCompactNumber(value, locale),
    })
  }, [
    data,
    firstTimeLabel,
    locale,
    props.period,
    registrationsLabel,
    resolvedTheme,
    returningLabel,
  ])

  const empty =
    !data ||
    data.registrations + data.first_time_payers + data.returning_payers === 0

  return (
    <ConsoleTrendCard
      title={t('Users')}
      description={t('Registrations, first-time payers and returning payers')}
      period={props.period}
      onPeriodChange={props.onPeriodChange}
      loading={props.loading}
      chart={
        <ConsoleChartFrame
          label={t('User chart, {{period}}', {
            period: t(periodLabelKey(props.period)),
          })}
          loading={props.loading}
          refreshing={props.refreshing}
          empty={empty}
          emptyTitle={t('No registrations or payments in this period')}
          chartKey={`users-${props.period}`}
          spec={spec}
        />
      }
      summary={[
        {
          label: registrationsLabel,
          value: formatNumber(data?.registrations ?? 0, locale),
        },
        {
          label: firstTimeLabel,
          value: formatNumber(data?.first_time_payers ?? 0, locale),
        },
        {
          label: returningLabel,
          value: formatNumber(data?.returning_payers ?? 0, locale),
        },
      ]}
    />
  )
}
