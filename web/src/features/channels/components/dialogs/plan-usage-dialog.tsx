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
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import type { RateLimitWindowData } from '../../lib/rate-limit-window'
import { RateLimitWindow } from '../rate-limit-window'

export type PlanUsageWindow = {
  title: string
  window?: RateLimitWindowData | null
}

type PlanUsageDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  channelLabel: string
  /** Subscription tier the provider reports, shown next to the availability. */
  plan?: string
  limitReached?: boolean
  windows: PlanUsageWindow[]
  onRefresh: () => void | Promise<void>
  isRefreshing: boolean
}

/** Usage windows of a subscription plan channel, such as OpenCode Go or a Coding Plan. */
export function PlanUsageDialog(props: PlanUsageDialogProps) {
  const { t } = useTranslation()
  const threeColumns = props.windows.length >= 3

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={props.title}
      description={props.channelLabel}
      contentClassName={threeColumns ? 'sm:max-w-[900px]' : 'sm:max-w-[640px]'}
      contentHeight='auto'
      bodyClassName='flex flex-col gap-4'
      footer={
        <Button
          type='button'
          variant='outline'
          onClick={() => props.onOpenChange(false)}
        >
          {t('Close')}
        </Button>
      }
    >
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex flex-wrap items-center gap-2'>
          {props.limitReached ? (
            <StatusBadge
              label={t('Limited')}
              variant='danger'
              copyable={false}
            />
          ) : (
            <StatusBadge
              label={t('Available')}
              variant='success'
              copyable={false}
            />
          )}
          {props.plan && (
            <StatusBadge
              label={t('Plan: {{plan}}', { plan: props.plan })}
              variant='neutral'
              copyable={false}
            />
          )}
        </div>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={props.onRefresh}
          disabled={props.isRefreshing}
        >
          <RefreshCw data-icon='inline-start' />
          {t('Refresh')}
        </Button>
      </div>
      <div
        className={cn(
          'grid grid-cols-1 gap-3',
          threeColumns ? 'md:grid-cols-3' : 'md:grid-cols-2'
        )}
      >
        {props.windows.map((item) => (
          <RateLimitWindow
            key={item.title}
            title={item.title}
            window={item.window}
          />
        ))}
      </div>
    </Dialog>
  )
}
