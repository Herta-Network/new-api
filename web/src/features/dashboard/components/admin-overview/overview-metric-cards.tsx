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
import { Link } from '@tanstack/react-router'
import {
  Activity,
  CircleAlert,
  Coins,
  Hash,
  Radio,
  UserPlus,
  Users,
  Wallet,
  type LucideIcon,
} from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Card } from '@/components/ui/card'
import type { IconBadgeTone } from '@/components/ui/icon-badge'
import { PAYMENT_ORDER_STATUS } from '@/features/payment-orders/constants'
import { toIntlLocale } from '@/i18n/languages'
import {
  formatBillingCurrencyFromUSD,
  formatMinorAmount,
  getCurrencyDisplay,
  isCurrencyDisplayEnabled,
} from '@/lib/currency'
import { formatCompactNumber, formatNumber, formatQuota } from '@/lib/format'

import type { ConsoleOverview } from '../../types'
import { StatCard } from '../ui/stat-card'

const METRIC_LINK_CLASS =
  'focus-visible:ring-ring focus-visible:ring-offset-background block rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-offset-2'

function OverviewMetric(props: {
  title: string
  value: string
  description: string
  icon: LucideIcon
  tone: IconBadgeTone
  loading: boolean
  children?: ReactNode
}) {
  return (
    <Card className='h-full gap-2 px-3 sm:px-4'>
      <StatCard
        title={props.title}
        value={props.value}
        description={props.description}
        icon={props.icon}
        iconTone={props.tone}
        sparklineVariant='none'
        loading={props.loading}
      />
      {props.children}
    </Card>
  )
}

/**
 * The eight figures of today at the top of the operations overview. Each card
 * opens the page with its details; the attention card links to both payment
 * queues it counts.
 */
export function OverviewMetricCards(props: {
  overview: ConsoleOverview | undefined
  loading: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const overview = props.overview
  const loading = props.loading && !overview
  const quota = overview?.quota ?? 0
  const channels = overview?.channels
  const attention = overview?.attention
  const attentionCount = attention?.count ?? 0
  const autoDisabled = channels?.auto_disabled ?? 0
  const usageDescription = (text: string) =>
    overview && !overview.usage_data_enabled
      ? t('Usage statistics are turned off')
      : text

  // Shown as quota points, the consumption is also given in money.
  const consumptionDescription = isCurrencyDisplayEnabled()
    ? t('Quota consumed by API calls')
    : t('About {{amount}}', {
        amount: formatBillingCurrencyFromUSD(
          quota / getCurrencyDisplay().config.quotaPerUnit
        ),
      })
  const attentionAmounts = (attention?.amounts ?? [])
    .map((item) => formatMinorAmount(item.amount, item.currency, locale))
    .join(' + ')

  return (
    <div className='grid grid-cols-2 gap-3 sm:gap-4 xl:grid-cols-4'>
      <Link
        to='/admin/dashboard/$section'
        params={{ section: 'models' }}
        className={METRIC_LINK_CLASS}
      >
        <OverviewMetric
          title={t('Requests today')}
          value={formatNumber(overview?.requests ?? 0, locale)}
          description={usageDescription(t('API calls recorded so far today'))}
          icon={Activity}
          tone='chart-1'
          loading={loading}
        />
      </Link>
      <Link
        to='/admin/dashboard/$section'
        params={{ section: 'models' }}
        className={METRIC_LINK_CLASS}
      >
        <OverviewMetric
          title={t('Consumption today')}
          value={formatQuota(quota)}
          description={usageDescription(consumptionDescription)}
          icon={Coins}
          tone='chart-2'
          loading={loading}
        />
      </Link>
      <Link
        to='/admin/dashboard/$section'
        params={{ section: 'flow' }}
        className={METRIC_LINK_CLASS}
      >
        <OverviewMetric
          title={t('Tokens today')}
          value={formatCompactNumber(overview?.tokens ?? 0, locale)}
          description={usageDescription(
            t('Exact count: {{value}}', {
              value: formatNumber(overview?.tokens ?? 0, locale),
            })
          )}
          icon={Hash}
          tone='chart-3'
          loading={loading}
        />
      </Link>
      <Link
        to='/admin/dashboard/$section'
        params={{ section: 'users' }}
        className={METRIC_LINK_CLASS}
      >
        <OverviewMetric
          title={t('Active users today')}
          value={formatNumber(overview?.active_users ?? 0, locale)}
          description={usageDescription(t('Users who called the API today'))}
          icon={Users}
          tone='chart-4'
          loading={loading}
        />
      </Link>
      <Link to='/admin/users' className={METRIC_LINK_CLASS}>
        <OverviewMetric
          title={t('New users today')}
          value={formatNumber(overview?.registrations ?? 0, locale)}
          description={t('Accounts registered today')}
          icon={UserPlus}
          tone='chart-5'
          loading={loading}
        />
      </Link>
      <Link
        to='/admin/payment-orders/$section'
        params={{ section: 'orders' }}
        className={METRIC_LINK_CLASS}
      >
        <OverviewMetric
          title={t('Revenue today')}
          value={formatMinorAmount(
            overview?.revenue ?? 0,
            overview?.currency ?? '',
            locale
          )}
          description={t('Paid orders: {{value}}', {
            value: formatNumber(overview?.revenue_orders ?? 0, locale),
          })}
          icon={Wallet}
          tone='primary'
          loading={loading}
        />
      </Link>
      <Link to='/admin/channels' className={METRIC_LINK_CLASS}>
        <OverviewMetric
          title={t('Enabled channels')}
          value={formatNumber(channels?.enabled ?? 0, locale)}
          description={t(
            '{{total}} in total, {{auto}} disabled automatically',
            {
              total: formatNumber(channels?.total ?? 0, locale),
              auto: formatNumber(autoDisabled, locale),
            }
          )}
          icon={Radio}
          tone={autoDisabled > 0 ? 'warning' : 'success'}
          loading={loading}
        />
      </Link>
      <OverviewMetric
        title={t('Needs attention')}
        value={formatNumber(attentionCount, locale)}
        description={
          attentionCount > 0
            ? t('{{amount}} waiting for an operator', {
                amount: attentionAmounts,
              })
            : t('No payments are waiting')
        }
        icon={CircleAlert}
        tone={attentionCount > 0 ? 'warning' : 'success'}
        loading={loading}
      >
        {attention && (
          <div className='flex flex-wrap gap-x-3 gap-y-1 text-xs font-medium'>
            <Link
              to='/admin/payment-orders/$section'
              params={{ section: 'orders' }}
              search={{ status: [String(PAYMENT_ORDER_STATUS.PAID)] }}
              className='text-primary hover:underline'
            >
              {t('Unfulfilled orders: {{value}}', {
                value: formatNumber(attention.unfulfilled_orders, locale),
              })}
            </Link>
            <Link
              to='/admin/payment-orders/$section'
              params={{ section: 'unmatched' }}
              search={{ uState: ['false'] }}
              className='text-primary hover:underline'
            >
              {t('Unmatched payments: {{value}}', {
                value: formatNumber(attention.unmatched_payments, locale),
              })}
            </Link>
          </div>
        )}
      </OverviewMetric>
    </div>
  )
}
