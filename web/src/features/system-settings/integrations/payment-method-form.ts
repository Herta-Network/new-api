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
  BALANCE_PAYMENT_PLATFORM,
  PAYMENT_FEE_MODE,
  type PaymentMethod,
  type PaymentMethodInput,
  type PaymentPlatformField,
} from './payment-methods-api'

// A non-negative decimal with at most two decimal places: fee percentages
// become basis points and fixed fees hundredths of the site currency.
const HUNDREDTHS_PATTERN = /^\d+(\.\d{1,2})?$/

export type PaymentMethodFormValues = {
  platform: string
  name: string
  icon: string
  description: string
  sort: string
  enabled: boolean
  domain: string
  config: Record<string, string>
  fee_mode: string
  /** Percent of the price, e.g. "2.9". */
  fee_rate: string
  /** Site currency amount, e.g. "0.30". */
  fee_fixed: string
}

export function editablePaymentFields(fields: PaymentPlatformField[]) {
  return fields.filter((field) => !field.managed)
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
function hundredthsToDecimal(value: number) {
  return String(value / 100)
}

function decimalToHundredths(value: string) {
  return Math.round(Number(value.trim()) * 100)
}

/**
 * Validates the method form. A required secret that is already stored may
 * stay blank, since a blank secret keeps the stored value.
 */
export function createPaymentMethodSchema(
  fields: PaymentPlatformField[],
  secretsSet: Record<string, boolean>
) {
  return z
    .object({
      platform: z.string().min(1, 'Select a platform'),
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
      domain: z.string().trim().max(255, 'Use at most 255 characters'),
      config: z.record(z.string(), z.string()),
      fee_mode: z.string(),
      fee_rate: z.string(),
      fee_fixed: z.string(),
    })
    .superRefine((values, ctx) => {
      for (const field of editablePaymentFields(fields)) {
        const value = values.config[field.key]?.trim() ?? ''
        const kept = field.secret && secretsSet[field.key]
        if (field.required && !value && !kept) {
          ctx.addIssue({
            code: 'custom',
            path: ['config', field.key],
            message: 'This field is required',
          })
        }
      }
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
}

export function paymentMethodFormDefaults(
  fields: PaymentPlatformField[],
  method: PaymentMethod | null,
  platform: string
): PaymentMethodFormValues {
  const config: Record<string, string> = {}
  for (const field of editablePaymentFields(fields)) {
    const value = method?.config[field.key]
    config[field.key] = typeof value === 'string' ? value : ''
  }
  return {
    platform: method?.platform ?? platform,
    name: method?.name ?? '',
    icon: method?.icon ?? '',
    description: method?.description ?? '',
    sort: String(method?.sort ?? 0),
    enabled: method?.enabled ?? true,
    domain: method?.domain ?? '',
    config,
    fee_mode: String(method?.fee_mode ?? PAYMENT_FEE_MODE.NONE),
    fee_rate: method ? hundredthsToDecimal(method.fee_rate_bp) : '0',
    fee_fixed: method ? hundredthsToDecimal(method.fee_fixed) : '0',
  }
}

/**
 * The request body for the form: the fee percentage in basis points, the
 * fixed fee in hundredths, and fee parts the mode does not use as zero.
 */
export function toPaymentMethodRequest(
  values: PaymentMethodFormValues,
  fields: PaymentPlatformField[]
): PaymentMethodInput {
  const mode = Number(values.fee_mode)
  const config: Record<string, string> = {}
  for (const field of editablePaymentFields(fields)) {
    config[field.key] = values.config[field.key]?.trim() ?? ''
  }
  const isBalance = values.platform === BALANCE_PAYMENT_PLATFORM
  return {
    name: values.name.trim(),
    platform: values.platform,
    icon: values.icon.trim(),
    description: values.description.trim(),
    sort: Number(values.sort.trim()),
    enabled: values.enabled,
    domain: isBalance ? '' : values.domain.trim(),
    config: isBalance ? {} : config,
    fee_mode: isBalance ? PAYMENT_FEE_MODE.NONE : mode,
    fee_rate_bp:
      !isBalance && usesFeeRate(mode)
        ? decimalToHundredths(values.fee_rate)
        : 0,
    fee_fixed:
      !isBalance && usesFixedFee(mode)
        ? decimalToHundredths(values.fee_fixed)
        : 0,
  }
}
