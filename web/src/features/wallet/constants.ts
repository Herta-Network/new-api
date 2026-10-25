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
import type { StripePaymentMethodType } from './types'

// ============================================================================
// Wallet Constants
// ============================================================================

/**
 * Payment platforms a buyer can meet
 */
export const PAYMENT_PLATFORMS = {
  EPAY: 'epay',
  STRIPE: 'stripe',
  CRYPTOMUS: 'cryptomus',
  BALANCE: 'balance',
} as const

/**
 * Stripe brand color for its built-in icon
 */
export const STRIPE_ICON_COLOR = '#635BFF'

/**
 * Brand colors of the icons of Stripe's Alipay and WeChat Pay methods
 */
export const ALIPAY_ICON_COLOR = '#1677FF'
export const WECHAT_PAY_ICON_COLOR = '#07C160'

/**
 * Labels (i18n keys) of the payment method types a Stripe method collects
 */
export const STRIPE_PAYMENT_METHOD_TYPE_LABELS: Record<
  StripePaymentMethodType,
  string
> = {
  card: 'Card',
  alipay: 'Alipay',
  wechat_pay: 'WeChat Pay',
}

/**
 * Default discount rate (no discount)
 */
export const DEFAULT_DISCOUNT_RATE = 1.0

/**
 * How often and for how long the order result dialog re-reads an order still
 * on its way, and when (seconds after opening) it asks the gateway. Asking the
 * gateway shares the strict per-IP limit with creating orders, so it happens
 * only a few times.
 */
export const ORDER_POLL_INTERVAL_MS = 3000
export const ORDER_POLL_WINDOW_MS = 15 * 60 * 1000
export const ORDER_SYNC_SCHEDULE_SECONDS = [0, 10, 30, 90] as const
