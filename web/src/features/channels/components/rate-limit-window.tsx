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
import { useTranslation } from 'react-i18next'

import type { StatusBadgeProps } from '@/components/status-badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  formatDurationSeconds,
  type RateLimitWindowData,
} from '../lib/rate-limit-window'

function clampPercent(value: unknown): number {
  const v = Number(value)
  return Number.isFinite(v) ? Math.max(0, Math.min(100, v)) : 0
}

function formatUnixSeconds(unixSeconds: unknown): string {
  const v = Number(unixSeconds)
  return Number.isFinite(v) && v > 0 ? formatTimestampToDate(v) : '-'
}

function windowLabel(windowData?: RateLimitWindowData | null) {
  const percent = clampPercent(windowData?.used_percent)
  let variant: StatusBadgeProps['variant'] = 'info'
  if (percent >= 95) {
    variant = 'danger'
  } else if (percent >= 80) {
    variant = 'warning'
  }
  return { percent, variant }
}

const percentTextClassName: Record<
  NonNullable<StatusBadgeProps['variant']>,
  string
> = {
  success: 'text-success',
  warning: 'text-warning',
  danger: 'text-destructive',
  info: 'text-info',
  neutral: 'text-muted-foreground',
  purple: 'text-chart-4',
  amber: 'text-warning',
  blue: 'text-chart-1',
  cyan: 'text-chart-2',
  green: 'text-success',
  grey: 'text-muted-foreground',
  indigo: 'text-chart-1',
  'light-blue': 'text-info',
  'light-green': 'text-emerald-500 dark:text-emerald-300',
  lime: 'text-chart-3',
  orange: 'text-warning',
  pink: 'text-chart-5',
  red: 'text-destructive',
  teal: 'text-chart-2',
  violet: 'text-chart-4',
  yellow: 'text-warning',
}

type RateLimitWindowProps = {
  title: string
  window?: RateLimitWindowData | null
}

export function RateLimitWindow(props: RateLimitWindowProps) {
  const { t } = useTranslation()
  const hasData =
    !!props.window &&
    typeof props.window === 'object' &&
    Object.keys(props.window).length > 0
  const { percent, variant } = windowLabel(props.window)

  return (
    <Card size='sm' className='gap-0 py-0'>
      <CardHeader className='p-3 pb-2'>
        <div className='flex items-start justify-between gap-3'>
          <div className='min-w-0'>
            <CardTitle className='text-sm font-semibold'>
              {props.title}
            </CardTitle>
            <CardDescription className='mt-1 text-xs'>
              {t('Window:')}{' '}
              {hasData
                ? formatDurationSeconds(props.window?.limit_window_seconds, t)
                : '-'}
            </CardDescription>
          </div>
          <div className='shrink-0 text-right'>
            <div
              className={cn(
                'text-xl leading-none font-semibold tabular-nums',
                percentTextClassName[variant ?? 'neutral']
              )}
            >
              {hasData ? `${percent}%` : '-'}
            </div>
            <div className='text-muted-foreground mt-1 text-[11px]'>
              {t('Used')}
            </div>
          </div>
        </div>
      </CardHeader>
      <CardContent className='p-3 pt-0'>
        {hasData ? (
          <Progress
            value={percent}
            aria-label={`${props.title} usage: ${percent}%`}
            className='mt-1'
          />
        ) : (
          <div className='text-muted-foreground mt-1 text-sm'>-</div>
        )}
        <div className='mt-3 grid grid-cols-1 gap-2 text-xs sm:grid-cols-2'>
          <div className='min-w-0'>
            <div className='text-muted-foreground text-[11px]'>
              {t('Reset at:')}
            </div>
            <div className='break-all tabular-nums'>
              {hasData ? formatUnixSeconds(props.window?.reset_at) : '-'}
            </div>
          </div>
          <div className='min-w-0 sm:text-right'>
            <div className='text-muted-foreground text-[11px]'>
              {t('Resets in:')}
            </div>
            <div className='tabular-nums'>
              {hasData
                ? formatDurationSeconds(props.window?.reset_after_seconds, t)
                : '-'}
            </div>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
