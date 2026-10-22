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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatQuota } from '@/lib/format'
import { useChartTheme } from '@/lib/use-chart-theme'

import { CONSOLE_RANKING_PERIODS } from '../../constants'
import {
  buildConsoleRankingSpec,
  type ConsoleRankingBar,
} from '../../lib/console'
import type { ConsoleRankingPeriod, ConsoleRankings } from '../../types'
import { PanelWrapper } from '../ui/panel-wrapper'
import { ConsoleChartFrame, ConsolePeriodTabs } from './trend-cards'

/**
 * The top models or users of a day by consumed quota, as horizontal bars.
 */
export function RankingCard(props: {
  kind: 'models' | 'users'
  rankings: ConsoleRankings | undefined
  loading: boolean
  refreshing: boolean
  period: ConsoleRankingPeriod
  onPeriodChange: (period: ConsoleRankingPeriod) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const { resolvedTheme } = useChartTheme()
  const rankings = props.rankings

  const bars = useMemo<ConsoleRankingBar[]>(() => {
    if (!rankings) return []
    if (props.kind === 'models') {
      return rankings.models.map((item) => ({
        name: item.model_name || t('Unknown'),
        quota: item.quota,
        requests: item.requests,
        tokens: item.tokens,
      }))
    }
    return rankings.users.map((item) => ({
      name: item.username || `#${item.user_id}`,
      quota: item.quota,
      requests: item.requests,
      tokens: item.tokens,
    }))
  }, [props.kind, rankings, t])

  const spec = useMemo(
    () =>
      bars.length === 0
        ? null
        : buildConsoleRankingSpec({
            bars,
            theme: resolvedTheme === 'dark' ? 'dark' : 'light',
            formatQuota,
            formatCount: (value) => formatNumber(value, locale),
            labels: {
              quota: t('Consumption'),
              requests: t('Requests'),
              tokens: t('Tokens'),
            },
          }),
    [bars, locale, resolvedTheme, t]
  )

  const title = props.kind === 'models' ? t('Top models') : t('Top users')
  const description =
    props.period === 'today'
      ? t('Top 10 by quota consumed today')
      : t('Top 10 by quota consumed yesterday')

  return (
    <PanelWrapper
      title={title}
      description={description}
      contentClassName='p-0 pb-3'
      headerActions={
        <ConsolePeriodTabs
          value={props.period}
          options={CONSOLE_RANKING_PERIODS}
          onValueChange={props.onPeriodChange}
          label={title}
        />
      }
    >
      <ConsoleChartFrame
        label={`${title}, ${description}`}
        loading={props.loading}
        refreshing={props.refreshing}
        empty={!props.loading && bars.length === 0}
        emptyTitle={t('No usage in this period')}
        chartKey={`ranking-${props.kind}-${props.period}`}
        spec={spec}
      />
    </PanelWrapper>
  )
}
