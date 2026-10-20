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

import type { OpenCodeGoUsage } from '../../api'
import { RateLimitWindow } from '../rate-limit-window'

type OpenCodeGoUsageDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  channelLabel: string
  usage: OpenCodeGoUsage | null
  onRefresh: () => void | Promise<void>
  isRefreshing: boolean
}

export function OpenCodeGoUsageDialog(props: OpenCodeGoUsageDialogProps) {
  const { t } = useTranslation()

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('OpenCode Go Usage')}
      description={props.channelLabel}
      contentClassName='sm:max-w-[900px]'
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
        {props.usage?.limit_reached ? (
          <StatusBadge label={t('Limited')} variant='danger' copyable={false} />
        ) : (
          <StatusBadge
            label={t('Available')}
            variant='success'
            copyable={false}
          />
        )}
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
      <div className='grid grid-cols-1 gap-3 md:grid-cols-3'>
        <RateLimitWindow
          title={t('5-Hour Window')}
          window={props.usage?.five_hour}
        />
        <RateLimitWindow
          title={t('Weekly Window')}
          window={props.usage?.weekly}
        />
        <RateLimitWindow
          title={t('Monthly Window')}
          window={props.usage?.monthly}
        />
      </div>
    </Dialog>
  )
}
