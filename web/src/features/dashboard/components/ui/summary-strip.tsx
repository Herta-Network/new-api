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
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

export interface SummaryStripItem {
  label: string
  value: string
}

interface SummaryStripProps {
  /** Three figures, side by side from the small breakpoint up. */
  items: SummaryStripItem[]
  loading?: boolean
  className?: string
}

/**
 * The row of figures under a chart card: the totals of the period the chart
 * shows, readable without hovering the chart.
 */
export function SummaryStrip(props: SummaryStripProps) {
  return (
    <dl className={cn('grid border-t sm:grid-cols-3', props.className)}>
      {props.items.map((item) => (
        <div
          key={item.label}
          className='min-w-0 border-t px-4 py-3 first:border-t-0 sm:border-t-0 sm:border-l sm:px-5 sm:first:border-l-0'
        >
          <dt className='text-muted-foreground truncate text-xs'>
            {item.label}
          </dt>
          <dd className='mt-1 truncate text-lg font-semibold tracking-tight'>
            {props.loading ? (
              <Skeleton className='h-6 w-24' />
            ) : (
              <span title={item.value}>{item.value}</span>
            )}
          </dd>
        </div>
      ))}
    </dl>
  )
}
