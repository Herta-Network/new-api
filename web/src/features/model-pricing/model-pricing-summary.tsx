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

import { StatusBadge, type StatusVariant } from '@/components/status-badge'

import type { ModelPricingEntry } from './api'
import { formatPricingAmount } from './currency'
import {
  getModelPriceSummary,
  MODEL_PRICING_MODE_LABELS,
  type ModelPricingMode,
} from './pricing-mode'

const MODE_VARIANTS: Record<ModelPricingMode, StatusVariant> = {
  simple: 'success',
  advanced: 'purple',
  task: 'info',
  legacy: 'warning',
  unpriced: 'danger',
}

export function ModelPricingModeBadge(props: { mode: ModelPricingMode }) {
  const { t } = useTranslation()
  return (
    <StatusBadge
      label={t(MODEL_PRICING_MODE_LABELS[props.mode])}
      variant={MODE_VARIANTS[props.mode]}
      copyable={false}
      size='sm'
    />
  )
}

/** Model prices are shown in USD, the unit the billing engine stores. */
export function ModelPriceSummary(props: {
  entry: Pick<
    ModelPricingEntry,
    'model_name' | 'effective' | 'pricing_mode' | 'billing_details'
  >
  mode: ModelPricingMode
}) {
  const { t } = useTranslation()
  if (props.mode === 'unpriced') {
    return <span className='text-destructive text-sm'>{t('No price set')}</span>
  }
  if (props.mode === 'task') {
    return (
      <span className='text-muted-foreground text-sm'>
        {t('Usage-based pricing')}
      </span>
    )
  }
  const summary = getModelPriceSummary(props.entry)
  if (!summary) {
    return (
      <span className='text-muted-foreground text-sm'>
        {t('Special billing expression')}
      </span>
    )
  }
  let value: string
  let caption: string
  if (summary.unit === 'token') {
    value = `${formatPricingAmount(summary.input)} / ${formatPricingAmount(summary.output)}`
    caption = t('Input / output, USD per 1M tokens')
  } else {
    value = formatPricingAmount(summary.price)
    caption = summary.perImage ? t('USD per image') : t('USD per request')
  }
  if (summary.tiers > 1) {
    caption += ` · ${t('{{count}} tiers', { count: summary.tiers })}`
  }
  return (
    <span className='block min-w-0 whitespace-normal'>
      <span className='block font-mono text-sm break-words tabular-nums'>
        {value}
      </span>
      <span className='text-muted-foreground block text-xs break-words'>
        {caption}
      </span>
    </span>
  )
}
