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

import type { OpenCodeGoUsage } from '../../api'
import { PlanUsageDialog } from './plan-usage-dialog'

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
    <PlanUsageDialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('OpenCode Go Usage')}
      channelLabel={props.channelLabel}
      limitReached={props.usage?.limit_reached}
      windows={[
        { title: t('5-Hour Window'), window: props.usage?.five_hour },
        { title: t('Weekly Window'), window: props.usage?.weekly },
        { title: t('Monthly Window'), window: props.usage?.monthly },
      ]}
      onRefresh={props.onRefresh}
      isRefreshing={props.isRefreshing}
    />
  )
}
