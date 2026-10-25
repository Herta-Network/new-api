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

import type {
  PaymentMethod,
  PaymentMethodInput,
  PaymentPlatformField,
} from './payment-methods-api'
import {
  decimalToHundredths,
  HUNDREDTHS_PATTERN,
  hundredthsToDecimal,
} from './payment-option-form'

export type PaymentMethodFormValues = {
  platform: string
  name: string
  /** The payment method the channel collects, as a string id. */
  option_id: string
  enabled: boolean
  /** Site currency amounts, e.g. "50.00"; empty leaves that end open. */
  min_amount: string
  max_amount: string
  priority: string
  weight: string
  domain: string
  config: Record<string, string>
}

export function editablePaymentFields(fields: PaymentPlatformField[]) {
  return fields.filter((field) => !field.managed)
}

/**
 * The editable fields that apply to config: a field shown only under a
 * choice of another field (the credentials of an EPay version) is left out
 * while that field holds another value.
 */
export function applicablePaymentFields(
  fields: PaymentPlatformField[],
  config: Record<string, string>
) {
  return editablePaymentFields(fields).filter((field) =>
    Object.entries(field.when ?? {}).every(
      ([key, value]) => config[key] === value
    )
  )
}

const WHOLE_NUMBER = /^\d+$/

/**
 * Validates the channel form. A required secret that is already stored may
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
      option_id: z.string().min(1, 'Choose the payment method it collects'),
      enabled: z.boolean(),
      min_amount: z.string(),
      max_amount: z.string(),
      priority: z.string(),
      weight: z.string(),
      domain: z.string().trim().max(255, 'Use at most 255 characters'),
      config: z.record(z.string(), z.string()),
    })
    .superRefine((values, ctx) => {
      for (const field of applicablePaymentFields(fields, values.config)) {
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
      const min = values.min_amount.trim()
      const max = values.max_amount.trim()
      for (const [path, value] of [
        ['min_amount', min],
        ['max_amount', max],
      ] as const) {
        if (value !== '' && !HUNDREDTHS_PATTERN.test(value)) {
          ctx.addIssue({
            code: 'custom',
            path: [path],
            message: 'Enter an amount with at most two decimal places',
          })
        }
      }
      if (
        min !== '' &&
        max !== '' &&
        HUNDREDTHS_PATTERN.test(min) &&
        HUNDREDTHS_PATTERN.test(max) &&
        Number(max) < Number(min)
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['max_amount'],
          message: 'The highest amount must not be below the lowest',
        })
      }
      const priority = values.priority.trim()
      if (!WHOLE_NUMBER.test(priority) || Number(priority) > 1000) {
        ctx.addIssue({
          code: 'custom',
          path: ['priority'],
          message: 'Enter a whole number from 0 to 1000',
        })
      }
      const weight = values.weight.trim()
      if (
        !WHOLE_NUMBER.test(weight) ||
        Number(weight) < 1 ||
        Number(weight) > 1000
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['weight'],
          message: 'Enter a whole number from 1 to 1000',
        })
      }
    })
}

/** An amount in hundredths as the form shows it; 0 is an open end. */
function amountText(value: number | undefined) {
  return value ? hundredthsToDecimal(value) : ''
}

export function paymentMethodFormDefaults(
  fields: PaymentPlatformField[],
  method: PaymentMethod | null,
  platform: string,
  optionId?: number
): PaymentMethodFormValues {
  const config: Record<string, string> = {}
  for (const field of editablePaymentFields(fields)) {
    const value = method?.config[field.key]
    // A new channel starts on the first choice of a select field.
    const initial = method ? '' : (field.options?.[0]?.value ?? '')
    config[field.key] = typeof value === 'string' ? value : initial
  }
  const option = method?.option_id ?? optionId
  return {
    platform: method?.platform ?? platform,
    name: method?.name ?? '',
    option_id: option === undefined ? '' : String(option),
    enabled: method?.enabled ?? true,
    min_amount: amountText(method?.min_amount),
    max_amount: amountText(method?.max_amount),
    priority: String(method?.priority ?? 0),
    weight: String(method?.weight ?? 1),
    domain: method?.domain ?? '',
    config,
  }
}

/**
 * The request body for the form: the configuration fields that apply, and
 * the amount range in hundredths, an empty end as 0.
 */
export function toPaymentMethodRequest(
  values: PaymentMethodFormValues,
  fields: PaymentPlatformField[]
): PaymentMethodInput {
  const config: Record<string, string> = {}
  for (const field of applicablePaymentFields(fields, values.config)) {
    config[field.key] = values.config[field.key]?.trim() ?? ''
  }
  const amount = (value: string) =>
    value.trim() === '' ? 0 : decimalToHundredths(value)
  return {
    name: values.name.trim(),
    platform: values.platform,
    option_id: Number(values.option_id),
    enabled: values.enabled,
    min_amount: amount(values.min_amount),
    max_amount: amount(values.max_amount),
    priority: Number(values.priority.trim()),
    weight: Number(values.weight.trim()),
    domain: values.domain.trim(),
    config,
  }
}
