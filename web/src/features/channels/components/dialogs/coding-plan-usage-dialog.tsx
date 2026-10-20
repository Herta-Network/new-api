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

import type { CodingPlanProvider, CodingPlanUsage } from '../../api'
import { CODING_PLAN_NAMES } from '../../lib/coding-plan'
import { PlanUsageDialog } from './plan-usage-dialog'

type CodingPlanUsageDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  provider: CodingPlanProvider
  channelLabel: string
  usage: CodingPlanUsage | null
  onRefresh: () => void | Promise<void>
  isRefreshing: boolean
}

export function CodingPlanUsageDialog(props: CodingPlanUsageDialogProps) {
  const { t } = useTranslation()

  return (
    <PlanUsageDialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('{{provider}} Usage', {
        provider: CODING_PLAN_NAMES[props.provider],
      })}
      channelLabel={props.channelLabel}
      plan={props.usage?.plan}
      limitReached={props.usage?.limit_reached}
      windows={[
        { title: t('5-Hour Window'), window: props.usage?.five_hour },
        { title: t('Weekly Window'), window: props.usage?.weekly },
      ]}
      onRefresh={props.onRefresh}
      isRefreshing={props.isRefreshing}
    />
  )
}
