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
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Activity, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { requireServerSuccess } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import {
  getConsoleOverview,
  getConsoleRankings,
  getConsoleRevenue,
  getConsoleUsers,
} from '../../api'
import { formatUtcOffset } from '../../lib/console'
import type { ConsoleRankingPeriod, ConsoleSummaryPeriod } from '../../types'
import { OverviewMetricCards } from './overview-metric-cards'
import { RankingCard } from './ranking-cards'
import { RevenueTrendCard, UserTrendCard } from './trend-cards'

const CONSOLE_QUERY_KEY = ['dashboard', 'admin-console'] as const
const CONSOLE_STALE_TIME = 60_000

/**
 * The admin home: today's figures, revenue and user trends, and the usage
 * rankings of the whole site.
 */
export function AdminOverview() {
  const { t } = useTranslation()
  const [revenuePeriod, setRevenuePeriod] =
    useState<ConsoleSummaryPeriod>('monthly')
  const [userPeriod, setUserPeriod] = useState<ConsoleSummaryPeriod>('monthly')
  const [rankingPeriod, setRankingPeriod] =
    useState<ConsoleRankingPeriod>('today')

  const overviewQuery = useQuery({
    queryKey: [...CONSOLE_QUERY_KEY, 'overview'],
    queryFn: async () => requireServerSuccess(await getConsoleOverview()).data,
    staleTime: CONSOLE_STALE_TIME,
  })
  const revenueQuery = useQuery({
    queryKey: [...CONSOLE_QUERY_KEY, 'revenue'],
    queryFn: async () => requireServerSuccess(await getConsoleRevenue()).data,
    staleTime: CONSOLE_STALE_TIME,
  })
  const usersQuery = useQuery({
    queryKey: [...CONSOLE_QUERY_KEY, 'users'],
    queryFn: async () => requireServerSuccess(await getConsoleUsers()).data,
    staleTime: CONSOLE_STALE_TIME,
  })
  const rankingsQuery = useQuery({
    queryKey: [...CONSOLE_QUERY_KEY, 'rankings', rankingPeriod],
    queryFn: async () =>
      requireServerSuccess(await getConsoleRankings(rankingPeriod)).data,
    staleTime: CONSOLE_STALE_TIME,
    placeholderData: keepPreviousData,
  })

  const queries = [overviewQuery, revenueQuery, usersQuery, rankingsQuery]
  const refreshing = queries.some((query) => query.isFetching)
  const settled = queries.every((query) => !query.isPending)
  const hasData = queries.some((query) => query.data !== undefined)
  const zone =
    overviewQuery.data ??
    revenueQuery.data ??
    usersQuery.data ??
    rankingsQuery.data

  const handleRefresh = () => {
    void Promise.all(queries.map((query) => query.refetch()))
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Overview')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button variant='outline' onClick={handleRefresh} disabled={refreshing}>
          <RefreshCw
            data-icon='inline-start'
            className={cn(refreshing && 'animate-spin')}
          />
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-3 sm:gap-4'>
          <p className='text-muted-foreground text-sm'>
            {zone
              ? t(
                  'Revenue, users and usage of the whole site. Days and hours follow the server time zone {{zone}} ({{offset}}).',
                  {
                    zone: zone.time_zone,
                    offset: formatUtcOffset(zone.utc_offset),
                  }
                )
              : t('Revenue, users and usage of the whole site.')}
          </p>
          {settled && !hasData ? (
            <EmptyState
              icon={Activity}
              title={t('No overview data yet')}
              description={t(
                'The figures appear here once the overview loads. Refresh to try again.'
              )}
              bordered
            />
          ) : (
            <>
              <OverviewMetricCards
                overview={overviewQuery.data}
                loading={overviewQuery.isPending}
              />
              <div className='grid gap-3 sm:gap-4 xl:grid-cols-2'>
                <RevenueTrendCard
                  revenue={revenueQuery.data}
                  loading={revenueQuery.isPending}
                  refreshing={revenueQuery.isFetching}
                  period={revenuePeriod}
                  onPeriodChange={setRevenuePeriod}
                />
                <UserTrendCard
                  users={usersQuery.data}
                  loading={usersQuery.isPending}
                  refreshing={usersQuery.isFetching}
                  period={userPeriod}
                  onPeriodChange={setUserPeriod}
                />
              </div>
              <div className='grid gap-3 sm:gap-4 xl:grid-cols-2'>
                <RankingCard
                  kind='models'
                  rankings={rankingsQuery.data}
                  loading={rankingsQuery.isPending}
                  refreshing={rankingsQuery.isFetching}
                  period={rankingPeriod}
                  onPeriodChange={setRankingPeriod}
                />
                <RankingCard
                  kind='users'
                  rankings={rankingsQuery.data}
                  loading={rankingsQuery.isPending}
                  refreshing={rankingsQuery.isFetching}
                  period={rankingPeriod}
                  onPeriodChange={setRankingPeriod}
                />
              </div>
            </>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
