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

import {
  BALANCE_PAYMENT_OPTION_ID,
  PAYMENT_FEE_MODE,
  type PaymentOption,
  type PaymentOptionInput,
} from './payment-methods-api'

// A non-negative decimal with at most two decimal places: fee percentages
// become basis points, fixed fees and amounts hundredths of the site
// currency.
export const HUNDREDTHS_PATTERN = /^\d+(\.\d{1,2})?$/

export type PaymentOptionFormValues = {
  name: string
  icon: string
  description: string
  sort: string
  enabled: boolean
  fee_mode: string
  /** Percent of the price, e.g. "2.9". */
  fee_rate: string
  /** Site currency amount, e.g. "0.30". */
  fee_fixed: string
}

export function usesFeeRate(mode: number) {
  return (
    mode === PAYMENT_FEE_MODE.RATE || mode === PAYMENT_FEE_MODE.RATE_AND_FIXED
  )
}

export function usesFixedFee(mode: number) {
  return (
    mode === PAYMENT_FEE_MODE.FIXED || mode === PAYMENT_FEE_MODE.RATE_AND_FIXED
  )
}

/** Hundredths written as a decimal: 290 → "2.9", 30 → "0.3". */
export function hundredthsToDecimal(value: number) {
  return String(value / 100)
}

export function decimalToHundredths(value: string) {
  return Math.round(Number(value.trim()) * 100)
}

export const paymentOptionSchema = z
  .object({
    name: z
      .string()
      .trim()
      .min(1, 'Name is required')
      .max(100, 'Use at most 100 characters'),
    icon: z.string().trim().max(255, 'Use at most 255 characters'),
    description: z.string(),
    sort: z
      .string()
      .trim()
      .regex(/^-?\d+$/, 'Enter a whole number'),
    enabled: z.boolean(),
    fee_mode: z.string(),
    fee_rate: z.string(),
    fee_fixed: z.string(),
  })
  .superRefine((values, ctx) => {
    const mode = Number(values.fee_mode)
    const rate = values.fee_rate.trim()
    if (
      usesFeeRate(mode) &&
      (!HUNDREDTHS_PATTERN.test(rate) || Number(rate) > 100)
    ) {
      ctx.addIssue({
        code: 'custom',
        path: ['fee_rate'],
        message:
          'Enter a percentage from 0 to 100 with at most two decimal places',
      })
    }
    if (
      usesFixedFee(mode) &&
      !HUNDREDTHS_PATTERN.test(values.fee_fixed.trim())
    ) {
      ctx.addIssue({
        code: 'custom',
        path: ['fee_fixed'],
        message: 'Enter an amount with at most two decimal places',
      })
    }
  })

export function paymentOptionFormDefaults(
  option: PaymentOption | null
): PaymentOptionFormValues {
  return {
    name: option?.name ?? '',
    icon: option?.icon ?? '',
    description: option?.description ?? '',
    sort: String(option?.sort ?? 0),
    enabled: option?.enabled ?? true,
    fee_mode: String(option?.fee_mode ?? PAYMENT_FEE_MODE.NONE),
    fee_rate: option ? hundredthsToDecimal(option.fee_rate_bp) : '0',
    fee_fixed: option ? hundredthsToDecimal(option.fee_fixed) : '0',
  }
}

/**
 * The request body for the form: the fee percentage in basis points, the
 * fixed fee in hundredths, and fee parts the mode does not use as zero. The
 * balance charges no fee.
 */
export function toPaymentOptionRequest(
  values: PaymentOptionFormValues,
  optionId: number | undefined
): PaymentOptionInput {
  const isBalance = optionId === BALANCE_PAYMENT_OPTION_ID
  const mode = isBalance ? PAYMENT_FEE_MODE.NONE : Number(values.fee_mode)
  return {
    name: values.name.trim(),
    icon: values.icon.trim(),
    description: values.description.trim(),
    sort: Number(values.sort.trim()),
    enabled: values.enabled,
    fee_mode: mode,
    fee_rate_bp: usesFeeRate(mode) ? decimalToHundredths(values.fee_rate) : 0,
    fee_fixed: usesFixedFee(mode) ? decimalToHundredths(values.fee_fixed) : 0,
  }
}
