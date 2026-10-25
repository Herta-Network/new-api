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
import type { StatusVariant } from '@/components/status-badge'

export const PAYMENT_ORDERS_SECTION_IDS = ['orders', 'unmatched'] as const
export type PaymentOrdersSectionId = (typeof PAYMENT_ORDERS_SECTION_IDS)[number]
export const PAYMENT_ORDERS_DEFAULT_SECTION: PaymentOrdersSectionId = 'orders'

export const PAYMENT_ORDERS_SECTION_LABELS: Record<
  PaymentOrdersSectionId,
  string
> = {
  orders: 'Orders',
  unmatched: 'Unmatched payments',
}

/** Order statuses (model.PaymentOrder*). */
export const PAYMENT_ORDER_STATUS = {
  PENDING: 1,
  PAID: 2,
  CLOSED: 3,
  FINISHED: 5,
} as const

export const PAYMENT_ORDER_STATUSES: Record<
  number,
  { labelKey: string; variant: StatusVariant }
> = {
  [PAYMENT_ORDER_STATUS.PENDING]: {
    labelKey: 'Awaiting payment',
    variant: 'info',
  },
  [PAYMENT_ORDER_STATUS.PAID]: {
    labelKey: 'Paid, not fulfilled',
    variant: 'warning',
  },
  [PAYMENT_ORDER_STATUS.CLOSED]: { labelKey: 'Closed', variant: 'neutral' },
  [PAYMENT_ORDER_STATUS.FINISHED]: {
    labelKey: 'Completed',
    variant: 'success',
  },
}

export const PAYMENT_PURPOSES: Record<string, string> = {
  topup: 'Top-up',
  subscription: 'Subscription',
}

/** Payment platforms in filter order (balance last). */
export const PAYMENT_PLATFORM_IDS = [
  'epay',
  'stripe',
  'cryptomus',
  'balance',
] as const

export const PAYMENT_CLOSE_REASONS: Record<string, string> = {
  expired: 'Expired',
  user: 'Closed by the buyer',
  admin: 'Closed by an administrator',
}

/** Invoice statuses (entity.PaymentInvoice*). */
export const PAYMENT_INVOICE_STATUSES: Record<
  number,
  { labelKey: string; variant: StatusVariant }
> = {
  1: { labelKey: 'Awaiting payment', variant: 'info' },
  2: { labelKey: 'Paid', variant: 'success' },
  3: { labelKey: 'Closed', variant: 'neutral' },
}

/** Why an invoice closed, beyond the reasons its order closed for. */
export const PAYMENT_INVOICE_CLOSE_REASONS: Record<string, string> = {
  ...PAYMENT_CLOSE_REASONS,
  order_paid: 'The order was paid through another invoice',
  manual: 'The order was marked as paid',
  failed: 'The channel could not start the payment',
  replaced: 'Replaced by a newer invoice',
}

export const PAYMENT_UNMATCHED_STATES = [
  { value: 'false', labelKey: 'Unresolved' },
  { value: 'true', labelKey: 'Resolved' },
] as const

/** Longest note the server accepts for a manual completion or a resolve. */
export const PAYMENT_NOTE_MAX_LENGTH = 500
