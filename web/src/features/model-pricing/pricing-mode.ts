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
import {
  parseTiersFromExpr,
  splitBillingExprAndRequestRules,
} from '@/features/pricing/lib/billing-expr'
import {
  buildPreviewRows,
  createInitialLaneState,
} from '@/features/system-settings/models/model-pricing-core'

import type { ModelPricingEntry, ServerPricingMode } from './api'
import type { PricingCurrency } from './currency'
import {
  pricingRow,
  type CacheWriteMode,
  type LegacyBillingDetails,
  type PricingValues,
} from './pricing'
import { parseSimplePricingExpr } from './simple-pricing'

export type ModelPricingMode =
  | 'simple'
  | 'advanced'
  | 'task'
  | 'legacy'
  | 'unpriced'

export const MODEL_PRICING_MODE_LABELS: Record<ModelPricingMode, string> = {
  simple: 'Simple pricing',
  advanced: 'Advanced pricing',
  task: 'Task pricing',
  legacy: 'Legacy pricing',
  unpriced: 'Unpriced',
}

/** Server-side list filters; simple and advanced share one expression filter. */
export const SERVER_PRICING_MODE_LABELS: Record<ServerPricingMode, string> = {
  expression: 'Expression pricing',
  task: 'Task pricing',
  legacy: 'Legacy pricing',
  unpriced: 'Unpriced',
}

type PricedEntry = Pick<
  ModelPricingEntry,
  'model_name' | 'effective' | 'pricing_mode' | 'billing_details'
>

export function getModelPricingMode(entry: PricedEntry): ModelPricingMode {
  if (entry.pricing_mode === 'expression') {
    const expression = entry.effective['billing_setting.billing_expr']
    return typeof expression === 'string' && parseSimplePricingExpr(expression)
      ? 'simple'
      : 'advanced'
  }
  if (entry.pricing_mode === 'task' || entry.pricing_mode === 'legacy') {
    return entry.pricing_mode
  }
  return 'unpriced'
}

/** USD amounts of the first tier: per 1M tokens, or per request. */
export type ModelPriceSummary =
  | { unit: 'token'; input: number; output: number; tiers: number }
  | { unit: 'request'; price: number; perImage: boolean; tiers: number }

export function getModelPriceSummary(
  entry: PricedEntry
): ModelPriceSummary | null {
  const mode = getModelPricingMode(entry)
  if (mode === 'simple' || mode === 'advanced') {
    const expression = String(entry.effective['billing_setting.billing_expr'])
    const tiers = parseTiersFromExpr(
      splitBillingExprAndRequestRules(expression).billingExpr
    )
    const first = tiers.at(0)
    if (!first) return null
    if (first.billingUnit === 'request') {
      return {
        unit: 'request',
        price: Number(first.fixedPrice),
        perImage: Boolean(first.imageCount),
        tiers: tiers.length,
      }
    }
    return {
      unit: 'token',
      input: Number(first.inputPrice ?? 0),
      output: Number(first.outputPrice ?? 0),
      tiers: tiers.length,
    }
  }
  if (mode !== 'legacy') return null
  const price = entry.effective.ModelPrice
  if (price !== undefined) {
    return {
      unit: 'request',
      price: Number(price),
      perImage: Boolean(entry.billing_details?.image_count),
      tiers: 1,
    }
  }
  const lanes = createInitialLaneState(
    pricingRow(entry.model_name, entry.effective)
  )
  return {
    unit: 'token',
    input: Number(lanes.promptPrice),
    output: Number(lanes.prices.completion || lanes.promptPrice),
    tiers: 1,
  }
}

/** The effective legacy prices a conversion starts from, one row per price. */
export function legacyPriceRows(
  preview: {
    modelName: string
    effective: PricingValues
    cacheWriteMode?: CacheWriteMode
    billingDetails?: LegacyBillingDetails
  },
  t: (key: string) => string,
  currency: PricingCurrency
) {
  const before = pricingRow(preview.modelName, preview.effective)
  const lanes = createInitialLaneState(before)
  return {
    perRequest: before.billingMode === 'per-request',
    rows: buildPreviewRows(
      before,
      before.billingMode ?? 'per-token',
      '',
      '',
      lanes.promptPrice,
      lanes.prices,
      lanes.enabled,
      t,
      currency,
      preview.cacheWriteMode,
      preview.billingDetails
    ).filter((row) => row.value !== t('Empty')),
  }
}
