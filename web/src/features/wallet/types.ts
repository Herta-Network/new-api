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
// ============================================================================
// Wallet Type Definitions
// ============================================================================

/**
 * Generic API response
 */
export interface ApiResponse<T = unknown> {
  success?: boolean
  message?: string
  data?: T
}

export type RedemptionResponse = ApiResponse<number>
export type AffiliateCodeResponse = ApiResponse<string>
export type AffiliateTransferResponse = ApiResponse

/**
 * Redemption settings (GET /api/user/topup/info)
 */
export interface RedemptionInfo {
  /** Codes can be redeemed once the compliance terms are confirmed */
  enable_redemption: boolean
  /** Where to buy redemption codes */
  topup_link?: string
}

/**
 * Redemption code request
 */
export interface RedemptionRequest {
  key: string
}

/**
 * Affiliate quota transfer request
 */
export interface AffiliateTransferRequest {
  quota: number
}

/**
 * User wallet data
 */
export interface UserWalletData {
  id: number
  username: string
  /** Current quota balance */
  quota: number
  used_quota: number
  request_count: number
  /** Affiliate quota (pending rewards) */
  aff_quota: number
  /** Total affiliate quota earned (historical) */
  aff_history_quota: number
  aff_count: number
  group: string
}

// ============================================================================
// Payments (/api/payment)
// ============================================================================

export type PaymentPurpose = 'topup' | 'subscription'

export type PaymentPlatform = 'epay' | 'stripe' | 'cryptomus' | 'balance'

/** The one Stripe payment method type a Stripe payment method collects. */
export type StripePaymentMethodType = 'card' | 'alipay' | 'wechat_pay'

/** A web console message: an English locale key plus its params. */
export interface ServerMessage {
  key: string
  params?: Record<string, unknown>
}

/** What the wallet needs to offer a top-up (GET /api/payment/info). */
export interface PaymentInfo {
  /** Site currency every price and top-up amount is in */
  currency: string
  /** Smallest top-up in the site currency, a decimal string */
  min_topup: string
  /** Preset top-up amounts in whole units of the site currency */
  amount_options: number[]
  /** Discount factor (0-1) by preset amount */
  amount_discount: Record<string, number>
  payment_compliance_confirmed: boolean
  payment_compliance_terms_version: string
}

/** Prices a payment method takes, in hundredths; a max of 0 is open-ended. */
export interface AmountRange {
  min: number
  max: number
}

/**
 * A payment method as a buyer sees it (GET /api/payment/methods): never the
 * channels that collect it.
 */
export interface PaymentMethodOption {
  id: number
  name: string
  icon: string
  description: string
  fee_mode: number
  fee_rate_bp: number
  fee_fixed: number
  /** The wallet balance */
  balance: boolean
  /** The prices it takes; empty takes any */
  amount_ranges: AmountRange[]
  available: boolean
  /** Why the method cannot take an order now */
  unavailable?: ServerMessage
}

/** What a buyer asks to buy (POST /api/payment/quote and /orders). */
export interface PurchaseRequest {
  purpose: PaymentPurpose
  payment_option_id: number
  /** Top-up amount in the site currency, at most two decimal places */
  amount?: number
  plan_id?: number
}

/**
 * The price of a purchase. Amounts count hundredths of `currency`;
 * amount = price + adjustment + fee_amount.
 */
export interface PaymentQuote {
  purpose: PaymentPurpose
  currency: string
  subject: string
  topup_amount: number
  plan_id: number
  price: number
  /** What the group ratio and the amount discount change; negative for a discount */
  adjustment: number
  fee_amount: number
  amount: number
  /** Quota a top-up credits or a balance purchase deducts */
  quota_amount: number
  quota_rate: string
  /** What the gateway collects, in hundredths of payment_currency; empty for the balance */
  payment_amount: number
  payment_currency: string
}

export interface StripeCheckout {
  publishable_key: string
  client_secret: string
  /** Selects how the client confirms the intent */
  payment_method_type: StripePaymentMethodType
}

/**
 * How to pay an order: open a URL, confirm with Stripe.js, scan a QR code
 * (its content, not an image), or nothing left to pay.
 */
export type CheckoutAction =
  | { type: 'url'; url: string; order_no: string }
  | { type: 'stripe'; stripe: StripeCheckout; order_no: string }
  | { type: 'qrcode'; qr_code: string; order_no: string }
  | { type: 'paid'; order_no: string }

export const PAYMENT_ORDER_STATUS = {
  PENDING: 1,
  PAID: 2,
  CLOSED: 3,
  FINISHED: 5,
} as const

export type PaymentOrderStatus =
  (typeof PAYMENT_ORDER_STATUS)[keyof typeof PAYMENT_ORDER_STATUS]

/**
 * A payment order as its buyer sees it; amounts count hundredths of their
 * currency. It names the payment method, never the channel that collected
 * it.
 */
export interface PaymentOrder {
  order_no: string
  purpose: PaymentPurpose
  plan_id: number
  subject: string
  topup_amount: number
  quota_amount: number
  currency: string
  price: number
  adjustment: number
  fee_amount: number
  amount: number
  /** The payment method's name */
  payment_option: string
  /** Paid with the wallet balance */
  balance: boolean
  /** What the buyer is charged once a checkout started */
  payment_amount: number
  payment_currency: string
  status: PaymentOrderStatus
  fulfil_error: string
  close_reason: string
  created_at: number
  expires_at: number
  paid_at: number
  finished_at: number
  closed_at: number
}

export interface CreatedPaymentOrder {
  order: PaymentOrder
  checkout: CheckoutAction
}

export interface PaymentOrderPage {
  page: number
  page_size: number
  total: number
  items: PaymentOrder[]
}
