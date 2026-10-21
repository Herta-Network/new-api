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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  RedemptionRequest,
  AffiliateTransferRequest,
  ApiResponse,
  RedemptionResponse,
  AffiliateCodeResponse,
  AffiliateTransferResponse,
  CheckoutAction,
  CreatedPaymentOrder,
  PaymentInfo,
  PaymentMethodOption,
  PaymentOrder,
  PaymentOrderPage,
  PaymentPurpose,
  PaymentQuote,
  PurchaseRequest,
  RedemptionInfo,
} from './types'

// ============================================================================
// Wallet API Functions
// ============================================================================

/**
 * Whether codes can be redeemed and where to buy them
 */
export async function getRedemptionInfo(): Promise<RedemptionInfo> {
  const res = await api.get<ApiResponse<RedemptionInfo>>('/api/user/topup/info')
  return requireServerSuccess(res.data).data as RedemptionInfo
}

/**
 * Redeem a topup code
 */
export async function redeemTopupCode(
  request: RedemptionRequest
): Promise<RedemptionResponse> {
  const res = await api.post('/api/user/topup', request)
  return res.data
}

/**
 * Get affiliate code
 */
export async function getAffiliateCode(): Promise<AffiliateCodeResponse> {
  const res = await api.get('/api/user/aff')
  return res.data
}

/**
 * Transfer affiliate quota to balance
 */
export async function transferAffiliateQuota(
  request: AffiliateTransferRequest
): Promise<AffiliateTransferResponse> {
  const res = await api.post('/api/user/aff_transfer', request)
  return res.data
}

// ============================================================================
// Payments: each call throws the server's message when it answers a failure.
// ============================================================================

export async function getPaymentInfo(): Promise<PaymentInfo> {
  const res = await api.get<ApiResponse<PaymentInfo>>('/api/payment/info')
  return requireServerSuccess(res.data).data as PaymentInfo
}

export async function getPaymentMethods(
  purpose: PaymentPurpose
): Promise<PaymentMethodOption[]> {
  const res = await api.get<ApiResponse<PaymentMethodOption[]>>(
    '/api/payment/methods',
    { params: { purpose } }
  )
  return requireServerSuccess(res.data).data ?? []
}

export async function quotePurchase(
  request: PurchaseRequest
): Promise<PaymentQuote> {
  const res = await api.post<ApiResponse<PaymentQuote>>(
    '/api/payment/quote',
    request
  )
  return requireServerSuccess(res.data).data as PaymentQuote
}

/** Create an order and start its payment. */
export async function createPaymentOrder(
  request: PurchaseRequest
): Promise<CreatedPaymentOrder> {
  const res = await api.post<ApiResponse<CreatedPaymentOrder>>(
    '/api/payment/orders',
    request
  )
  return requireServerSuccess(res.data).data as CreatedPaymentOrder
}

/** Start the payment of a pending order again. */
export async function checkoutPaymentOrder(
  orderNo: string
): Promise<CheckoutAction> {
  const res = await api.post<ApiResponse<CheckoutAction>>(
    `/api/payment/orders/${encodeURIComponent(orderNo)}/checkout`
  )
  return requireServerSuccess(res.data).data as CheckoutAction
}

/** Ask the gateway whether an order was paid; settles it if so. */
export async function syncPaymentOrder(orderNo: string): Promise<PaymentOrder> {
  const res = await api.post<ApiResponse<PaymentOrder>>(
    `/api/payment/orders/${encodeURIComponent(orderNo)}/sync`
  )
  return requireServerSuccess(res.data).data as PaymentOrder
}

/** Give up a pending order. */
export async function closePaymentOrder(
  orderNo: string
): Promise<PaymentOrder> {
  const res = await api.post<ApiResponse<PaymentOrder>>(
    `/api/payment/orders/${encodeURIComponent(orderNo)}/close`
  )
  return requireServerSuccess(res.data).data as PaymentOrder
}

export async function getPaymentOrder(orderNo: string): Promise<PaymentOrder> {
  const res = await api.get<ApiResponse<PaymentOrder>>(
    `/api/payment/orders/${encodeURIComponent(orderNo)}`
  )
  return requireServerSuccess(res.data).data as PaymentOrder
}

/** The user's orders, newest first. */
export async function listPaymentOrders(
  page: number,
  pageSize: number
): Promise<PaymentOrderPage> {
  const res = await api.get<ApiResponse<PaymentOrderPage>>(
    '/api/payment/orders',
    { params: { p: page, page_size: pageSize } }
  )
  return requireServerSuccess(res.data).data as PaymentOrderPage
}
