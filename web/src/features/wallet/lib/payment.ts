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
import { getServerMessage } from '@/lib/server-error-message'

import type { PaymentInfo } from '../types'

// ============================================================================
// Payment Helpers
// ============================================================================

/**
 * Payment endpoints answer errors as {"message": "error", "data": text}. When
 * the response also carries a message_key, this is its translation; callers
 * fall back to the text they already read.
 */
export function getPaymentErrorTranslation(
  response: unknown
): string | undefined {
  if (!response || typeof response !== 'object') return undefined
  const body = response as Record<string, unknown>
  if (typeof body.message_key !== 'string' || !body.message_key) {
    return undefined
  }
  return getServerMessage(body)
}

const AMOUNT_PATTERN = /^(\d{1,12})(?:\.(\d{1,2}))?$/

/**
 * Parse a top-up amount typed in the site currency into hundredths. Only
 * plain decimals with at most two decimal places are accepted, as the
 * server does.
 */
export function parseMinorAmount(value: string): number | null {
  const match = AMOUNT_PATTERN.exec(value.trim())
  if (!match) return null
  const fraction = (match[2] ?? '').padEnd(2, '0')
  return Number(match[1]) * 100 + Number(fraction)
}

/**
 * The discount factor (0-1) of a top-up amount in hundredths; only whole
 * preset amounts carry one.
 */
export function getAmountDiscount(
  info: PaymentInfo | undefined,
  minor: number
): number {
  if (!info || minor % 100 !== 0) return 1
  const discount = info.amount_discount?.[String(minor / 100)]
  return typeof discount === 'number' && discount > 0 ? discount : 1
}
