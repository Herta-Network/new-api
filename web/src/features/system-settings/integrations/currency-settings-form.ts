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
import * as z from 'zod'

import type { ExchangeRatePair } from './exchange-rates-api'

/** A pinned rate: a positive decimal with at most twelve decimal places. */
const RATE_PATTERN = /^\d+(\.\d{1,12})?$/

export const currencySettingsSchema = z.object({
  currency: z.string().min(1),
  quota_display_mode: z.enum(['currency', 'tokens']),
  exchange_rate_api_key: z.string(),
  /**
   * Pinned rates by pair key ("USD/CNY"); an empty value removes one, and a
   * row left untouched has none.
   */
  overrides: z.record(
    z.string(),
    z
      .string()
      .trim()
      .refine(
        (value) =>
          value === '' || (RATE_PATTERN.test(value) && Number(value) > 0),
        'Enter a positive rate with at most 12 decimal places'
      )
      .optional()
  ),
  billing_api_uses_token_quota: z.boolean(),
})

export type CurrencySettingsValues = z.infer<typeof currencySettingsSchema>

/** Read payment_setting.exchange_rate_overrides; anything else is none. */
export function parseRateOverrides(json: string): Record<string, string> {
  try {
    const parsed: unknown = JSON.parse(json || '{}')
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {}
    }
    return Object.fromEntries(
      Object.entries(parsed).filter(
        (entry): entry is [string, string] => typeof entry[1] === 'string'
      )
    )
  } catch {
    return {}
  }
}

/** The overrides to save: trimmed, empty ones removed, keys sorted. */
export function serializeRateOverrides(
  overrides: Record<string, string | undefined>
): string {
  const kept = Object.entries(overrides)
    .map(([pair, rate]) => [pair, rate?.trim() ?? ''] as const)
    .filter(([, rate]) => rate !== '')
    .sort(([a], [b]) => a.localeCompare(b))
  return JSON.stringify(Object.fromEntries(kept))
}

/**
 * How a rate row reads and which override key it edits: the server's
 * direction (one base in quote), unless a saved override names the pair
 * the other way round, which the row then keeps so the value stays exact.
 */
export function rateRowDirection(
  pair: ExchangeRatePair,
  overrides: Record<string, string | undefined>
): { from: string; to: string; key: string; reversed: boolean } {
  const key = `${pair.base}/${pair.quote}`
  const reverseKey = `${pair.quote}/${pair.base}`
  if (!(key in overrides) && reverseKey in overrides) {
    return { from: pair.quote, to: pair.base, key: reverseKey, reversed: true }
  }
  return { from: pair.base, to: pair.quote, key, reversed: false }
}

/** The rate amounts in the site currency are shown at, as the form stands. */
export type SiteRateStatus =
  | { state: 'usd' | 'loading' | 'missing' }
  | { state: 'manual'; usdRate: number }
  | { state: 'live'; usdRate: number; fetchedAt: number }

/**
 * Which rate amounts in `currency` would be shown at: the one set for its
 * USD pair in the form, else a fresh live rate; none means amounts fall back
 * to USD.
 */
export function siteRateStatus(
  currency: string,
  pairs: ExchangeRatePair[] | undefined,
  overrides: Record<string, string | undefined>
): SiteRateStatus {
  if (currency === 'USD') return { state: 'usd' }
  if (!pairs) return { state: 'loading' }
  const quotaPair = pairs.find((pair) => pair.uses.includes('quota'))
  if (!quotaPair) return { state: 'missing' }
  const direction = rateRowDirection(quotaPair, overrides)
  const pinned = Number(overrides[direction.key]?.trim() || Number.NaN)
  if (pinned > 0) {
    return {
      state: 'manual',
      usdRate: direction.reversed ? 1 / pinned : pinned,
    }
  }
  if (quotaPair.live && quotaPair.status === 'live') {
    return {
      state: 'live',
      usdRate: Number(quotaPair.live.rate),
      fetchedAt: quotaPair.live.fetched_at,
    }
  }
  return { state: 'missing' }
}
